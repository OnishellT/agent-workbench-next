package hosts

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

var errHostHelperPending = errors.New("operación del host bloqueada: el helper Go de Workbench todavía no está disponible para esta ruta")

func (s *Server) magpieHostRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/hosts/{id}/magpie/status", s.hostAgentStatusRoute)
	mux.HandleFunc("POST /api/hosts/{id}/magpie/install", s.hostAgentInstallRoute)
	// Only the models surface is proxied now that Work and its terminal are
	// retired; their host routes stay unregistered so they answer 404.
	handler := s.hostAgentProxyRoute("models")
	mux.HandleFunc("/api/hosts/{id}/models", handler)
	mux.HandleFunc("/api/hosts/{id}/models/{tail...}", handler)
	blocked := func(w http.ResponseWriter, _ *http.Request) { fail(w, http.StatusNotImplemented, errHostHelperPending) }
	mux.HandleFunc("/api/hosts/{id}/magpie/{tail...}", blocked)
}

type remoteNativePorts struct {
	Site    int `json:"site"`
	Web     int `json:"web"`
	Gateway int `json:"gateway"`
}

// NativeMagpieReady validates only the helper-owned release and its private
// native config; no provider or usage request is made while resolving a UI.
func (m *Module) NativeMagpieReady(ctx context.Context, hostID string) error {
	if m == nil || m.SSH == nil {
		return errors.New("remote native Magpie transport is unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return m.SSH.SSHOperation(ctx, hostID, func(opCtx context.Context, client *ssh.Client, identity Identity) error {
		endpoint, err := readRemoteNativeMagpieEndpoint(opCtx, client, identity, true)
		clear(endpoint.key)
		return err
	})
}

func (m *Module) NativeMagpieSiteHandler(hostID string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m == nil || m.SSH == nil {
			http.Error(w, "native Magpie is unavailable", http.StatusServiceUnavailable)
			return
		}
		err := m.SSH.SSHOperation(r.Context(), hostID, func(opCtx context.Context, client *ssh.Client, identity Identity) error {
			endpoint, err := readRemoteNativeMagpieEndpoint(opCtx, client, identity, false)
			if err != nil {
				return err
			}
			defer clear(endpoint.key)
			target, _ := url.Parse("http://" + endpoint.address)
			transport := endpoint.transport(client)
			defer transport.CloseIdleConnections()
			proxy := &httputil.ReverseProxy{
				Rewrite: func(req *httputil.ProxyRequest) {
					out := req.Out
					out.URL.Scheme, out.URL.Host = target.Scheme, target.Host
					out.Host = endpoint.address
					for name := range out.Header {
						lower := strings.ToLower(name)
						if lower == "host" || lower == "origin" || lower == "cookie" || lower == "authorization" || lower == "proxy-authorization" || lower == "referer" || lower == "forwarded" || strings.HasPrefix(lower, "x-forwarded-") || lower == "x-real-ip" || lower == "cf-connecting-ip" || lower == "true-client-ip" {
							out.Header.Del(name)
						}
					}
					out.Header.Set("Cookie", endpoint.cookie())
					if req.In.Header.Get("Origin") != "" {
						out.Header.Set("Origin", "http://"+endpoint.address)
					}
				},
				Transport: transport,
				ModifyResponse: func(response *http.Response) error {
					for _, cookie := range response.Header.Values("Set-Cookie") {
						if strings.HasPrefix(strings.ToLower(cookie), "magpie_web_") || strings.HasPrefix(strings.ToLower(cookie), "workbench_magpie_") {
							response.Header.Del("Set-Cookie")
						}
					}
					return nil
				},
				ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
					http.Error(w, "native Magpie is unavailable", http.StatusServiceUnavailable)
				},
				FlushInterval: -1,
			}
			proxy.ServeHTTP(w, r)
			return nil
		})
		if err != nil && r.Context().Err() == nil {
			http.Error(w, "native Magpie is unavailable", http.StatusServiceUnavailable)
		}
	})
}

type remoteNativeMagpieEndpoint struct {
	address string
	port    int
	key     []byte
}

func (e remoteNativeMagpieEndpoint) cookie() string {
	return "magpie_web_" + strconv.Itoa(e.port) + "=" + string(e.key)
}

func (e remoteNativeMagpieEndpoint) transport(client *ssh.Client) *http.Transport {
	return &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, host string) (net.Conn, error) {
		if network != "tcp" || host != e.address {
			return nil, errors.New("remote native Magpie tunnel target rejected")
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		conn, err := client.Dial(network, host)
		if err == nil {
			context.AfterFunc(ctx, func() { _ = conn.Close() })
		}
		return conn, err
	}}
}

func readRemoteNativeMagpieEndpoint(ctx context.Context, client *ssh.Client, identity Identity, verifyBinary bool) (remoteNativeMagpieEndpoint, error) {
	target, err := discoverHostAgentTarget(ctx, client, identity.HostID)
	if err != nil {
		return remoteNativeMagpieEndpoint{}, err
	}
	fs, err := sftp.NewClient(client)
	if err != nil {
		return remoteNativeMagpieEndpoint{}, errors.New("SFTP no disponible para native Magpie")
	}
	defer fs.Close()
	state, err := inspectRemoteHostAgentAt(ctx, client, fs, identity, target)
	clear(state.token)
	if err != nil {
		return remoteNativeMagpieEndpoint{}, err
	}
	if !state.status.Ready {
		return remoteNativeMagpieEndpoint{}, errHostAgentNotReady
	}
	if verifyBinary {
		if err := verifyRemoteNativeMagpie(ctx, client, fs, target, identity, ""); err != nil {
			return remoteNativeMagpieEndpoint{}, errHostAgentNotReady
		}
	} else {
		if _, err := readRemoteNativeMagpieOwner(fs, target, identity, ""); err != nil {
			return remoteNativeMagpieEndpoint{}, errHostAgentNotReady
		}
	}
	modelsDir := target.stateDir
	if err := verifyRemoteDirectory(fs, modelsDir, target.uid, 0o700); err != nil {
		return remoteNativeMagpieEndpoint{}, errHostAgentConflict
	}
	portsBytes, _, err := readRemotePrivateFile(fs, path.Join(modelsDir, "native-ports.json"), target.uid, 0o600, 4096)
	if err != nil {
		return remoteNativeMagpieEndpoint{}, errHostAgentNotReady
	}
	var ports remoteNativePorts
	decoder := json.NewDecoder(strings.NewReader(string(portsBytes)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&ports) != nil || ports.Site < 1 || ports.Site > 65535 || ports.Web < 1 || ports.Web > 65535 || ports.Gateway < 1 || ports.Gateway > 65535 || ports.Site == ports.Web || ports.Site == ports.Gateway || ports.Web == ports.Gateway {
		return remoteNativeMagpieEndpoint{}, errHostAgentConflict
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return remoteNativeMagpieEndpoint{}, errHostAgentConflict
	}
	key, _, err := readRemotePrivateFile(fs, path.Join(modelsDir, "native-web.key"), target.uid, 0o600, 128)
	if err != nil {
		return remoteNativeMagpieEndpoint{}, errHostAgentNotReady
	}
	decoded, err := base64.RawURLEncoding.DecodeString(string(key))
	if err != nil || len(decoded) != 32 {
		clear(key)
		clear(decoded)
		return remoteNativeMagpieEndpoint{}, errHostAgentConflict
	}
	clear(decoded)
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(ports.Web))
	return remoteNativeMagpieEndpoint{address: address, port: ports.Web, key: key}, nil
}

type nativeMagpieOwner struct {
	HostID     string `json:"host_id"`
	UID        int    `json:"uid"`
	Hash       string `json:"hash"`
	Nonce      string `json:"nonce"`
	Executable string `json:"executable"`
}

func openNativeMagpieBinary() (hostAgentBinary, error) {
	name := strings.TrimSpace(os.Getenv("WORKBENCH_MAGPIE_BIN"))
	if name == "" {
		workbench, err := os.Executable()
		if err != nil {
			return hostAgentBinary{}, errors.New("native Magpie binary is required")
		}
		name = filepath.Join(filepath.Dir(workbench), "magpie")
	}
	if !filepath.IsAbs(name) {
		return hostAgentBinary{}, errors.New("WORKBENCH_MAGPIE_BIN must be absolute")
	}
	file, err := os.Open(name)
	if err != nil {
		return hostAgentBinary{}, errors.New("native Magpie binary is unavailable")
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 || info.Size() < 1 || info.Size() > hostAgentMaxBinary {
		_ = file.Close()
		return hostAgentBinary{}, errors.New("native Magpie release binary is invalid")
	}
	hasher := sha256.New()
	size, err := io.Copy(hasher, io.LimitReader(file, hostAgentMaxBinary+1))
	if err != nil || size != info.Size() {
		_ = file.Close()
		return hostAgentBinary{}, errors.New("native Magpie release binary could not be hashed")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return hostAgentBinary{}, err
	}
	return hostAgentBinary{file: file, size: info.Size(), hash: hex.EncodeToString(hasher.Sum(nil))}, nil
}

func installRemoteNativeMagpie(ctx context.Context, fs *sftp.Client, target hostAgentTarget, identity Identity, binary hostAgentBinary, nonce string) ([]string, error) {
	ownerPath := path.Join(target.stateDir, NativeMagpieOwnerFile)
	binaryPath := path.Join(target.stateDir, "bin", "magpie")
	for _, name := range []string{ownerPath, binaryPath} {
		if _, err := fs.Lstat(name); err == nil || !os.IsNotExist(err) {
			return nil, errHostAgentConflict
		}
	}
	if err := uploadHostAgentBinary(ctx, binary.file, binary.size, binary.hash, fs, binaryPath, nonce, target.uid); err != nil {
		return nil, err
	}
	owner := nativeMagpieOwner{HostID: identity.HostID, UID: target.uid, Hash: binary.hash, Nonce: nonce, Executable: "magpie"}
	ownerData, err := json.Marshal(owner)
	if err != nil {
		_ = fs.Remove(binaryPath)
		return nil, err
	}
	if err := writeRemotePrivateFile(fs, ownerPath, ownerData, 0o600, nonce, target.uid); err != nil {
		_ = fs.Remove(binaryPath)
		return nil, err
	}
	return []string{binaryPath, ownerPath}, nil
}

func verifyRemoteNativeMagpie(ctx context.Context, client *ssh.Client, fs *sftp.Client, target hostAgentTarget, identity Identity, expectedHash string) error {
	owner, err := readRemoteNativeMagpieOwner(fs, target, identity, expectedHash)
	if err != nil {
		return err
	}
	binDir := path.Join(target.stateDir, "bin")
	hash, err := remoteBinaryDigest(ctx, client, fs, path.Join(binDir, "magpie"), target.uid, 0o700)
	if err != nil || hash != owner.Hash {
		return errHostAgentConflict
	}
	return nil
}

func readRemoteNativeMagpieOwner(fs *sftp.Client, target hostAgentTarget, identity Identity, expectedHash string) (nativeMagpieOwner, error) {
	ownerPath := path.Join(target.stateDir, NativeMagpieOwnerFile)
	data, _, err := readRemotePrivateFile(fs, ownerPath, target.uid, 0o600, hostAgentMaxStateFile)
	if err != nil {
		return nativeMagpieOwner{}, errHostAgentConflict
	}
	var owner nativeMagpieOwner
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&owner) != nil {
		return nativeMagpieOwner{}, errHostAgentConflict
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || owner.HostID != identity.HostID || owner.UID != target.uid || owner.Executable != "magpie" || !hostAgentHashPattern.MatchString(owner.Hash) || !hostAgentNoncePattern.MatchString(owner.Nonce) || expectedHash != "" && owner.Hash != expectedHash {
		return nativeMagpieOwner{}, errHostAgentConflict
	}
	if err := verifyRemoteDirectory(fs, path.Join(target.stateDir, "bin"), target.uid, 0o700); err != nil {
		return nativeMagpieOwner{}, errHostAgentConflict
	}
	return owner, nil
}
