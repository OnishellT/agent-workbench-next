package app

import (
	"net/http"
	"strings"
	"testing"
)

func TestAuthRejectsDuplicateHostHeader(t *testing.T) {
	cfg := testConfig(t)
	handler, err := New(cfg, testAssets(), testModules(t, cfg, Modules{}))
	if err != nil {
		t.Fatal(err)
	}
	request := newRequest(http.MethodPost, cfg.PublicURL+"/api/auth", "127.0.0.1:18888", strings.NewReader("{\"token\":\""+cfg.AuthToken+"\"}"), cfg.PublicURL)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Add("Host", "attacker.example")
	response := serve(handler.Handler(), request)
	if response.Code != http.StatusForbidden || response.Header().Get("Set-Cookie") != "" {
		t.Fatalf("duplicate Host accepted: status=%d cookie=%q", response.Code, response.Header().Get("Set-Cookie"))
	}
}
