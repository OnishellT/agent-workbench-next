package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"workbench/internal/config"
	"workbench/internal/settings"
)

const testToken = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKL"

func testConfig(t *testing.T) config.Config {
	t.Helper()
	stateDir := t.TempDir()
	if err := os.Chmod(stateDir, 0700); err != nil {
		t.Fatal(err)
	}
	return config.Config{
		StateDir:  stateDir,
		Listen:    "127.0.0.1:18888",
		PublicURL: "http://127.0.0.1:18888",
		AuthToken: testToken,
	}
}

func testModules(t *testing.T, cfg config.Config, modules Modules) Modules {
	t.Helper()
	if modules.Settings == nil {
		store, err := settings.Open(cfg.StateDir)
		if err != nil {
			t.Fatalf("open test settings: %v", err)
		}
		modules.Settings = store
	}
	return modules
}

func testAssets() fstest.MapFS {
	return fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte("<!doctype html><title>Workbench</title>")},
		"assets/app.js": &fstest.MapFile{Data: []byte("console.log('app')")},
		"secret.txt":    &fstest.MapFile{Data: []byte("not public")},
	}
}

func newRequest(method, target, host string, body io.Reader, origin string) *http.Request {
	request := httptest.NewRequest(method, target, body)
	request.Host = host
	request.RemoteAddr = "127.0.0.1:45678"
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	return request
}

func authorize(t *testing.T, handler http.Handler, cfg config.Config) *http.Cookie {
	t.Helper()
	body := strings.NewReader(fmt.Sprintf(`{"token":%q}`, cfg.AuthToken))
	request := newRequest(http.MethodPost, cfg.PublicURL+"/api/auth", mustURLHost(t, cfg.PublicURL), body, cfg.PublicURL)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("auth status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("auth set %d cookies, want 1", len(cookies))
	}
	return cookies[0]
}

func mustURLHost(t *testing.T, raw string) string {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, raw, nil)
	return request.URL.Host
}

func addCookie(request *http.Request, cookie *http.Cookie) {
	request.AddCookie(cookie)
}

func serve(handler http.Handler, request *http.Request) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestAuthCookieIsInstanceScopedAndBootstrapDoesNotExposeToken(t *testing.T) {
	cfg := testConfig(t)
	handler, err := New(cfg, testAssets(), testModules(t, cfg, Modules{}))
	if err != nil {
		t.Fatal(err)
	}
	cookie := authorize(t, handler.Handler(), cfg)
	if cookie.Value == cfg.AuthToken || len(cookie.Value) < 32 || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Secure {
		t.Fatalf("cookie must carry a random session, not the token: %#v", cookie)
	}
	if !strings.HasPrefix(cookie.Name, "workbench_session_") {
		t.Fatalf("cookie is not instance-scoped: %q", cookie.Name)
	}

	otherCfg := cfg
	otherCfg.AuthToken = strings.Repeat("z", 48)
	other, err := New(otherCfg, testAssets(), testModules(t, otherCfg, Modules{}))
	if err != nil {
		t.Fatal(err)
	}
	if cookie.Name == other.cookieName {
		t.Fatal("different instances share a cookie name")
	}

	request := newRequest(http.MethodGet, cfg.PublicURL+"/api/bootstrap", mustURLHost(t, cfg.PublicURL), nil, "")
	addCookie(request, cookie)
	response := serve(handler.Handler(), request)
	if response.Code != http.StatusOK {
		t.Fatalf("bootstrap status = %d: %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), cfg.AuthToken) {
		t.Fatal("bootstrap exposed the access token")
	}
	var bootstrap struct {
		Status  string            `json:"status"`
		Modules map[string]string `json:"modules"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &bootstrap); err != nil {
		t.Fatal(err)
	}
	if bootstrap.Status != "degraded" || bootstrap.Modules["platform_hosts"] != "not_implemented" || bootstrap.Modules["models"] != "not_implemented" {
		t.Fatalf("bootstrap reports missing modules as healthy: %#v", bootstrap)
	}
	if _, exists := bootstrap.Modules["atlas"]; exists {
		t.Fatalf("retired Atlas tracker still appears in readiness: %#v", bootstrap.Modules)
	}

	health := serve(handler.Handler(), newRequest(http.MethodGet, cfg.PublicURL+"/health", mustURLHost(t, cfg.PublicURL), nil, ""))
	if health.Code != http.StatusServiceUnavailable || !strings.Contains(health.Body.String(), `"status":"degraded"`) {
		t.Fatalf("health = %d %s, want degraded 503", health.Code, health.Body.String())
	}
}

// The cookie carries a random session: the master token is not accepted as a
// cookie value, and logout revokes the session server-side.
func TestSessionCookieIsNotTheTokenAndLogoutRevokesIt(t *testing.T) {
	cfg := testConfig(t)
	handler, err := New(cfg, testAssets(), testModules(t, cfg, Modules{}))
	if err != nil {
		t.Fatal(err)
	}
	cookie := authorize(t, handler.Handler(), cfg)
	host := mustURLHost(t, cfg.PublicURL)
	fetchBootstrap := func(c *http.Cookie) int {
		request := newRequest(http.MethodGet, cfg.PublicURL+"/api/bootstrap", host, nil, "")
		addCookie(request, c)
		return serve(handler.Handler(), request).Code
	}
	if code := fetchBootstrap(&http.Cookie{Name: cookie.Name, Value: cfg.AuthToken}); code != http.StatusUnauthorized {
		t.Fatalf("token as cookie accepted: %d", code)
	}
	if code := fetchBootstrap(cookie); code != http.StatusOK {
		t.Fatalf("session cookie rejected: %d", code)
	}
	logout := newRequest(http.MethodPost, cfg.PublicURL+"/api/logout", host, nil, cfg.PublicURL)
	addCookie(logout, cookie)
	if code := serve(handler.Handler(), logout).Code; code != http.StatusOK {
		t.Fatalf("logout status = %d", code)
	}
	if code := fetchBootstrap(cookie); code != http.StatusUnauthorized {
		t.Fatalf("session still valid after logout: %d", code)
	}
}

// The shell learns the SIM origin from bootstrap only; an unconfigured SIM
// stays empty instead of pointing at the Workbench origin itself.
func TestBootstrapExposesOnlyTheConfiguredSIMOrigin(t *testing.T) {
	cfg := testConfig(t)
	handler, err := New(cfg, testAssets(), testModules(t, cfg, Modules{}))
	if err != nil {
		t.Fatal(err)
	}
	type bootstrapView struct {
		SIMURL   string            `json:"sim_url"`
		Modules  map[string]string `json:"modules"`
		Settings map[string]any    `json:"settings"`
	}
	bootstrap := func(server *Server) bootstrapView {
		t.Helper()
		request := newRequest(http.MethodGet, cfg.PublicURL+"/api/bootstrap", mustURLHost(t, cfg.PublicURL), nil, "")
		addCookie(request, authorize(t, server.Handler(), cfg))
		response := serve(server.Handler(), request)
		if response.Code != http.StatusOK {
			t.Fatalf("bootstrap status = %d: %s", response.Code, response.Body.String())
		}
		var decoded bootstrapView
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		return decoded
	}

	unconfigured := bootstrap(handler)
	if unconfigured.SIMURL != "" {
		t.Fatalf("unconfigured bootstrap sim_url = %q, want empty", unconfigured.SIMURL)
	}
	for _, retired := range []string{"orbit", "orbit_worker"} {
		if _, exists := unconfigured.Modules[retired]; exists {
			t.Errorf("retired Orbit module %q still appears in readiness: %#v", retired, unconfigured.Modules)
		}
	}
	for _, retired := range []string{"orbit_local_worker", "orbit_image"} {
		if _, exists := unconfigured.Settings[retired]; exists {
			t.Errorf("retired Orbit setting %q still appears in bootstrap: %#v", retired, unconfigured.Settings)
		}
	}

	cfg.SIMURL = "http://127.0.0.1:33500"
	configured, err := New(cfg, testAssets(), testModules(t, cfg, Modules{}))
	if err != nil {
		t.Fatal(err)
	}
	if got := bootstrap(configured).SIMURL; got != cfg.SIMURL {
		t.Fatalf("configured bootstrap sim_url = %q, want %q", got, cfg.SIMURL)
	}
}

func TestAuthRejectsBadTokenOriginAndHost(t *testing.T) {
	cfg := testConfig(t)
	handler, err := New(cfg, testAssets(), testModules(t, cfg, Modules{}))
	if err != nil {
		t.Fatal(err)
	}

	request := newRequest(http.MethodPost, cfg.PublicURL+"/api/auth", mustURLHost(t, cfg.PublicURL), strings.NewReader(`{"token":"wrong"}`), cfg.PublicURL)
	request.Header.Set("Content-Type", "application/json")
	response := serve(handler.Handler(), request)
	if response.Code != http.StatusUnauthorized || response.Header().Get("Set-Cookie") != "" {
		t.Fatalf("bad token response = %d, cookie=%q", response.Code, response.Header().Get("Set-Cookie"))
	}

	request = newRequest(http.MethodPost, cfg.PublicURL+"/api/auth", mustURLHost(t, cfg.PublicURL), strings.NewReader(fmt.Sprintf(`{"token":%q}`, cfg.AuthToken)), "https://evil.example")
	request.Header.Set("Content-Type", "application/json")
	if response = serve(handler.Handler(), request); response.Code != http.StatusForbidden {
		t.Fatalf("bad origin status = %d, want 403", response.Code)
	}

	request = newRequest(http.MethodPost, cfg.PublicURL+"/api/auth", "evil.example", strings.NewReader(fmt.Sprintf(`{"token":%q}`, cfg.AuthToken)), cfg.PublicURL)
	request.Header.Set("Content-Type", "application/json")
	if response = serve(handler.Handler(), request); response.Code != http.StatusForbidden {
		t.Fatalf("bad host status = %d, want 403", response.Code)
	}

	request = newRequest(http.MethodPost, cfg.PublicURL+"/api/auth", mustURLHost(t, cfg.PublicURL), strings.NewReader(fmt.Sprintf(`{"token":%q}`, cfg.AuthToken)), cfg.PublicURL)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Add("Origin", "https://evil.example")
	if response = serve(handler.Handler(), request); response.Code != http.StatusForbidden {
		t.Fatalf("duplicate origin status = %d, want 403", response.Code)
	}
}

func TestHTTPSRequiresExplicitProxyAndSetsSecureCookie(t *testing.T) {
	cfg := testConfig(t)
	cfg.PublicURL = "https://workbench.example"
	cfg.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}
	handler, err := New(cfg, testAssets(), testModules(t, cfg, Modules{}))
	if err != nil {
		t.Fatal(err)
	}

	request := newRequest(http.MethodPost, cfg.PublicURL+"/api/auth", "workbench.example", strings.NewReader(fmt.Sprintf(`{"token":%q}`, cfg.AuthToken)), cfg.PublicURL)
	request.TLS = nil
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Forwarded-Proto", "https")
	response := serve(handler.Handler(), request)
	if response.Code != http.StatusOK {
		t.Fatalf("trusted proxy auth = %d: %s", response.Code, response.Body.String())
	}
	cookie := response.Result().Cookies()[0]
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("public HTTPS cookie flags = %#v", cookie)
	}

	request = newRequest(http.MethodPost, cfg.PublicURL+"/api/auth", "workbench.example", strings.NewReader(fmt.Sprintf(`{"token":%q}`, cfg.AuthToken)), cfg.PublicURL)
	request.TLS = nil
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Forwarded-Proto", "https")
	request.RemoteAddr = "198.51.100.10:40000"
	if response = serve(handler.Handler(), request); response.Code != http.StatusForbidden {
		t.Fatalf("untrusted proxy status = %d, want 403", response.Code)
	}

	request = newRequest(http.MethodPost, cfg.PublicURL+"/api/auth", "workbench.example", strings.NewReader(fmt.Sprintf(`{"token":%q}`, cfg.AuthToken)), cfg.PublicURL)
	request.TLS = nil
	request.Header.Set("Content-Type", "application/json")
	if response = serve(handler.Handler(), request); response.Code != http.StatusForbidden {
		t.Fatalf("missing forwarded scheme status = %d, want 403", response.Code)
	}
}

func TestLogoutRequiresSessionAndExpiresOnlyThisInstanceCookie(t *testing.T) {
	cfg := testConfig(t)
	handler, err := New(cfg, testAssets(), testModules(t, cfg, Modules{}))
	if err != nil {
		t.Fatal(err)
	}
	request := newRequest(http.MethodPost, cfg.PublicURL+"/api/logout", mustURLHost(t, cfg.PublicURL), nil, cfg.PublicURL)
	if response := serve(handler.Handler(), request); response.Code != http.StatusUnauthorized {
		t.Fatalf("logout without cookie status = %d, want 401", response.Code)
	}
	cookie := authorize(t, handler.Handler(), cfg)
	request = newRequest(http.MethodPost, cfg.PublicURL+"/api/logout", mustURLHost(t, cfg.PublicURL), nil, cfg.PublicURL)
	addCookie(request, cookie)
	response := serve(handler.Handler(), request)
	if response.Code != http.StatusOK {
		t.Fatalf("logout status = %d: %s", response.Code, response.Body.String())
	}
	cleared := response.Result().Cookies()
	if len(cleared) != 1 || cleared[0].Name != cookie.Name || cleared[0].MaxAge >= 0 || !cleared[0].HttpOnly || cleared[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("logout cookie was not expired correctly: %#v", cleared)
	}
}

func TestRetiredAtlasRoutesAndModelsAPIOnly(t *testing.T) {
	cfg := testConfig(t)
	var modelCalls atomic.Int32
	models := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls.Add(1)
		_, _ = fmt.Fprint(w, "model-api:"+r.URL.Path)
	})
	handler, err := New(cfg, testAssets(), testModules(t, cfg, Modules{Models: models}))
	if err != nil {
		t.Fatal(err)
	}
	cookie := authorize(t, handler.Handler(), cfg)

	request := newRequest(http.MethodGet, cfg.PublicURL+"/api/atlas/api/summary", mustURLHost(t, cfg.PublicURL), nil, "")
	addCookie(request, cookie)
	if response := serve(handler.Handler(), request); response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "Workbench") {
		t.Fatalf("retired Atlas route = %d %q, want honest 404", response.Code, response.Body.String())
	}

	request = newRequest(http.MethodGet, cfg.PublicURL+"/api/models/state", mustURLHost(t, cfg.PublicURL), nil, "")
	addCookie(request, cookie)
	if response := serve(handler.Handler(), request); response.Code != http.StatusOK || response.Body.String() != "model-api:/api/models/state" {
		t.Fatalf("Models API = %d %q", response.Code, response.Body.String())
	}
	// Unknown model paths must not reach Magpie's web surface (which carries the web key).
	for _, p := range []string{"/api/models/catalog", "/api/models/state/x", "/api/models/"} {
		request = newRequest(http.MethodGet, cfg.PublicURL+p, mustURLHost(t, cfg.PublicURL), nil, "")
		addCookie(request, cookie)
		if response := serve(handler.Handler(), request); response.Code != http.StatusNotFound {
			t.Fatalf("%s = %d, want 404", p, response.Code)
		}
	}

	request = newRequest(http.MethodGet, cfg.PublicURL+"/magpie", mustURLHost(t, cfg.PublicURL), nil, "")
	response := serve(handler.Handler(), request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Workbench") || modelCalls.Load() != 1 {
		t.Fatalf("Models handler served non-API route: status=%d calls=%d body=%q", response.Code, modelCalls.Load(), response.Body.String())
	}
}

func TestMissingModulesAndUnknownAPIAreNeverHealthyOrSPA(t *testing.T) {
	cfg := testConfig(t)
	handler, err := New(cfg, testAssets(), testModules(t, cfg, Modules{}))
	if err != nil {
		t.Fatal(err)
	}
	cookie := authorize(t, handler.Handler(), cfg)
	request := newRequest(http.MethodGet, cfg.PublicURL+"/api/atlas/api/widget", mustURLHost(t, cfg.PublicURL), nil, "")
	addCookie(request, cookie)
	if response := serve(handler.Handler(), request); response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "Workbench") {
		t.Fatalf("retired Atlas = %d %q, want 404 JSON", response.Code, response.Body.String())
	}
	request = newRequest(http.MethodGet, cfg.PublicURL+"/api/not-implemented", mustURLHost(t, cfg.PublicURL), nil, "")
	addCookie(request, cookie)
	if response := serve(handler.Handler(), request); response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "Workbench") {
		t.Fatalf("unknown API fell back to SPA: %d %q", response.Code, response.Body.String())
	}
}

func TestStaticRejectsTraversalAndServesSafeDeepLinks(t *testing.T) {
	cfg := testConfig(t)
	handler, err := New(cfg, testAssets(), testModules(t, cfg, Modules{}))
	if err != nil {
		t.Fatal(err)
	}
	request := newRequest(http.MethodGet, cfg.PublicURL+"/settings", mustURLHost(t, cfg.PublicURL), nil, "")
	if response := serve(handler.Handler(), request); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Workbench") {
		t.Fatalf("deep link response = %d %q", response.Code, response.Body.String())
	}
	request = newRequest(http.MethodGet, cfg.PublicURL+"/assets/%2e%2e/secret.txt", mustURLHost(t, cfg.PublicURL), nil, "")
	request.URL.Path = "/assets/../secret.txt"
	request.URL.RawPath = "/assets/%2e%2e/secret.txt"
	response := serve(handler.Handler(), request)
	if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "not public") {
		t.Fatalf("traversal response = %d %q", response.Code, response.Body.String())
	}
}
