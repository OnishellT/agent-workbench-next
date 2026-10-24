package models

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Module.Handler builds the gateway proxy once; repeated requests must keep
// reaching the credential gate for every gateway prefix, and nothing else.
func TestModuleHandlerRoutesGatewayPrefixesThroughCredentialGate(t *testing.T) {
	m := &Module{native: &nativeProcess{webURL: "http://127.0.0.1:1", webHost: "127.0.0.1:1", gateway: "http://127.0.0.1:2", client: http.DefaultClient}}
	h := m.Handler()
	for round := 0; round < 2; round++ {
		for _, path := range []string{"/v1/models", "/v1beta/models", "/backend-api/codex/responses"} {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://workbench.test"+path, nil))
			if w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Fatalf("round %d %s: status %d, want 401 from the credential gate", round, path, w.Code)
			}
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://workbench.test/other", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("unrelated path: %d, want 404", w.Code)
	}
}
