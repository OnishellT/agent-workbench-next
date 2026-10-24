package hosts

import (
	"net/http"
	"strings"
	"testing"
)

func TestHostAgentLifecycleRoutesAreMountedAndControlTailBlocked(t *testing.T) {
	m, _ := newTestModule(t)
	defer m.Close()
	for _, request := range []struct {
		method, path, body string
		status             int
		message            string
	}{
		{http.MethodGet, "/api/hosts/test/magpie/status", "", http.StatusNotFound, "host no encontrado"},
		{http.MethodPost, "/api/hosts/test/magpie/install", `{}`, http.StatusBadRequest, "revisión del host obligatoria"},
		{http.MethodPost, "/api/hosts/test/magpie/api/work/sessions", `{}`, http.StatusNotImplemented, "helper Go"},
	} {
		w := moduleRequest(t, m.Handler(), request.method, request.path, request.body)
		if w.Code != request.status || !strings.Contains(w.Body.String(), request.message) || strings.Contains(w.Body.String(), `"installed":true`) {
			t.Fatalf("%s %s route response = %d %s; want %d containing %q and no installed claim", request.method, request.path, w.Code, w.Body.String(), request.status, request.message)
		}
	}
}

// Work and its terminal are retired: only the models surface is proxied per host,
// so retired routes stay unregistered and answer the mux 404 without a fallback.
func TestRetiredWorkTerminalSurfacesAreNotProxied(t *testing.T) {
	m, _ := newTestModule(t)
	defer m.Close()
	for _, path := range []string{
		"/api/hosts/some-host/work",
		"/api/hosts/some-host/work/status",
		"/api/hosts/some-host/terminal",
		"/api/hosts/some-host/terminal/stream",
	} {
		w := moduleRequest(t, m.Handler(), http.MethodGet, path, "")
		if w.Code != http.StatusNotFound {
			t.Fatalf("GET %s = %d %s, want 404 for the retired surface", path, w.Code, w.Body.String())
		}
	}
}
