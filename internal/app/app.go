package app

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"workbench/internal/config"
	"workbench/internal/settings"
)

// foundryLoopPrefix is where the managed Foundry UI and API live (internal/foundryloop.Prefix).
const foundryLoopPrefix = "/foundry"

const maxSettingsBody = 1 << 20

type NativeSiteResolver func(context.Context, string) (NativeSite, error)

type Modules struct {
	Models             http.Handler
	AgentModels        http.Handler
	NativeSites        []NativeSite
	NativeSiteResolver NativeSiteResolver
	Foundry            http.Handler
	// FoundryLoop serves the managed Conversation Foundry under /foundry/. The session
	// check stays here; the handler authenticates to Foundry itself.
	FoundryLoop http.Handler
	Hosts       http.Handler
	Settings    *settings.Store
	// ModuleDiagnostics reports the managed module's real state and bounded log.
	// Without it the diagnostics module stays explicitly not_implemented.
	ModuleDiagnostics func(context.Context) json.RawMessage
}

// Server composes the browser-facing API and embedded assets. It does not own
// the lifecycle of module handlers supplied by the coordinator.
type Server struct {
	cfg                config.Config
	assets             fs.FS
	assetsOK           bool
	models             http.Handler
	agentModels        http.Handler
	foundry            http.Handler
	foundryLoop        http.Handler
	hosts              http.Handler
	settings           *settings.Store
	cookieName         string
	nativeSites        map[string]NativeSite
	nativeSitePending  map[string]chan struct{}
	nativeSiteResolver NativeSiteResolver
	moduleDiagnostics  func(context.Context) json.RawMessage

	mu              sync.Mutex
	settingsMu      sync.Mutex
	nativeSiteMu    sync.Mutex
	nativeSiteEpoch uint64
	nativeGrants    map[string]nativeSiteGrant
	nativeSessions  map[string]nativeSiteSession
	// sessions maps the random dashboard cookie value to its expiry. The master
	// token never travels in a cookie, so a leaked cookie can be revoked by logout
	// or restart without rotating access.token.
	sessionMu     sync.Mutex
	sessions      map[string]time.Time
	closing       bool
	nextRequestID uint64
	active        map[uint64]context.CancelFunc
	drained       chan struct{}
	drainedClosed bool

	authMu    sync.Mutex
	authFails map[netip.Addr]authFailure
}

// New creates the single-origin application handler. Missing and unported
// modules remain explicit in its status responses.
func New(cfg config.Config, assets fs.FS, modules Modules) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if modules.Settings == nil {
		return nil, errors.New("application requires the shared settings store")
	}
	nativeSites := make(map[string]NativeSite, len(modules.NativeSites))
	nativeOrigins := make(map[string]struct{}, len(modules.NativeSites))
	for _, site := range modules.NativeSites {
		if err := validateNativeSite(site); err != nil {
			return nil, err
		}
		if _, exists := nativeSites[site.HostID]; exists {
			return nil, errors.New("native Magpie site host IDs must be unique")
		}
		if _, exists := nativeOrigins[site.Origin]; exists {
			return nil, errors.New("native Magpie site origins must be unique")
		}
		nativeSites[site.HostID] = site
		nativeOrigins[site.Origin] = struct{}{}
	}
	cfg.TrustedProxies = append([]netip.Prefix(nil), cfg.TrustedProxies...)
	s := &Server{
		cfg:                cfg,
		assets:             assets,
		models:             modules.Models,
		agentModels:        modules.AgentModels,
		foundry:            modules.Foundry,
		foundryLoop:        modules.FoundryLoop,
		hosts:              modules.Hosts,
		settings:           modules.Settings,
		cookieName:         instanceCookieName(cfg.AuthToken),
		nativeSites:        nativeSites,
		nativeSitePending:  make(map[string]chan struct{}),
		nativeSiteResolver: modules.NativeSiteResolver,
		moduleDiagnostics:  modules.ModuleDiagnostics,
		nativeSiteEpoch:    1,
		nativeGrants:       make(map[string]nativeSiteGrant),
		nativeSessions:     make(map[string]nativeSiteSession),
		sessions:           make(map[string]time.Time),
		drained:            make(chan struct{}),
		active:             make(map[uint64]context.CancelFunc),
	}
	if assets != nil {
		info, err := fs.Stat(assets, "index.html")
		s.assetsOK = err == nil && info.Mode().IsRegular()
	}
	return s, nil
}

// Handler returns the application handler. It installs no global server
// timeouts, so streaming module handlers are not cut off by this layer.
func (s *Server) Handler() http.Handler { return http.HandlerFunc(s.serveHTTP) }

// Close revokes future requests and waits for admitted handlers to return. The
// entrypoint still owns and closes the module resources after a successful drain.
func (s *Server) Close(ctx context.Context) error {
	if ctx == nil {
		return errors.New("close requires a context")
	}
	s.mu.Lock()
	s.closing = true
	cancels := make([]context.CancelFunc, 0, len(s.active))
	for _, cancel := range s.active {
		cancels = append(cancels, cancel)
	}
	if len(s.active) == 0 {
		s.markDrainedLocked()
	}
	drained := s.drained
	s.mu.Unlock()
	s.revokeNativeSiteState()
	for _, cancel := range cancels {
		cancel()
	}
	select {
	case <-drained:
		return nil
	default:
	}
	select {
	case <-drained:
		return nil
	case <-ctx.Done():
		select {
		case <-drained:
			return nil
		default:
			return ctx.Err()
		}
	}
}

func (s *Server) enter(parent context.Context) (context.Context, uint64, bool) {
	ctx, cancel := context.WithCancel(parent)
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		cancel()
		return nil, 0, false
	}
	s.nextRequestID++
	id := s.nextRequestID
	s.active[id] = cancel
	s.mu.Unlock()
	return ctx, id, true
}

func (s *Server) leave(id uint64) {
	s.mu.Lock()
	cancel := s.active[id]
	delete(s.active, id)
	if s.closing && len(s.active) == 0 {
		s.markDrainedLocked()
	}
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *Server) markDrainedLocked() {
	if !s.drainedClosed {
		close(s.drained)
		s.drainedClosed = true
	}
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	securityHeaders(w)
	ctx, requestID, admitted := s.enter(r.Context())
	if !admitted {
		writeError(w, http.StatusServiceUnavailable, "server_closing")
		return
	}
	defer s.leave(requestID)
	r = r.WithContext(ctx)
	if hasPrefixPath(r.URL.Path, agentModelsPrefix) {
		http.StripPrefix(agentModelsPrefix, http.HandlerFunc(s.serveAgentModels)).ServeHTTP(w, r)
		return
	}
	if !s.authorizedAuthority(r) {
		writeError(w, http.StatusForbidden, "request_rejected")
		return
	}

	switch r.URL.Path {
	case "/health":
		s.health(w, r)
		return
	case "/api/auth":
		s.auth(w, r)
		return
	case "/api/logout":
		if !s.requireSession(w, r) {
			return
		}
		s.logout(w, r)
		return
	case "/api/bootstrap":
		if !s.requireSession(w, r) {
			return
		}
		s.bootstrap(w, r)
		return
	case "/api/sim/status":
		if !s.requireSession(w, r) {
			return
		}
		s.simStatus(w, r)
		return
	}

	if hasPrefixPath(r.URL.Path, foundryLoopPrefix) {
		if !s.requireSession(w, r) {
			return
		}
		if s.foundryLoop == nil {
			writeError(w, http.StatusNotImplemented, "foundry_loop_not_implemented")
			return
		}
		s.foundryLoop.ServeHTTP(w, r)
		return
	}

	if s.isAPISurface(r.URL.Path) {
		if !isInferenceNamespace(r.URL.Path) && !s.requireSession(w, r) {
			return
		}
		s.dispatchAPI(w, r)
		return
	}
	s.serveAssets(w, r)
}

func (s *Server) isAPISurface(pathname string) bool {
	return hasPrefixPath(pathname, "/api") ||
		hasPrefixPath(pathname, "/v1") ||
		hasPrefixPath(pathname, "/v1beta") ||
		hasPrefixPath(pathname, "/backend-api/codex")
}

func (s *Server) dispatchAPI(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/hosts/") && strings.HasSuffix(r.URL.Path, "/magpie/open") {
		s.openNativeMagpie(w, r)
		return
	}
	if r.URL.Path == "/api/settings" {
		s.updateSettings(w, r)
		return
	}
	if r.URL.Path == "/api/diagnostics" {
		s.diagnostics(w, r)
		return
	}
	if isInferenceNamespace(r.URL.Path) {
		if s.models == nil {
			writeError(w, http.StatusNotImplemented, "models_not_implemented")
			return
		}
		if !validInferenceRequest(r, s.cfg.AuthToken) {
			writeError(w, http.StatusUnauthorized, "inference_bearer_required")
			return
		}
		s.models.ServeHTTP(w, r)
		return
	}
	if hasPrefixPath(r.URL.Path, "/api/work") || hasPrefixPath(r.URL.Path, "/api/terminal") || r.URL.Path == "/api/usage/overview" {
		http.NotFound(w, r)
		return
	}
	if hasPrefixPath(r.URL.Path, "/api/conversations") || hasPrefixPath(r.URL.Path, "/api/tasks") || hasPrefixPath(r.URL.Path, "/api/foundry") {
		if s.foundry == nil {
			writeError(w, http.StatusNotImplemented, "foundry_not_implemented")
			return
		}
		s.foundry.ServeHTTP(w, r)
		return
	}
	if hasPrefixPath(r.URL.Path, "/api/hosts") || r.URL.Path == "/api/network/wireguard" {
		if s.hosts == nil {
			writeError(w, http.StatusNotImplemented, "hosts_not_implemented")
			return
		}
		s.hosts.ServeHTTP(w, r)
		return
	}

	if hasPrefixPath(r.URL.Path, "/api/models") {
		// Only the read-through endpoints exist; anything else would reach Magpie's
		// web surface with the private web key.
		if r.URL.Path != "/api/models/state" && r.URL.Path != "/api/models/groups" {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		if s.models == nil {
			writeError(w, http.StatusNotImplemented, "models_not_implemented")
			return
		}
		s.models.ServeHTTP(w, r)
		return
	}
	writeError(w, http.StatusNotFound, "not_found")
}
