package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func simStatusOf(t *testing.T, simURL string) map[string]bool {
	t.Helper()
	cfg := testConfig(t)
	cfg.SIMURL = simURL
	handler, err := New(cfg, testAssets(), testModules(t, cfg, Modules{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close(t.Context()) })
	cookie := authorize(t, handler.Handler(), cfg)
	request := newRequest(http.MethodGet, cfg.PublicURL+"/api/sim/status", mustURLHost(t, cfg.PublicURL), nil, "")
	addCookie(request, cookie)
	response := serve(handler.Handler(), request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body)
	}
	var got map[string]bool
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestSimStatusReportsReachabilityOfTheConfiguredSim(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusFound) // any answer proves a server is there
	}))
	if got := simStatusOf(t, up.URL); !got["configured"] || !got["reachable"] {
		t.Fatalf("running SIM = %v, want configured and reachable", got)
	}
	down := up.URL
	up.Close()
	if got := simStatusOf(t, down); !got["configured"] || got["reachable"] {
		t.Fatalf("stopped SIM = %v, want configured but not reachable", got)
	}
	if got := simStatusOf(t, ""); got["configured"] || got["reachable"] {
		t.Fatalf("no SIM = %v, want neither", got)
	}
}

func TestSimStatusNeedsASession(t *testing.T) {
	cfg := testConfig(t)
	handler, err := New(cfg, testAssets(), testModules(t, cfg, Modules{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close(t.Context()) })
	request := newRequest(http.MethodGet, cfg.PublicURL+"/api/sim/status", mustURLHost(t, cfg.PublicURL), nil, "")
	if got := serve(handler.Handler(), request); got.Code != http.StatusUnauthorized {
		t.Fatalf("without a session = %d, want 401", got.Code)
	}
}
