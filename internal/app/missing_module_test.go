package app

import (
	"net/http"
	"strings"
	"testing"
)

func TestMissingModelsRouteIsNotImplemented(t *testing.T) {
	cfg := testConfig(t)
	handler, err := New(cfg, testAssets(), testModules(t, cfg, Modules{}))
	if err != nil {
		t.Fatal(err)
	}
	request := newRequest(http.MethodGet, cfg.PublicURL+"/v1/models", "127.0.0.1:18888", nil, "")
	response := serve(handler.Handler(), request)
	if response.Code != http.StatusNotImplemented || !strings.Contains(response.Body.String(), "models_not_implemented") {
		t.Fatalf("missing Models route = %d %q", response.Code, response.Body.String())
	}
}

// Orbit is retired with SIM as its replacement: the old API surface must be a
// plain JSON 404 behind the normal session check, not a 501 placeholder.
func TestRetiredOrbitRoutesAreNotFoundJSON(t *testing.T) {
	cfg := testConfig(t)
	handler, err := New(cfg, testAssets(), testModules(t, cfg, Modules{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/orbit", "/api/orbit/runs", "/api/orbit/diagnostics"} {
		request := newRequest(http.MethodGet, cfg.PublicURL+path, mustURLHost(t, cfg.PublicURL), nil, cfg.PublicURL)
		response := serve(handler.Handler(), request)
		if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), "authentication_required") {
			t.Fatalf("anonymous %s = %d %q, want 401 JSON", path, response.Code, response.Body.String())
		}
		request = newRequest(http.MethodGet, cfg.PublicURL+path, mustURLHost(t, cfg.PublicURL), nil, cfg.PublicURL)
		addCookie(request, authorize(t, handler.Handler(), cfg))
		response = serve(handler.Handler(), request)
		if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), "not_found") || !strings.Contains(response.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("authenticated %s = %d %q (%s), want JSON 404", path, response.Code, response.Body.String(), response.Header().Get("Content-Type"))
		}
	}
}
