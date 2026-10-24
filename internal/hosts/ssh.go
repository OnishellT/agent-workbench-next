package hosts

import (
	"context"
	"crypto/cipher"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// Match native user installs when non-interactive SSH omits them from PATH.
const nativeSSHPath = `export PATH="${PATH:-/usr/local/bin:/usr/bin:/bin}:$HOME/.local/bin:$HOME/.nix-profile/bin"; `

type hostSync struct {
	cancel context.CancelFunc
	done   chan struct{}
}

type SSHManager struct {
	Store             *Store
	cipher            cipher.AEAD
	mu                sync.Mutex
	running           map[string]*hostSync
	operations        map[string]map[*activeSSHOperation]struct{}
	conversationReads map[string]map[*conversationSSH]struct{}
	closed            bool
	wg                sync.WaitGroup
	slots             chan struct{}
	installing        map[string]struct{}
	// root is the manager lifetime for host contexts; Close is its terminator.
	root context.Context
}

func NewSSHManager(store *Store) (*SSHManager, error) {
	aead, err := secretCipher(store.Dir)
	if err != nil {
		return nil, err
	}
	return &SSHManager{Store: store, cipher: aead, running: map[string]*hostSync{}, operations: make(map[string]map[*activeSSHOperation]struct{}), slots: make(chan struct{}, 2), root: context.Background()}, nil
}

// tryLockInstall serializes dependency installs per host; false means one is
// already running. The returned func releases the claim.
func (m *SSHManager) tryLockInstall(id string) (release func(), ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, busy := m.installing[id]; busy {
		return nil, false
	}
	if m.installing == nil {
		m.installing = map[string]struct{}{}
	}
	m.installing[id] = struct{}{}
	return func() {
		m.mu.Lock()
		delete(m.installing, id)
		m.mu.Unlock()
	}, true
}

func (m *SSHManager) Save(input HostInput) (Host, error) {
	h := input.Host
	if err := h.Validate(); err != nil {
		return h, err
	}
	if len(input.Password) > 4096 {
		return h, fmt.Errorf("contraseña demasiado larga")
	}
	if h.Auth == "key" {
		if _, err := m.allowedKeyPath(h.KeyPath, false); err != nil {
			return h, err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	old, err := m.Store.Host(h.ID)
	if err != nil && err != sql.ErrNoRows {
		return h, err
	}
	if err == nil && input.CreateOnly {
		return h, ErrHostExists
	}
	secret := []byte{}
	if err == nil {
		secret, err = m.Store.hostSecret(h.ID)
		if err != nil {
			return h, err
		}
		// Moving to another endpoint must never silently reuse the former trust pin.
		if old.Address != h.Address || old.Port != h.Port || old.User != h.User {
			old.Fingerprint = ""
			old.ObservedFingerprint = ""
			old.Home = ""
			old.Hostname, old.IP = "", ""
		}
		h.Revision = old.Revision
		h.Fingerprint, h.ObservedFingerprint, h.Home = old.Fingerprint, old.ObservedFingerprint, old.Home
		h.Hostname, h.IP = old.Hostname, old.IP
		h.LastSync, h.LastAttempt = old.LastSync, old.LastAttempt
		h.Files, h.SyncedFiles = old.Files, old.SyncedFiles
	}
	if err == sql.ErrNoRows {
		h.Revision = 0
		h.Fingerprint, h.ObservedFingerprint = "", ""
		h.Hostname, h.IP = "", ""
	}
	if input.Password != "" {
		secret, err = sealPassword(m.cipher, h.ID, input.Password)
		if err != nil {
			return h, err
		}
	}
	h.Revision++
	h.PasswordSet = len(secret) > 0
	if h.Auth == "password" && !h.PasswordSet {
		return h, fmt.Errorf("indica una contraseña para este host")
	}
	h.Status = "pending"
	h.Error = ""
	if !h.Enabled {
		h.Status = "paused"
	}
	if sync := m.running[h.ID]; sync != nil {
		sync.cancel()
	}
	m.cancelOperationsLocked(h.ID)
	err = m.Store.putHost(h, secret)
	if err == nil {
		m.cancelConversationsLocked(h.ID)
	}
	return h, err
}

func (m *SSHManager) Start(id string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return false, fmt.Errorf("servicio SSH detenido")
	}
	h, err := m.Store.Host(id)
	if err != nil {
		return false, err
	}
	if !h.Enabled {
		return false, fmt.Errorf("el host está pausado")
	}
	if m.running[id] != nil {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	operation := &hostSync{cancel: cancel, done: make(chan struct{})}
	m.running[id] = operation
	h.Status, h.Error = "syncing", ""
	h.LastAttempt = time.Now().UTC().Format(time.RFC3339)
	h.SyncedFiles = 0
	if err = m.Store.hostState(h); err != nil {
		delete(m.running, id)
		cancel()
		return false, err
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer close(operation.done)
		defer func() {
			cancel()
			m.mu.Lock()
			if m.running[id] == operation {
				delete(m.running, id)
			}
			m.mu.Unlock()
		}()
		select {
		case m.slots <- struct{}{}:
			defer func() { <-m.slots }()
		case <-ctx.Done():
			h.Status, h.Error = "error", "Sincronización cancelada"
			_ = m.Store.hostState(h)
			return
		}
		if err := m.sync(ctx, &h); err != nil {
			h.Status, h.Error = "error", truncate(err.Error(), 300)
		}
		_ = m.Store.hostState(h)
	}()
	return true, nil
}

func (m *SSHManager) Close() {
	m.mu.Lock()
	m.closed = true
	for _, sync := range m.running {
		sync.cancel()
	}
	for _, active := range m.operations {
		for operation := range active {
			operation.cancel()
		}
	}
	for id := range m.conversationReads {
		m.cancelConversationsLocked(id)
	}
	m.mu.Unlock()
	m.wg.Wait()
}

func (m *SSHManager) connect(ctx context.Context, h *Host) (*ssh.Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, errors.New("servicio SSH detenido")
	}
	current, err := m.Store.Host(h.ID)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if !current.Enabled {
		m.mu.Unlock()
		return nil, ErrPaused
	}
	if current.ObservedFingerprint != "" {
		m.mu.Unlock()
		return nil, ErrIdentityChanged
	}
	if current.Revision != h.Revision || current.Address != h.Address || current.User != h.User || current.Port != h.Port || current.Fingerprint != h.Fingerprint {
		m.mu.Unlock()
		return nil, ErrIdentityChanged
	}
	var encrypted []byte
	if h.Auth == "password" {
		encrypted, err = m.Store.hostSecret(h.ID)
	}
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	auth := []ssh.AuthMethod{}
	var agentConn net.Conn
	switch h.Auth {
	case "password":
		password, err := openPassword(m.cipher, h.ID, encrypted)
		if err != nil {
			return nil, err
		}
		auth = append(auth, ssh.Password(password))
	case "key":
		keyPath, err := m.allowedKeyPath(h.KeyPath, true)
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, fmt.Errorf("no se puede leer la clave privada")
		}
		signer, err := ssh.ParsePrivateKey(data)
		if err != nil {
			return nil, fmt.Errorf("clave privada inválida o cifrada; usa el agente SSH para claves con frase de acceso")
		}
		auth = append(auth, ssh.PublicKeys(signer))
	case "agent":
		var err error
		agentConn, err = net.DialTimeout("unix", os.Getenv("SSH_AUTH_SOCK"), 3*time.Second)
		if err != nil {
			return nil, fmt.Errorf("agente SSH no disponible para el servicio")
		}
		auth = append(auth, ssh.PublicKeysCallback(agent.NewClient(agentConn).Signers))
	}
	if agentConn != nil {
		defer agentConn.Close()
	}
	address := net.JoinHostPort(h.Address, strconv.Itoa(h.Port))
	cfg := &ssh.ClientConfig{User: h.User, Auth: auth, Timeout: 8 * time.Second, HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
		fingerprint := ssh.FingerprintSHA256(key)
		if h.Fingerprint == "" || h.Fingerprint != fingerprint {
			h.ObservedFingerprint = fingerprint
			keyErr := &hostKeyError{changed: h.Fingerprint != ""}
			h.Status, h.Error = "error", truncate(keyErr.Error(), 300)
			m.mu.Lock()
			m.cancelOperationsLocked(h.ID)
			m.cancelConversationsLocked(h.ID)
			persistErr := m.Store.hostState(*h)
			m.mu.Unlock()
			if persistErr != nil {
				return errors.Join(keyErr, persistErr)
			}
			return keyErr
		}
		h.ObservedFingerprint = ""
		return nil
	}}
	conn, err := (&net.Dialer{Timeout: 8 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("SSH no disponible en %s: %w", address, err)
	}
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	if remote, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
		h.IP = remote.IP.String()
	}
	// Lifetime ownership: on success this hook is deliberately NOT stopped. The
	// returned client is owned by ctx, so cancelling ctx (operation end, Save,
	// trust change, pause, Close, install timeout) closes the socket. Every
	// caller therefore passes a cancellable ctx it cancels when done; only the
	// failure path below disarms the hook.
	closer := context.AfterFunc(ctx, func() { conn.Close() })
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, address, cfg)
	if err != nil {
		closer()
		conn.Close()
		return nil, fmt.Errorf("conexión SSH: %w", err)
	}
	conn.SetDeadline(time.Time{})
	return ssh.NewClient(sshConn, chans, reqs), nil
}

// sshKeepaliveInterval paces keepalive@openssh.com requests while an operation
// holds a connection; a reply that does not arrive within two intervals means a
// dead transport and the client is closed so blocked reads/writes fail fast.
var sshKeepaliveInterval = 15 * time.Second

// startSSHKeepalive sends keepalives until stop is called or ctx ends. A
// negative reply (the server rejecting the request) is still a live transport;
// only a transport error or a missing reply closes the client.
func startSSHKeepalive(ctx context.Context, client *ssh.Client) (stop func()) {
	kctx, cancel := context.WithCancel(ctx)
	interval := sshKeepaliveInterval
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-kctx.Done():
				return
			case <-ticker.C:
			}
			replied := make(chan error, 1)
			go func() {
				_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
				replied <- err
			}()
			timer := time.NewTimer(2 * interval)
			select {
			case err := <-replied:
				timer.Stop()
				if err != nil {
					_ = client.Close()
					return
				}
			case <-timer.C:
				_ = client.Close()
				return
			case <-kctx.Done():
				timer.Stop()
				return
			}
		}
	}()
	return cancel
}

// allowedKeyPath resolves a private-key path and only accepts files under the
// service user's ~/.ssh or the Workbench state directory, after resolving
// symlinks, so a stored path can never be used to read arbitrary local files.
//
// With strict=false (Save) a file that does not exist yet is checked
// lexically, so a key can be generated after registering the host; connect
// always uses strict=true.
func (m *SSHManager) allowedKeyPath(name string, strict bool) (string, error) {
	denied := fmt.Errorf("la clave privada debe estar en ~/.ssh o en el directorio de estado de Workbench")
	if name == "" || !filepath.IsAbs(name) {
		return "", denied
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(name))
	if err != nil {
		if strict || !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("no se puede leer la clave privada")
		}
		resolved = filepath.Clean(name)
	}
	roots := []string{}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		roots = append(roots, filepath.Join(home, ".ssh"))
	}
	if m.Store != nil && m.Store.Dir != "" {
		roots = append(roots, m.Store.Dir)
	}
	for _, root := range roots {
		real, err := filepath.EvalSymlinks(root)
		if err != nil {
			if strict {
				continue
			}
			real = root
		}
		if rel, err := filepath.Rel(real, resolved); err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return resolved, nil
		}
	}
	return "", denied
}

func (m *SSHManager) sync(ctx context.Context, h *Host) error {
	client, err := m.connect(ctx, h)
	if err != nil {
		return err
	}
	defer client.Close()
	fs, err := sftp.NewClient(client)
	if err != nil {
		return fmt.Errorf("SFTP no disponible: %w", err)
	}
	defer fs.Close()
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	home, err := session.Output(remoteIdentityCommand)
	_ = session.Close()
	if err != nil {
		return fmt.Errorf("no se pudo leer la carpeta del usuario remoto")
	}
	metadata := strings.Split(strings.TrimSpace(string(home)), "\n")
	if len(metadata) != 2 || len(home) > 8192 {
		return fmt.Errorf("identidad remota inválida")
	}
	h.Home, h.Hostname = strings.TrimSpace(metadata[0]), strings.TrimSpace(metadata[1])
	if !hostNamePattern.MatchString(h.Hostname) || !path.IsAbs(h.Home) || len(h.Home) > 4096 {
		return fmt.Errorf("identidad remota inválida")
	}
	environment, err := remoteCommand(ctx, client, remoteEnvironmentCommand)
	if err != nil {
		return err
	}
	pathEnv, err := parseRemoteEnvironment(environment)
	if err != nil {
		return err
	}
	if err := m.ensureNativeHostSources(h.ID, sshSourceDefaults(h.Home, pathEnv)); err != nil {
		return err
	}
	sources, err := m.Store.Sources()
	if err != nil {
		return err
	}
	h.Files, h.SyncedFiles = 0, 0
	issues := []string{}
	for _, source := range sources {
		if source.HostID != h.ID {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !source.Enabled {
			source.Status = "paused"
			if err := m.Store.sourceState(source); err != nil {
				return err
			}
			continue
		}
		source.Error, source.Files = "", 0
		source.Status = "connected"
		source.LastScan = time.Now().UTC().Format(time.RFC3339)
		count, err := scanRemoteSource(ctx, fs, &source)
		if err != nil {
			return err
		}
		source.Files = count
		h.Files += count
		h.SyncedFiles += count
		if count == 0 && source.Status == "connected" {
			source.Status = "empty"
		}
		if source.Error != "" {
			issues = append(issues, source.Name+": "+source.Error)
		}
		if err := m.Store.sourceState(source); err != nil {
			return err
		}
	}
	h.Status, h.Error = "connected", ""
	h.LastSync = time.Now().UTC().Format(time.RFC3339)
	if len(issues) > 0 {
		h.Status, h.Error = "warning", truncate(strings.Join(issues, " · "), 300)
	}
	return nil
}

// maxSourceFiles caps the files counted per source walk.
const maxSourceFiles = 20000

// scanRemoteSource counts the conversation files of one source over SFTP and
// records the outcome in source.Status/Error. Only a cancelled ctx is an error.
func scanRemoteSource(ctx context.Context, fs *sftp.Client, source *Source) (int, error) {
	info, err := fs.Stat(source.Path)
	if err != nil {
		if os.IsNotExist(err) {
			source.Status = "missing"
		} else {
			source.Status, source.Error = "error", truncate(err.Error(), 200)
		}
		return 0, nil
	}
	if !info.IsDir() {
		if info.Mode().IsRegular() {
			return 1, nil
		}
		return 0, nil
	}
	count := 0
	if source.Kind == "opencode" {
		entries, err := fs.ReadDir(source.Path)
		if err != nil {
			source.Status, source.Error = "warning", truncate(err.Error(), 200)
			return 0, nil
		}
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasPrefix(entry.Name(), "opencode") && strings.HasSuffix(entry.Name(), ".db") {
				count++
			}
		}
		return count, nil
	}
	walker := fs.Walk(source.Path)
	for walker.Step() {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if walkErr := walker.Err(); walkErr != nil {
			source.Status, source.Error = "warning", truncate(walkErr.Error(), 200)
			continue
		}
		item := walker.Stat()
		if item.Mode()&os.ModeSymlink == 0 && item.Mode().IsRegular() && strings.HasSuffix(item.Name(), ".jsonl") {
			count++
			if count > maxSourceFiles {
				source.Status, source.Error = "warning", "límite de 20.000 archivos por fuente"
				break
			}
		}
	}
	return count, nil
}

// Remote probes shared by a manual sync and the automatic conversation capture.
const (
	remoteIdentityCommand    = `printf '%s\n' "$HOME"; hostname 2>/dev/null || uname -n`
	remoteEnvironmentCommand = `printf '%s\0' "$XDG_DATA_HOME" "$CODEX_HOME" "$OMP_AGENT_DIR" "$PI_CODING_AGENT_DIR" "$PI_CONFIG_DIR" "$CLAUDE_CONFIG_DIR"`
)

// parseRemoteEnvironment validates the NUL-separated output of
// remoteEnvironmentCommand and returns the six path variables in order.
func parseRemoteEnvironment(out []byte) ([]string, error) {
	parts := strings.Split(string(out), "\x00")
	if len(parts) != 7 || parts[6] != "" {
		return nil, fmt.Errorf("entorno de rutas remoto inválido")
	}
	for _, value := range parts[:6] {
		if len(value) > 4096 || strings.ContainsAny(value, "\r\n") {
			return nil, fmt.Errorf("ruta del entorno remoto inválida")
		}
	}
	return parts[:6], nil
}

// Derive remote paths lexically: controller filesystem symlinks and HOME must
// never participate in another host's native defaults.
func sshSourceDefaults(home string, environment []string) []Source {
	resolve := func(value, fallback string) string {
		if value == "" {
			return path.Clean(fallback)
		}
		if path.IsAbs(value) {
			return path.Clean(value)
		}
		return path.Join(home, value)
	}
	dataHome := resolve(environment[0], path.Join(home, ".local/share"))
	codex := resolve(environment[1], path.Join(home, ".codex"))
	config := environment[4]
	if config == "" {
		config = ".omp"
	}
	omp := resolve(environment[2], resolve(environment[3], path.Join(home, config, "agent")))
	pi := resolve(environment[3], path.Join(home, ".pi/agent"))
	claude := resolve(environment[5], path.Join(home, ".claude"))
	return []Source{
		{ID: "codex", Name: "Codex", Kind: "codex", Path: path.Join(codex, "sessions"), Description: "Sesiones y subagentes · lectura por SSH"},
		{ID: "codex-archive", Name: "Codex", Kind: "codex", Path: path.Join(codex, "archived_sessions"), Description: "Sesiones archivadas · lectura por SSH"},
		{ID: "claude", Name: "Claude Code", Kind: "claude", Path: path.Join(claude, "projects"), Description: "Sesiones y subagentes · lectura por SSH"},
		{ID: "omp", Name: "OMP · Oh My Pi", Kind: "omp", Path: path.Join(omp, "sessions"), Description: "Sesiones y llamadas auxiliares · lectura por SSH"},
		{ID: "pi", Name: "Pi", Kind: "pi", Path: path.Join(pi, "sessions"), Description: "Sesiones y uso auxiliar · lectura por SSH"},
		{ID: "opencode", Name: "OpenCode", Kind: "opencode", Path: path.Join(dataHome, "opencode"), Description: "SQLite remoto · lectura consistente por SSH"},
	}
}

func (m *SSHManager) ensureNativeHostSources(id string, defaults []Source) error {
	existing, err := m.Store.Sources()
	if err != nil {
		return err
	}
	for _, source := range defaults {
		source.ID = id + "-" + source.ID
		source.HostID = id
		source.Enabled = true
		source.Status = "pending"
		covered := false
		for _, row := range existing {
			if row.ID == source.ID {
				covered = true
				break
			} // Preserve every configured path.
			if row.HostID == id && parserFamily(row.Kind) == parserFamily(source.Kind) {
				parent := path.Clean(row.Path)
				child := path.Clean(source.Path)
				if parent == child || strings.HasPrefix(child, parent+"/") {
					covered = true
					break
				}
			}
		}
		if covered {
			continue
		}
		if err := m.Store.PutSource(source); err != nil {
			return err
		}
		existing = append(existing, source)
	}
	return nil
}
