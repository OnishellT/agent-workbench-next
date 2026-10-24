package app

import (
	"net/http"
	"strings"
	"testing"
)

func TestPlainHTTPMutationsRejectForeignOriginButAllowNoOrigin(t *testing.T) {
	cfg := testConfig(t)
	handler, err := New(cfg, testAssets(), testModules(t, cfg, Modules{}))
	if err != nil {
		t.Fatal(err)
	}
	cookie := authorize(t, handler.Handler(), cfg)
	host := mustURLHost(t, cfg.PublicURL)
	request := newRequest(http.MethodPost, cfg.PublicURL+"/api/logout", host, nil, "http://evil.example")
	addCookie(request, cookie)
	if got := serve(handler.Handler(), request); got.Code != http.StatusForbidden {
		t.Fatalf("POST with foreign Origin = %d, want 403", got.Code)
	}
	// Non-browser clients send no Origin; SameSite=Strict covers browsers on plain HTTP.
	request = newRequest(http.MethodPost, cfg.PublicURL+"/api/logout", host, nil, "")
	addCookie(request, cookie)
	if got := serve(handler.Handler(), request); got.Code != http.StatusOK {
		t.Fatalf("POST without Origin = %d, want 200", got.Code)
	}
}

func TestAuthBacksOffAfterWrongTokens(t *testing.T) {
	cfg := testConfig(t)
	handler, err := New(cfg, testAssets(), testModules(t, cfg, Modules{}))
	if err != nil {
		t.Fatal(err)
	}
	attempt := func(token, remote string) *http.Request {
		r := newRequest(http.MethodPost, cfg.PublicURL+"/api/auth", mustURLHost(t, cfg.PublicURL), strings.NewReader(`{"token":"`+token+`"}`), cfg.PublicURL)
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = remote
		return r
	}
	if got := serve(handler.Handler(), attempt("wrong-token-wrong-token-wrong-token-1", "127.0.0.1:1")); got.Code != http.StatusUnauthorized {
		t.Fatalf("first wrong token = %d", got.Code)
	}
	blocked := serve(handler.Handler(), attempt(cfg.AuthToken, "127.0.0.1:2"))
	if blocked.Code != http.StatusTooManyRequests || blocked.Header().Get("Retry-After") == "" {
		t.Fatalf("immediate retry = %d %v, want 429 with Retry-After", blocked.Code, blocked.Header())
	}
	// Another client address is unaffected.
	if got := serve(handler.Handler(), attempt(cfg.AuthToken, "127.0.0.9:1")); got.Code != http.StatusOK {
		t.Fatalf("other client = %d, want 200", got.Code)
	}
}

func TestMethodNotAllowedListsTheRoutesMethods(t *testing.T) {
	cfg := testConfig(t)
	handler, err := New(cfg, testAssets(), testModules(t, cfg, Modules{}))
	if err != nil {
		t.Fatal(err)
	}
	cookie := authorize(t, handler.Handler(), cfg)
	for path, want := range map[string]string{"/api/bootstrap": "GET", "/api/diagnostics": "GET, HEAD", "/api/settings": "POST"} {
		method := http.MethodDelete
		request := newRequest(method, cfg.PublicURL+path, mustURLHost(t, cfg.PublicURL), nil, cfg.PublicURL)
		addCookie(request, cookie)
		got := serve(handler.Handler(), request)
		if got.Code != http.StatusMethodNotAllowed || got.Header().Get("Allow") != want {
			t.Errorf("%s: %d Allow=%q, want 405 %q", path, got.Code, got.Header().Get("Allow"), want)
		}
	}
}

func TestCleanPluginPath(t *testing.T) {
	const base = "/_magpie/plugin/0123456789abcdef0123456789abcdef/"
	for path, want := range map[string]bool{
		base: true, base + "page.html": true, base + "a/b.js": true,
		base + "../x": false, base + "./x": false, base + "a//b": false, base + "a/..": false,
	} {
		if cleanPluginPath(path) != want {
			t.Errorf("cleanPluginPath(%q) = %v, want %v", path, !want, want)
		}
	}
}
