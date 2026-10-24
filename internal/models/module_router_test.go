package models

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func fakeNativeProcess(t *testing.T, state string) *nativeProcess {
	t.Helper()
	expectedCookie := ""
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != expectedCookie {
			t.Errorf("native API request did not carry the private web cookie")
		}
		if r.URL.Path == "/api/state" || r.URL.Path == "/api/groups" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(state))
			return
		}
		if r.URL.Path == "/api/window/quit" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(web.Close)
	u, _ := url.Parse(web.URL)
	port, _ := strconv.Atoi(u.Port())
	expectedCookie = "magpie_web_" + strconv.Itoa(port) + "=fixture-web-key"
	return &nativeProcess{webURL: web.URL, webHost: u.Host, webKey: "fixture-web-key", ports: nativePorts{Web: port}, client: web.Client(), gateway: "http://127.0.0.1:1"}
}

func TestRouterAdaptersUseNativeAgentStateWithoutGeneratingConfiguration(t *testing.T) {
	p := fakeNativeProcess(t, `{"agents":[{"id":"omp","wired":true},{"id":"codex","wired":true,"fields":[{"key":"model","value":"fixture/provider-model","options":["fixture/provider-model","fixture/other"]}]},{"id":"claude","wired":false}]}`)
	m := &Module{native: p}
	if env, err := m.AgentEnv("omp", "default"); err != nil || len(env) != 0 {
		t.Fatalf("native OMP environment = %v, %v; want no generated credentials", env, err)
	}
	if err := m.Probe("codex", ""); err != nil {
		t.Fatalf("native connected Codex probe: %v", err)
	}
	if err := m.Probe("claude", ""); err == nil {
		t.Fatal("unwired native Claude agent passed probe")
	}
	if err := m.Probe("not-native", ""); err == nil {
		t.Fatal("unknown agent passed native probe")
	}
	args, err := m.CodexArgs("fixture/other")
	if err != nil || len(args) != 0 {
		t.Fatalf("native Codex model args = %v, %v", args, err)
	}
	if _, err := m.CodexArgs("unlisted/model"); err == nil {
		t.Fatal("unlisted model accepted outside native Magpie catalog")
	}
}

func TestOMPProfileProbeUsesItsOwnNativeAgent(t *testing.T) {
	m := &Module{native: fakeNativeProcess(t, `{"agents":[{"id":"omp","wired":true},{"id":"omp#personal","wired":false},{"id":"omp#work","wired":true}]}`)}
	for _, test := range []struct {
		profile string
		ready   bool
	}{{"", true}, {"default", true}, {"personal", false}, {"work", true}, {"missing", false}} {
		if err := m.Probe("omp", test.profile); (err == nil) != test.ready {
			t.Errorf("profile %q probe = %v, want ready=%v", test.profile, err, test.ready)
		}
		if env, err := m.AgentEnv("omp", test.profile); (err == nil) != test.ready || len(env) != 0 {
			t.Errorf("profile %q env = %v, %v, want ready=%v without generated configuration", test.profile, env, err, test.ready)
		}
	}
}

func TestNativeConfigurationDriftBlocksExecution(t *testing.T) {
	m := &Module{native: fakeNativeProcess(t, `{"agents":[{"id":"omp#personal","wired":true,"drift":{"kind":"replaced"}},{"id":"codex","wired":true,"drift":{"kind":"unwired"}}]}`)}
	if err := m.Probe("omp", "personal"); err == nil || !strings.Contains(err.Error(), "omp#personal") {
		t.Fatalf("profile drift probe = %v, want the exact native agent rejected", err)
	}
	if _, err := m.CodexArgs(""); err == nil {
		t.Fatal("Codex configuration drift allowed execution")
	}
}

func TestNativeCatalogReadThroughEndpointsAreReadOnlyAndRaw(t *testing.T) {
	state := `{"agents":[{"id":"omp","wired":true}]}`
	groups := `{"groups":[{"id":"fixture","name":"Fixture"}]}`
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/state" {
			_, _ = w.Write([]byte(state))
		} else if r.URL.Path == "/api/groups" {
			_, _ = w.Write([]byte(groups))
		} else {
			http.NotFound(w, r)
		}
	}))
	defer web.Close()
	u, _ := url.Parse(web.URL)
	port, _ := strconv.Atoi(u.Port())
	m := &Module{native: &nativeProcess{webURL: web.URL, webHost: u.Host, ports: nativePorts{Web: port}, webKey: "fixture-web-key", client: web.Client()}}
	for path, want := range map[string]string{"/api/models/state": state, "/api/models/groups": groups} {
		r := httptest.NewRequest(http.MethodGet, "http://workbench.test"+path, nil)
		w := httptest.NewRecorder()
		m.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusOK || w.Body.String() != want {
			t.Fatalf("GET %s: status=%d body=%s", path, w.Code, w.Body)
		}
	}
	r := httptest.NewRequest(http.MethodPost, "http://workbench.test/api/models/state", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("POST state status=%d Allow=%q", w.Code, w.Header().Get("Allow"))
	}
}

func TestNativeNoContentActionIsNotAValidJSONRead(t *testing.T) {
	p := fakeNativeProcess(t, `{}`)
	if data, err := p.nativeJSON(context.Background(), http.MethodPost, "/api/window/quit", []byte("{}")); err != nil || len(data) != 0 {
		t.Fatalf("native quit = %s, %v, want a successful no-content action", data, err)
	}
	if _, err := p.NativeJSON(context.Background(), "/api/window/quit"); err == nil {
		t.Fatal("an empty native response was accepted as a JSON read")
	}
}

func TestLoopbackOriginForListen(t *testing.T) {
	for _, test := range []struct {
		listen string
		want   string
		ok     bool
	}{
		{"127.0.0.1:8080", "http://127.0.0.1:8080", true},
		{"[::1]:8080", "http://[::1]:8080", true},
		{"0.0.0.0:8080", "http://127.0.0.1:8080", true},
		{"[::]:8080", "http://[::1]:8080", true},
		{"example.test:8080", "", false},
		{"127.0.0.1:0", "", false},
	} {
		got, err := loopbackOriginForListen(test.listen)
		if (err == nil) != test.ok || got != test.want {
			t.Errorf("loopbackOriginForListen(%q) = %q, %v", test.listen, got, err)
		}
	}
}
