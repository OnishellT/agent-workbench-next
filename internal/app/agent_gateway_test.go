package app

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestAgentModelsRetainsLocalPathAndBodyWithoutWeakeningPublicTLS(t *testing.T) {
	cfg := testConfig(t)
	cfg.PublicURL = "https://workbench.example"
	calls := 0
	module := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body, err := io.ReadAll(r.Body)
		if err != nil || r.URL.Path != "/v1/responses" || string(body) != "retained-body" {
			t.Errorf("expected native path and body; observed path=%q body=%q err=%v", r.URL.Path, body, err)
		}
		w.WriteHeader(http.StatusAccepted)
	})
	server, err := New(cfg, testAssets(), testModules(t, cfg, Modules{AgentModels: module}))
	if err != nil {
		t.Fatal(err)
	}
	request := newRequest(http.MethodPost, "http://"+cfg.Listen+agentModelsPrefix+"/v1/responses", cfg.Listen, strings.NewReader("retained-body"), "")
	request.Header.Set("Authorization", "Bearer internal-handler-test-key")
	if response := serve(server.Handler(), request); response.Code != http.StatusAccepted || calls != 1 {
		t.Fatalf("expected local dispatch 202/1; observed status=%d calls=%d body=%s", response.Code, calls, response.Body)
	}
	request = newRequest(http.MethodPost, "http://"+cfg.Listen+"/v1/responses", "workbench.example", strings.NewReader("retained-body"), "")
	request.Header.Set("Authorization", "Bearer internal-handler-test-key")
	if response := serve(server.Handler(), request); response.Code != http.StatusForbidden || calls != 1 {
		t.Fatalf("expected public plaintext to remain forbidden; observed status=%d calls=%d", response.Code, calls)
	}
}

func TestAgentModelsRejectsBrowserAndNonlocalAuthority(t *testing.T) {
	cfg := testConfig(t)
	cfg.PublicURL = "https://workbench.example"
	calls := 0
	server, err := New(cfg, testAssets(), testModules(t, cfg, Modules{AgentModels: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusAccepted)
	})}))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		mutate func(*http.Request)
		status int
	}{
		{"remote peer", func(r *http.Request) { r.RemoteAddr = "203.0.113.9:45678" }, http.StatusForbidden},
		{"public host", func(r *http.Request) { r.Host = "workbench.example" }, http.StatusForbidden},
		{"wrong port", func(r *http.Request) { r.Host = "127.0.0.1:1" }, http.StatusForbidden},
		{"duplicate Host", func(r *http.Request) { r.Header.Set("Host", cfg.Listen) }, http.StatusForbidden},
		{"Origin", func(r *http.Request) { r.Header.Set("Origin", cfg.PublicURL) }, http.StatusForbidden},
		{"empty Origin", func(r *http.Request) { r.Header.Set("Origin", "") }, http.StatusForbidden},
		{"Cookie", func(r *http.Request) { r.Header.Set("Cookie", "workbench_session=test") }, http.StatusForbidden},
		{"Forwarded", func(r *http.Request) { r.Header.Set("Forwarded", "for=127.0.0.1;proto=https") }, http.StatusForbidden},
		{"forwarded address", func(r *http.Request) { r.Header.Set("X-Forwarded-For", "127.0.0.1") }, http.StatusForbidden},
		{"forwarded proto", func(r *http.Request) { r.Header.Set("X-Forwarded-Proto", "https") }, http.StatusForbidden},
		{"missing bearer", func(r *http.Request) { r.Header.Del("Authorization") }, http.StatusUnauthorized},
		{"web token bearer", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+cfg.AuthToken) }, http.StatusUnauthorized},
		{"query key", func(r *http.Request) { r.URL.RawQuery = "api_key=not-a-gateway-key" }, http.StatusUnauthorized},
		{"upgrade", func(r *http.Request) { r.Header.Set("Upgrade", "websocket") }, http.StatusUnauthorized},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			request := newRequest(http.MethodPost, "http://"+cfg.Listen+agentModelsPrefix+"/v1/responses", cfg.Listen, strings.NewReader("{}"), "")
			request.Header.Set("Authorization", "Bearer internal-handler-test-key")
			item.mutate(request)
			response := serve(server.Handler(), request)
			if response.Code != item.status || calls != 0 {
				t.Fatalf("expected status=%d and no dispatch; observed status=%d calls=%d body=%s", item.status, response.Code, calls, response.Body)
			}
		})
	}
}
