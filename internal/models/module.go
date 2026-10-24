package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"workbench/internal/harness"
)

// Module supervises native Magpie and reads its agent state; Magpie remains the
// only owner of provider, model, and authentication configuration.
type Module struct {
	native    *nativeProcess
	routerErr error
	ctx       context.Context // cancelled by Close so router lookups stop with the module
	cancel    context.CancelFunc
}

var _ harness.Router = (*Module)(nil)

// New starts the native Magpie actor with agentHome as its HOME, so it reads the
// user's own agent and profile. Inside a container agentHome is the container
// user's home, not the Linux host user's: remote hosts stay reachable only over
// SSH through the host agent, never through a local fallback.
func New(stateDir, publicURL, listen, agentHome string) (*Module, error) {
	if strings.TrimSpace(stateDir) == "" || !filepath.IsAbs(stateDir) {
		return nil, errors.New("state directory is required")
	}
	if strings.TrimSpace(agentHome) == "" || !filepath.IsAbs(agentHome) {
		return nil, errors.New("absolute agent home is required")
	}
	_, routerErr := loopbackOriginForListen(listen)
	native, err := startNativeProcess(filepath.Clean(stateDir), filepath.Clean(agentHome), publicURL)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Module{native: native, routerErr: routerErr, ctx: ctx, cancel: cancel}, nil
}

// NewRemoteOnly returns a Module without a local native actor for environments
// (such as containers) where the process cannot present the Linux host user's
// HOME. Every served surface answers unavailable without a local fallback;
// agents on other hosts stay reachable over SSH through the host agent.
func NewRemoteOnly() *Module {
	return &Module{}
}

func loopbackOriginForListen(listen string) (string, error) {
	host, port, err := net.SplitHostPort(strings.TrimSpace(listen))
	if err != nil {
		return "", errors.New("Magpie routing requires a valid root listener host:port")
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return "", errors.New("Magpie routing requires the root listener to use a numeric nonzero port")
	}
	var address netip.Addr
	if host == "" {
		address = netip.MustParseAddr("127.0.0.1")
	} else {
		address, err = netip.ParseAddr(host)
		if err != nil || address.Zone() != "" {
			return "", errors.New("Magpie routing requires the root listener to use a literal IP address")
		}
		if address.IsUnspecified() {
			if address.Is4() {
				address = netip.MustParseAddr("127.0.0.1")
			} else {
				address = netip.MustParseAddr("::1")
			}
		} else if !address.IsLoopback() {
			return "", errors.New("Magpie routing is disabled because the root listener is bound only to a non-loopback address")
		}
	}
	return "http://" + net.JoinHostPort(address.String(), strconv.Itoa(number)), nil
}

type nativeAgentState struct {
	Agents []struct {
		ID    string `json:"id"`
		Wired bool   `json:"wired"`
		Drift *struct {
			Kind string `json:"kind"`
		} `json:"drift,omitempty"`
		Fields []struct {
			Key     string            `json:"key"`
			Value   json.RawMessage   `json:"value"`
			Options []json.RawMessage `json:"options"`
		} `json:"fields"`
	} `json:"agents"`
}

func (m *Module) nativeState() (nativeAgentState, error) {
	var state nativeAgentState
	if m == nil || m.native == nil {
		return state, errors.New("native Magpie is unavailable")
	}
	base := m.ctx
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithTimeout(base, 8*time.Second)
	defer cancel()
	data, err := m.native.NativeJSON(ctx, "/api/state")
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, errors.New("native Magpie agent state is invalid")
	}
	return state, nil
}

func (m *Module) AgentEnv(engine, profile string) ([]string, error) {
	if err := m.Probe(engine, profile); err != nil {
		return nil, err
	}
	return nil, nil
}

func (m *Module) CodexArgs(model string) ([]string, error) {
	if m == nil {
		return nil, errors.New("models module is unavailable")
	}
	if m.routerErr != nil {
		return nil, m.routerErr
	}
	state, err := m.nativeState()
	if err != nil {
		return nil, err
	}
	for _, agent := range state.Agents {
		if agent.ID != "codex" {
			continue
		}
		if !agent.Wired {
			return nil, errors.New("native Magpie Codex agent is not connected")
		}
		if agent.Drift != nil {
			return nil, fmt.Errorf("native Magpie Codex configuration has %q drift; reapply it in Magpie", agent.Drift.Kind)
		}
		if model == "" {
			return nil, nil
		}
		for _, field := range agent.Fields {
			if field.Key != "model" {
				continue
			}
			if nativeString(field.Value) == model {
				return nil, nil
			}
			for _, raw := range field.Options {
				var value string
				if json.Unmarshal(raw, &value) == nil && value == model {
					return nil, nil
				}
				var option struct {
					Value string `json:"value"`
				}
				if json.Unmarshal(raw, &option) == nil && option.Value == model {
					return nil, nil
				}
			}
			return nil, fmt.Errorf("model %q is not available in native Magpie Codex state", model)
		}
		return nil, errors.New("native Magpie Codex model field is unavailable")
	}
	return nil, errors.New("native Magpie Codex agent is unavailable")
}

func nativeString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}

// Probe checks native Magpie state only; configuration changes remain in its UI.
func (m *Module) Probe(engine, profile string) error {
	if m == nil || m.native == nil {
		return errors.New("models module is unavailable")
	}
	if m.routerErr != nil {
		return m.routerErr
	}
	state, err := m.nativeState()
	if err != nil {
		return err
	}
	if engine == harness.EngineOMP && profile != "" && profile != harness.OMPProfileDefault {
		engine += "#" + profile
	}
	for _, agent := range state.Agents {
		if agent.ID == engine {
			if !agent.Wired {
				return fmt.Errorf("native Magpie agent %q is not connected", engine)
			}
			if agent.Drift != nil {
				return fmt.Errorf("native Magpie agent %q has %q configuration drift; reapply it in Magpie", engine, agent.Drift.Kind)
			}
			return nil
		}
	}
	return fmt.Errorf("native Magpie agent %q is unavailable", engine)
}

func (m *Module) NativeJSON(ctx context.Context, path string) (json.RawMessage, error) {
	if m == nil || m.native == nil {
		return nil, errors.New("native Magpie is unavailable")
	}
	return m.native.NativeJSON(ctx, path)
}

// Diagnostics reports the managed child's real state and bounded log; read-only.
func (m *Module) Diagnostics(ctx context.Context) json.RawMessage {
	var native *nativeProcess
	if m != nil {
		native = m.native
	}
	data, err := json.Marshal(native.diagnostics(ctx))
	if err != nil {
		return json.RawMessage(`{"id":"models","state":"unavailable","healthy":false,"detail":"diagnostics could not be encoded","events":[]}`)
	}
	return json.RawMessage(data)
}

func (m *Module) SiteAddr() string {
	if m == nil || m.native == nil {
		return ""
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(m.native.ports.Site))
}

func (m *Module) SiteOrigin() string {
	if m == nil || m.native == nil {
		return ""
	}
	return m.native.siteOrigin
}

func (m *Module) SiteHandler() http.Handler {
	if m == nil || m.native == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "native Magpie is unavailable", http.StatusServiceUnavailable)
		})
	}
	return m.native.SiteHandler()
}

func (m *Module) Handler() http.Handler {
	if m == nil || m.native == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "native Magpie is unavailable", http.StatusServiceUnavailable)
		})
	}
	gateway := m.native.GatewayHandler() // built once: the reverse proxy is stateless
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/models/state":
			m.nativeReadThrough(w, r, "/api/state")
			return
		case "/api/models/groups":
			m.nativeReadThrough(w, r, "/api/groups")
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/") || strings.HasPrefix(r.URL.Path, "/v1beta/") || strings.HasPrefix(r.URL.Path, "/backend-api/codex/") {
			gateway.ServeHTTP(w, r)
			return
		}
		// Nothing else belongs here: the web-key proxy is only for the dedicated site origin.
		http.NotFound(w, r)
	})
}

func (m *Module) nativeReadThrough(w http.ResponseWriter, r *http.Request, path string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	data, err := m.NativeJSON(r.Context(), path)
	if err != nil {
		http.Error(w, "native Magpie state unavailable", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (m *Module) AgentHandler() http.Handler {
	if m == nil || m.native == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "native Magpie agent handler is unavailable", http.StatusServiceUnavailable)
		})
	}
	return m.native.AgentGatewayHandler()
}

func (m *Module) Close(ctx context.Context) error {
	if m == nil || m.native == nil {
		return nil
	}
	if m.cancel != nil && m.native.cmd != nil { // an attached Magpie keeps running after Close
		m.cancel()
	}
	return m.native.Close(ctx)
}
