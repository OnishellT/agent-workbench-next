package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"workbench/internal/config"
)

const diagnosticsPayload = `{"id":"models","state":"ready","healthy":true,"pid":4242,"events":[{"time":"2026-01-01T00:00:00Z","event":"probe_ok"}]}`

func diagnosticsServer(t *testing.T, cfg config.Config, provider func(context.Context) json.RawMessage) http.Handler {
	t.Helper()
	modules := testModules(t, cfg, Modules{ModuleDiagnostics: provider})
	if provider != nil {
		modules.Models = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	}
	handler, err := New(cfg, testAssets(), modules)
	if err != nil {
		t.Fatal(err)
	}
	return handler.Handler()
}

func TestDiagnosticsRequiresSessionAndStaysExplicitWithoutSource(t *testing.T) {
	cfg := testConfig(t)
	t.Setenv("WORKBENCH_DIAGNOSTICS_CANARY", "CANARY-child-environment-must-not-be-published")
	handler := diagnosticsServer(t, cfg, nil)

	request := newRequest(http.MethodGet, cfg.PublicURL+"/api/diagnostics", mustURLHost(t, cfg.PublicURL), nil, cfg.PublicURL)
	response := serve(handler, request)
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), "authentication_required") {
		t.Fatalf("anonymous diagnostics = %d %q, want 401 JSON", response.Code, response.Body.String())
	}

	request = newRequest(http.MethodGet, cfg.PublicURL+"/api/diagnostics", mustURLHost(t, cfg.PublicURL), nil, cfg.PublicURL)
	addCookie(request, authorize(t, handler, cfg))
	response = serve(handler, request)
	if response.Code != http.StatusNotImplemented || !strings.Contains(response.Body.String(), "module_diagnostics_not_implemented") {
		t.Fatalf("diagnostics without a source = %d %q, want an explicit 501", response.Code, response.Body.String())
	}
}

func TestDiagnosticsReportsModuleGapsAndNoSecrets(t *testing.T) {
	cfg := testConfig(t)
	t.Setenv("WORKBENCH_DIAGNOSTICS_CANARY", "CANARY-child-environment-must-not-be-published")
	handler := diagnosticsServer(t, cfg, func(context.Context) json.RawMessage {
		return json.RawMessage(diagnosticsPayload)
	})
	cookie := authorize(t, handler, cfg)

	request := newRequest(http.MethodGet, cfg.PublicURL+"/api/diagnostics", mustURLHost(t, cfg.PublicURL), nil, cfg.PublicURL)
	addCookie(request, cookie)
	response := serve(handler, request)
	if response.Code != http.StatusOK {
		t.Fatalf("diagnostics = %d %q, want 200", response.Code, response.Body.String())
	}
	body := response.Body.String()
	var decoded struct {
		Status          string                     `json:"status"`
		Modules         map[string]string          `json:"modules"`
		RoutesNotPorted []string                   `json:"routes_not_ported"`
		NativeModule    map[string]json.RawMessage `json:"native_module"`
	}
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("diagnostics JSON is invalid: %v (%s)", err, body)
	}
	if decoded.Status != "partial" {
		t.Fatalf("wired diagnostics status = %q, want the honest partial state", decoded.Status)
	}
	if decoded.Modules["module_diagnostics"] != "mounted" {
		t.Fatalf("module_diagnostics state = %q, want mounted", decoded.Modules["module_diagnostics"])
	}
	for _, gap := range decoded.RoutesNotPorted {
		if gap == "module_diagnostics" {
			t.Fatalf("a wired diagnostics module is still reported as a gap: %v", decoded.RoutesNotPorted)
		}
	}
	if string(decoded.NativeModule["pid"]) != "4242" || string(decoded.NativeModule["healthy"]) != "true" {
		t.Fatalf("managed module payload was rewritten: %s", body)
	}
	for _, canary := range []string{cfg.AuthToken, cookie.Value, "CANARY-child-environment-must-not-be-published"} {
		if strings.Contains(body, canary) {
			t.Fatalf("diagnostics published a secret: %s", body)
		}
	}

	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		request := newRequest(method, cfg.PublicURL+"/api/diagnostics", mustURLHost(t, cfg.PublicURL), strings.NewReader("body"), cfg.PublicURL)
		addCookie(request, cookie)
		if response := serve(handler, request); response.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s diagnostics = %d %q, want 405", method, response.Code, response.Body.String())
		}
	}
}
