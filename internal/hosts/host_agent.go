package hosts

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

const (
	hostAgentExecutable = "workbench"
	// hostAgentPartialFile attributes a partial first install to the attempt
	// that staged it. It is written BEFORE any upload while the transport is
	// alive, so a later timeout or dead transport leaves attributable state
	// instead of an ambiguous one. Only marker-listed names are ever removed.
	hostAgentPartialFile  = ".install.partial"
	hostAgentMaxBinary    = 512 << 20
	hostAgentMaxStateFile = 4096
	hostAgentRequestLimit = 4096
)

var (
	hostAgentHashPattern           = regexp.MustCompile(`^[0-9a-f]{64}$`)
	hostAgentNoncePattern          = regexp.MustCompile(`^[0-9a-f]{32}$`)
	errHostAgentCAS                = errors.New("la revisión del host cambió; vuelve a revisar la identidad")
	errHostAgentConflict           = errors.New("existe un estado de host-agent no atribuible; no se modificó")
	errHostAgentInstalling         = errors.New("host-agent installation is already in progress")
	errHostAgentNotReady           = errors.New("host-agent no instalado o no disponible")
	errHostAgentReleaseUnavailable = errors.New("release artifact unavailable")
	errHostAgentCallerAuth         = errors.New("la autorización de inferencia del host no es válida")
)

type hostAgentInstallInput struct {
	Revision int64 `json:"revision"`
}

// hostAgentStatus intentionally excludes the access token, owner nonce, uid,
// binary hash, private paths, pid, and loopback port from the public response.
type hostAgentStatus struct {
	HostID    string `json:"host_id"`
	Revision  int64  `json:"revision"`
	Installed bool   `json:"installed"`
	Ready     bool   `json:"ready"`
	State     string `json:"state"`
	Version   string `json:"version,omitempty"`
	StartedAt string `json:"started_at,omitempty"`
}

type hostAgentTarget struct {
	stateRoot string
	stateDir  string
	uid       int
}

type hostAgentBinary struct {
	file *os.File
	size int64
	hash string
}

type remoteHostAgent struct {
	status hostAgentStatus
	owner  HostAgentOwner
	ready  HostAgentReady
	token  []byte
	exists bool
}

func discoverHostAgentTarget(ctx context.Context, client *ssh.Client, hostID string) (hostAgentTarget, error) {
	const command = `printf '%s\0' "${XDG_STATE_HOME:-$HOME/.local/state}" "$(id -u)" "$(uname -s)" "$(uname -m)"`
	data, err := remoteCommand(ctx, client, command)
	if err != nil {
		return hostAgentTarget{}, errors.New("no se pudo comprobar la plataforma y el estado remoto")
	}
	parts := strings.Split(string(data), "\x00")
	if len(parts) != 5 || parts[4] != "" || strings.ContainsAny(parts[0], "\r\n\x00") || len(parts[0]) > 4096 {
		return hostAgentTarget{}, errors.New("estado remoto inválido")
	}
	uid, err := strconv.Atoi(parts[1])
	if err != nil || uid < 0 || uint64(uid) > uint64(^uint32(0)) {
		return hostAgentTarget{}, errors.New("UID remoto inválido")
	}
	if parts[2] != "Linux" || (parts[3] != "x86_64" && parts[3] != "amd64") {
		return hostAgentTarget{}, errors.New("el host-agent requiere Linux x86-64")
	}
	stateDir, err := HostAgentStateDir(parts[0], hostID)
	if err != nil {
		return hostAgentTarget{}, err
	}
	return hostAgentTarget{stateRoot: parts[0], stateDir: stateDir, uid: uid}, nil
}

func inspectRemoteHostAgent(ctx context.Context, client *ssh.Client, identity Identity) (remoteHostAgent, error) {
	target, err := discoverHostAgentTarget(ctx, client, identity.HostID)
	if err != nil {
		return remoteHostAgent{}, err
	}
	fs, err := sftp.NewClient(client)
	if err != nil {
		return remoteHostAgent{}, errors.New("SFTP no disponible para host-agent")
	}
	defer fs.Close()
	return inspectRemoteHostAgentAt(ctx, client, fs, identity, target)
}

func inspectRemoteHostAgentAt(ctx context.Context, client *ssh.Client, fs *sftp.Client, identity Identity, target hostAgentTarget) (remoteHostAgent, error) {
	result := remoteHostAgent{status: hostAgentStatus{HostID: identity.HostID, Revision: identity.Revision, State: "absent"}}
	exists, err := checkRemoteAgentDirs(fs, target, false)
	if err != nil || !exists {
		return result, err
	}
	result.exists = true
	lockPath := path.Join(target.stateDir, ".install.lock")
	if lockInfo, err := fs.Lstat(lockPath); err == nil {
		if err := verifyRemoteEntry(fs, lockPath, 0o600, true, target.uid); err != nil {
			return result, errHostAgentConflict
		}
		if !clearStaleInstallLock(ctx, client, fs, target, lockPath, lockInfo) {
			result.status.State = "installing"
			return result, nil
		}
	} else if !os.IsNotExist(err) {
		return result, err
	}
	ownerPath := path.Join(target.stateDir, HostAgentOwnerFile)
	ownerData, _, err := readRemotePrivateFile(fs, ownerPath, target.uid, 0o600, hostAgentMaxStateFile)
	if os.IsNotExist(err) {
		entries, readErr := fs.ReadDir(target.stateDir)
		if readErr != nil {
			return result, readErr
		}
		if len(entries) == 0 {
			return result, nil
		}
		return result, errHostAgentConflict
	}
	if err != nil {
		return result, errHostAgentConflict
	}
	if err := decodeStrictJSON(bytes.NewReader(ownerData), &result.owner); err != nil {
		return result, errHostAgentConflict
	}
	owner := result.owner
	if owner.HostID != identity.HostID || owner.UID != target.uid || !hostAgentHashPattern.MatchString(owner.Hash) || !hostAgentNoncePattern.MatchString(owner.Nonce) || owner.Executable != hostAgentExecutable {
		return result, errHostAgentConflict
	}
	tokenPath := path.Join(target.stateDir, HostAgentTokenFile)
	token, _, err := readRemotePrivateFile(fs, tokenPath, target.uid, 0o600, 128)
	if err != nil || !validHostAgentToken(token) {
		return result, errHostAgentConflict
	}
	binDir := path.Join(target.stateDir, "bin")
	if err := verifyRemoteDirectory(fs, binDir, target.uid, 0o700); err != nil {
		return result, errHostAgentConflict
	}
	binaryPath := path.Join(binDir, owner.Executable)
	binaryHash, err := remoteBinaryDigest(ctx, client, fs, binaryPath, target.uid, 0o700)
	if err != nil || binaryHash != owner.Hash {
		return result, errHostAgentConflict
	}
	result.status.Installed = true
	result.status.State = "installed"
	readyPath := path.Join(target.stateDir, HostAgentReadyFile)
	readyData, _, err := readRemotePrivateFile(fs, readyPath, target.uid, 0o600, hostAgentMaxStateFile)
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return result, errHostAgentConflict
	}
	var ready HostAgentReady
	if err := decodeStrictJSON(bytes.NewReader(readyData), &ready); err != nil {
		return result, errHostAgentConflict
	}
	if ready.HostID != owner.HostID || ready.UID != owner.UID || ready.Hash != owner.Hash || ready.Nonce != owner.Nonce || ready.PID < 1 || ready.Port < 1 || ready.Port > 65535 || ready.Version == "" || len(ready.Version) > 128 || strings.ContainsAny(ready.Version, "\r\n") {
		return result, errHostAgentConflict
	}
	if _, err := time.Parse(time.RFC3339Nano, ready.StartedAt); err != nil {
		return result, errHostAgentConflict
	}
	result.status.Version = ready.Version
	result.status.StartedAt = ready.StartedAt
	result.ready = ready
	result.token = token
	if probeHostAgentHealth(ctx, client, ready) {
		result.status.Ready = true
		result.status.State = "ready"
	}
	return result, nil
}

func validHostAgentToken(token []byte) bool {
	if len(token) != base64.RawURLEncoding.EncodedLen(32) {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(string(token))
	return err == nil && len(decoded) == 32 && base64.RawURLEncoding.EncodeToString(decoded) == string(token)
}

func launchHostAgent(ctx context.Context, client *ssh.Client, hostID, stateDir string) error {
	executable := path.Join(stateDir, "bin", hostAgentExecutable)
	readyFile := path.Join(stateDir, HostAgentReadyFile)
	command := "nohup " + shellQuote(executable) + " host-agent --state-dir " + shellQuote(stateDir) + " --host-id " + shellQuote(hostID) + " --ready-file " + shellQuote(readyFile) + " >/dev/null 2>&1 </dev/null &"
	if _, err := remoteCommand(ctx, client, command); err != nil {
		return errors.New("no se pudo iniciar el host-agent instalado")
	}
	return nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func probeHostAgentHealth(ctx context.Context, client *ssh.Client, ready HostAgentReady) bool {
	if err := ctx.Err(); err != nil {
		return false
	}
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(ready.Port))
	conn, err := client.Dial("tcp", address)
	if err != nil {
		return false
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	defer conn.Close()
	deadline := time.Now().Add(1500 * time.Millisecond)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = conn.SetDeadline(deadline)
	request, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/health", ready.Port), nil)
	if err != nil {
		return false
	}
	request.Close = true
	if err := request.Write(conn); err != nil {
		return false
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil || (response.StatusCode != http.StatusOK && response.StatusCode != http.StatusServiceUnavailable) {
		return false
	}
	var health struct {
		Application string            `json:"application"`
		Modules     map[string]string `json:"modules"`
	}
	if json.Unmarshal(body, &health) != nil || health.Application != "agent-workbench" {
		return false
	}
	// The helper only mounts models and settings now that Work and its terminal
	// are retired; readiness must not wait for removed modules.
	return health.Modules["settings"] == "mounted" && health.Modules["models"] == "mounted"
}
