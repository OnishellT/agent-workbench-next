package app

import (
	"net/http"
	"testing"
)

func TestHealthRejectsForeignOriginEvenOnLoopbackAlias(t *testing.T) {
	cfg := testConfig(t)
	handler, err := New(cfg, testAssets(), testModules(t, cfg, Modules{}))
	if err != nil {
		t.Fatal(err)
	}
	request := newRequest(http.MethodGet, cfg.PublicURL+"/health", "127.0.0.1:18888", nil, "https://attacker.example")
	response := serve(handler.Handler(), request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("foreign health Origin accepted: %d %s", response.Code, response.Body.String())
	}
}
