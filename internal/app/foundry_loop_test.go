package app

import (
	"net/http"
	"testing"
)

func TestFoundryLoopRouteNeedsASessionAndMountedModule(t *testing.T) {
	cfg := testConfig(t)
	host := mustURLHost(t, cfg.PublicURL)
	var seen string
	mounted, err := New(cfg, testAssets(), testModules(t, cfg, Modules{FoundryLoop: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Path
		_, _ = w.Write([]byte("foundry"))
	})}))
	if err != nil {
		t.Fatal(err)
	}
	handler := mounted.Handler()

	// Without the Workbench session Foundry is unreachable, UI and API alike.
	for _, path := range []string{"/foundry/", "/foundry/api/state", "/foundry"} {
		if response := serve(handler, newRequest(http.MethodGet, cfg.PublicURL+path, host, nil, "")); response.Code != http.StatusUnauthorized || seen != "" {
			t.Fatalf("%s without session = %d (handler saw %q), want 401 before the handler", path, response.Code, seen)
		}
	}

	cookie := authorize(t, handler, cfg)
	request := newRequest(http.MethodGet, cfg.PublicURL+"/foundry/api/state", host, nil, "")
	addCookie(request, cookie)
	if response := serve(handler, request); response.Code != http.StatusOK || response.Body.String() != "foundry" || seen != "/foundry/api/state" {
		t.Fatalf("with session = %d %q (saw %q), want the Foundry handler", response.Code, response.Body.String(), seen)
	}
	// A foreign Origin is refused by the shared authority check, as for every other route.
	forged := newRequest(http.MethodPost, cfg.PublicURL+"/foundry/api/action", host, nil, "https://evil.example")
	addCookie(forged, cookie)
	if response := serve(handler, forged); response.Code != http.StatusForbidden {
		t.Fatalf("foreign origin = %d, want 403", response.Code)
	}
	// A path that merely starts with the prefix is the SPA, not Foundry.
	lookalike := newRequest(http.MethodGet, cfg.PublicURL+"/foundryx", host, nil, "")
	addCookie(lookalike, cookie)
	seen = ""
	if serve(handler, lookalike); seen != "" {
		t.Fatalf("/foundryx reached Foundry")
	}

	unmounted, err := New(cfg, testAssets(), testModules(t, cfg, Modules{}))
	if err != nil {
		t.Fatal(err)
	}
	cookie = authorize(t, unmounted.Handler(), cfg)
	request = newRequest(http.MethodGet, cfg.PublicURL+"/foundry/", host, nil, "")
	addCookie(request, cookie)
	if response := serve(unmounted.Handler(), request); response.Code != http.StatusNotImplemented {
		t.Fatalf("unmounted = %d, want 501 rather than the SPA", response.Code)
	}
}
