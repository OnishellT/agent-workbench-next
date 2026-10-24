package app

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"workbench/internal/settings"
)

func TestNewRequiresCoordinatorSettingsStore(t *testing.T) {
	cfg := testConfig(t)
	if _, err := New(cfg, testAssets(), Modules{}); err == nil || !strings.Contains(err.Error(), "shared settings store") {
		t.Fatalf("New without shared store error = %v, want clear shared-store failure", err)
	}
}

func TestRetiredSurfacesNeverMount(t *testing.T) {
	cfg := testConfig(t)
	mounted := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("retired handler called") })
	server, err := New(cfg, testAssets(), testModules(t, cfg, Modules{Foundry: mounted, Hosts: mounted}))
	if err != nil {
		t.Fatal(err)
	}
	cookie := authorize(t, server.Handler(), cfg)
	for _, path := range []string{"/api/work", "/api/work/runtime", "/api/terminal", "/api/terminal/sessions", "/api/usage/overview"} {
		req := newRequest(http.MethodGet, cfg.PublicURL+path, mustURLHost(t, cfg.PublicURL), nil, cfg.PublicURL)
		addCookie(req, cookie)
		if got := serve(server.Handler(), req); got.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", path, got.Code)
		}
	}
	for _, name := range []string{"work", "terminal"} {
		if _, exists := server.moduleStates()[name]; exists {
			t.Errorf("retired module %s reported", name)
		}
		for _, pending := range server.routesNotPorted() {
			if pending == name {
				t.Errorf("retired module %s pending", name)
			}
		}
	}
}

func TestSettingsStoreSharedWithFoundrySnapshots(t *testing.T) {
	cfg := testConfig(t)
	store, err := settings.Open(cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	work := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		projects := store.Snapshot().Projects
		if len(projects) == 0 {
			_, _ = fmt.Fprint(w, "empty")
			return
		}
		_, _ = fmt.Fprint(w, projects[0].Name)
	})
	server, err := New(cfg, testAssets(), Modules{Settings: store, Foundry: work})
	if err != nil {
		t.Fatal(err)
	}
	if server.settings != store {
		t.Fatal("application constructed a different Settings store")
	}
	cookie := authorize(t, server.Handler(), cfg)
	projectDir := t.TempDir()
	body := fmt.Sprintf("{\"projects\":[{\"path\":%q,\"name\":\"shared-now\"}]}", projectDir)
	if response := settingsRequest(t, server.Handler(), cfg, cookie, http.MethodPost, body); response.Code != http.StatusOK {
		t.Fatalf("POST settings = %d %s, want 200", response.Code, response.Body.String())
	}
	request := newRequest(http.MethodGet, cfg.PublicURL+"/api/conversations", mustURLHost(t, cfg.PublicURL), nil, cfg.PublicURL)
	addCookie(request, cookie)
	if response := serve(server.Handler(), request); response.Code != http.StatusOK || response.Body.String() != "shared-now" {
		t.Fatalf("Work project snapshot = %d %q, want 200 shared-now", response.Code, response.Body.String())
	}
}

func TestMountedWorkHostsRequireSessionAndRejectForeignOrigin(t *testing.T) {
	cfg := testConfig(t)
	var calls atomic.Int32
	mounted := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
	server, err := New(cfg, testAssets(), testModules(t, cfg, Modules{Foundry: mounted, Hosts: mounted}))
	if err != nil {
		t.Fatal(err)
	}
	request := newRequest(http.MethodGet, cfg.PublicURL+"/api/conversations", mustURLHost(t, cfg.PublicURL), nil, cfg.PublicURL)
	if response := serve(server.Handler(), request); response.Code != http.StatusUnauthorized {
		t.Fatalf("Work without session = %d, want 401", response.Code)
	}
	cookie := authorize(t, server.Handler(), cfg)
	request = newRequest(http.MethodGet, cfg.PublicURL+"/api/conversations", mustURLHost(t, cfg.PublicURL), nil, "https://attacker.example")
	addCookie(request, cookie)
	if response := serve(server.Handler(), request); response.Code != http.StatusForbidden {
		t.Fatalf("Work with foreign Origin = %d, want 403", response.Code)
	}
	request = newRequest(http.MethodGet, cfg.PublicURL+"/api/hosts", mustURLHost(t, cfg.PublicURL), nil, "https://attacker.example")
	addCookie(request, cookie)
	if response := serve(server.Handler(), request); response.Code != http.StatusForbidden {
		t.Fatalf("Hosts with foreign Origin = %d, want 403", response.Code)
	}
	request = newRequest(http.MethodGet, cfg.PublicURL+"/api/hosts", "attacker.example", nil, cfg.PublicURL)
	addCookie(request, cookie)
	if response := serve(server.Handler(), request); response.Code != http.StatusForbidden {
		t.Fatalf("Hosts with foreign Host = %d, want 403", response.Code)
	}
	if calls.Load() != 0 {
		t.Fatalf("unauthorized mounted handlers called %d times, want 0", calls.Load())
	}
}

func TestFoundryMountRetainsFullPathsAndRootAuthority(t *testing.T) {
	cfg := testConfig(t)
	calls := 0
	wantPath := ""
	foundry := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != "body-preserved" || r.URL.Path != wantPath {
			t.Errorf("Foundry received path=%q body=%q error=%v", r.URL.Path, body, err)
		}
		w.WriteHeader(http.StatusAccepted)
	})
	server, err := New(cfg, testAssets(), testModules(t, cfg, Modules{Foundry: foundry}))
	if err != nil {
		t.Fatal(err)
	}
	request := newRequest(http.MethodPost, cfg.PublicURL+"/api/tasks", mustURLHost(t, cfg.PublicURL), strings.NewReader("body-preserved"), cfg.PublicURL)
	if response := serve(server.Handler(), request); response.Code != http.StatusUnauthorized || calls != 0 {
		t.Fatalf("unauthed Foundry: status=%d calls=%d", response.Code, calls)
	}
	cookie := authorize(t, server.Handler(), cfg)
	for _, path := range []string{"/api/conversations/sources", "/api/tasks", "/api/foundry/example"} {
		wantPath = path
		request = newRequest(http.MethodPost, cfg.PublicURL+path, mustURLHost(t, cfg.PublicURL), strings.NewReader("body-preserved"), cfg.PublicURL)
		addCookie(request, cookie)
		if response := serve(server.Handler(), request); response.Code != http.StatusAccepted {
			t.Fatalf("POST %s = %d %s", path, response.Code, response.Body.String())
		}
	}
	request = newRequest(http.MethodPost, cfg.PublicURL+"/api/tasks", mustURLHost(t, cfg.PublicURL), strings.NewReader("body-preserved"), "https://other.example")
	addCookie(request, cookie)
	if response := serve(server.Handler(), request); response.Code != http.StatusForbidden || calls != 3 {
		t.Fatalf("foreign-origin Foundry: status=%d calls=%d", response.Code, calls)
	}
	if server.moduleStates()["foundry"] != "mounted" {
		t.Fatal("Foundry mount missing from bootstrap")
	}
	for _, missing := range server.routesNotPorted() {
		if missing == "foundry" {
			t.Fatal("mounted Foundry still reported as missing")
		}
	}
}
