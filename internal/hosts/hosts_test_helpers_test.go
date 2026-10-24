package hosts

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

func newTestModule(t *testing.T) (*Module, string) {
	t.Helper()
	stateDir := t.TempDir()
	m, err := NewModule(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m, stateDir
}

func moduleRequest(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "http://workbench.example"+path, strings.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func testStore(t *testing.T) *Store {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.DB.Close() })
	return store
}

func sshFixture(t *testing.T, home string, environment ...map[string]string) (string, int, string) {
	return sshFixtureWithExecGate(t, home, nil, environment...)
}

func sshFixtureWithExecGate(t *testing.T, home string, gate <-chan struct{}, environment ...map[string]string) (string, int, string) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	config := &ssh.ServerConfig{PasswordCallback: func(connection ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
		if connection.User() != "dev" || string(password) != "fixture-secret" {
			return nil, fmt.Errorf("invalid credentials")
		}
		return nil, nil
	}}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer connection.Close()
				server, channels, requests, err := ssh.NewServerConn(connection, config)
				if err != nil {
					return
				}
				defer server.Close()
				go ssh.DiscardRequests(requests)
				for incoming := range channels {
					if incoming.ChannelType() != "session" {
						_ = incoming.Reject(ssh.UnknownChannelType, "unsupported")
						continue
					}
					channel, requests, err := incoming.Accept()
					if err != nil {
						continue
					}
					go func() {
						defer channel.Close()
						for request := range requests {
							switch request.Type {
							case "subsystem":
								var payload struct{ Name string }
								ssh.Unmarshal(request.Payload, &payload)
								if payload.Name != "sftp" {
									_ = request.Reply(false, nil)
									continue
								}
								_ = request.Reply(true, nil)
								server, err := sftp.NewServer(channel, sftp.ReadOnly(), sftp.WithServerWorkingDirectory(home))
								if err == nil {
									_ = server.Serve()
									_ = server.Close()
								}
								return
							case "exec":
								var payload struct{ Command string }
								ssh.Unmarshal(request.Payload, &payload)
								_ = request.Reply(true, nil)
								code := uint32(0)
								if strings.Contains(payload.Command, "$XDG_DATA_HOME") {
									if gate != nil {
										<-gate
									}
									values := map[string]string{"XDG_DATA_HOME": filepath.Join(home, ".local/share")}
									if len(environment) > 0 {
										for key, value := range environment[0] {
											values[key] = value
										}
									}
									for _, key := range []string{"XDG_DATA_HOME", "CODEX_HOME", "OMP_AGENT_DIR", "PI_CODING_AGENT_DIR", "PI_CONFIG_DIR", "CLAUDE_CONFIG_DIR"} {
										_, _ = io.WriteString(channel, values[key]+"\x00")
									}
								} else if payload.Command == "printf '%s\\n' \"$HOME\"; hostname 2>/dev/null || uname -n" {
									_, _ = io.WriteString(channel, home+"\nfixture-remote\n")
								} else {
									code = 1
								}
								_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{code}))
								return
							default:
								_ = request.Reply(false, nil)
							}
						}
					}()
				}
			}()
		}
	}()
	address := listener.Addr().(*net.TCPAddr)
	return address.IP.String(), address.Port, ssh.FingerprintSHA256(signer.PublicKey())
}

func trustSSHFixture(t *testing.T, manager *SSHManager, ctx context.Context, host *Host, pin string) {
	t.Helper()
	if err := manager.sync(ctx, host); err == nil || host.ObservedFingerprint != pin || host.Fingerprint != "" {
		t.Fatalf("fixture host key was not blocked for explicit confirmation: %+v %v", host, err)
	}
	host.Status, host.Error = "error", "huella SSH desconocida"
	if err := manager.Store.hostState(*host); err != nil {
		t.Fatal(err)
	}
	trusted, err := manager.TrustFingerprint(host.ID, host.Revision, pin)
	if err != nil {
		t.Fatal(err)
	}
	*host = trusted
}
