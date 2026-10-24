package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"workbench/internal/config"
	"workbench/internal/settings"
)

func settingsJSON(t *testing.T, value any) string {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func settingsRequest(t *testing.T, handler http.Handler, cfg config.Config, cookie *http.Cookie, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := newRequest(method, cfg.PublicURL+"/api/settings", mustURLHost(t, cfg.PublicURL), strings.NewReader(body), cfg.PublicURL)
	addCookie(req, cookie)
	req.Header.Set("Content-Type", "application/json")
	return serve(handler, req)
}

func TestLegacyCaptureSettingDoesNotReactivateOrBlockUpgrade(t *testing.T) {
	cfg := testConfig(t)
	store, err := settings.Open(cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	values := store.Snapshot()
	values.Capture = true
	if err := store.Save(values); err != nil {
		t.Fatal(err)
	}
	server, err := New(cfg, testAssets(), testModules(t, cfg, Modules{}))
	if err != nil {
		t.Fatalf("legacy capture=true blocked startup: %v", err)
	}
	cookie := authorize(t, server.Handler(), cfg)
	request := newRequest(http.MethodGet, cfg.PublicURL+"/api/bootstrap", mustURLHost(t, cfg.PublicURL), nil, "")
	addCookie(request, cookie)
	response := serve(server.Handler(), request)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "\"capture\"") {
		t.Fatalf("bootstrap exposed a retired capture setting: %d %s", response.Code, response.Body.String())
	}
	project := settings.Project{Path: t.TempDir(), Name: "Safe update"}
	response = settingsRequest(t, server.Handler(), cfg, cookie, http.MethodPost, settingsJSON(t, map[string]any{"projects": []settings.Project{project}}))
	if response.Code != http.StatusOK {
		t.Fatalf("unrelated settings update = %d %s", response.Code, response.Body.String())
	}
	if !server.settings.Snapshot().Capture {
		t.Fatal("unrelated update overwrote preserved legacy setting")
	}
	reopened, err := settings.Open(cfg.StateDir)
	if err != nil || !reopened.Snapshot().Capture {
		t.Fatalf("legacy setting did not survive update/reopen: %v", err)
	}
	response = settingsRequest(t, server.Handler(), cfg, cookie, http.MethodPost, settingsJSON(t, map[string]bool{"capture": false}))
	if response.Code != http.StatusBadRequest || !server.settings.Snapshot().Capture {
		t.Fatalf("retired capture update should be rejected without mutation: %d %s", response.Code, response.Body.String())
	}
}

func TestSettingsRejectsInvalidAndUnimplementedUpdatesWithoutPersistence(t *testing.T) {
	cfg := testConfig(t)
	server, err := New(cfg, testAssets(), testModules(t, cfg, Modules{}))
	if err != nil {
		t.Fatal(err)
	}
	cookie := authorize(t, server.Handler(), cfg)
	cases := []struct {
		body   string
		status int
	}{
		{settingsJSON(t, map[string]any{"capture": true}), http.StatusBadRequest},
		{settingsJSON(t, map[string]any{"unknown": true}), http.StatusBadRequest},
		{settingsJSON(t, map[string]any{"projects": []map[string]any{{"path": "relative", "name": "bad"}}}), http.StatusBadRequest},
		{settingsJSON(t, map[string]any{"orbit_local_worker": true}), http.StatusBadRequest},
		{settingsJSON(t, map[string]any{"orbit_image": "image:latest"}), http.StatusBadRequest},
	}
	for _, test := range cases {
		response := settingsRequest(t, server.Handler(), cfg, cookie, http.MethodPost, test.body)
		if response.Code != test.status {
			t.Errorf("POST %s = %d %s, want %d", test.body, response.Code, response.Body.String(), test.status)
		}
	}
	tooMany, err := json.Marshal(map[string]any{"projects": make([]settings.Project, 101)})
	if err != nil {
		t.Fatal(err)
	}
	if response := settingsRequest(t, server.Handler(), cfg, cookie, http.MethodPost, string(tooMany)); response.Code != http.StatusBadRequest {
		t.Fatalf("101 projects = %d, want 400: %s", response.Code, response.Body.String())
	}
	values := server.settings.Snapshot()
	if values.Capture || len(values.Projects) != 0 {
		t.Fatalf("rejected update changed settings: %#v", values)
	}
}

// An existing settings file keeps its projects after Orbit is retired: the two
// known Orbit keys are read and dropped instead of blocking startup or resetting.
func TestLegacyOrbitSettingsKeepProjects(t *testing.T) {
	cfg := testConfig(t)
	project := t.TempDir()
	legacy := fmt.Sprintf(`{"capture":false,"projects":[{"path":%q,"name":"Kept"}],"orbit_local_worker":true,"orbit_image":"image:latest"}`, project)
	if err := os.WriteFile(filepath.Join(cfg.StateDir, "settings.json"), []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	server, err := New(cfg, testAssets(), testModules(t, cfg, Modules{}))
	if err != nil {
		t.Fatalf("legacy Orbit settings blocked startup: %v", err)
	}
	if got := server.settings.Snapshot().Projects; len(got) != 1 || got[0].Path != project || got[0].Name != "Kept" {
		t.Fatalf("legacy Orbit settings lost projects: %#v", got)
	}
	cookie := authorize(t, server.Handler(), cfg)
	request := newRequest(http.MethodGet, cfg.PublicURL+"/api/bootstrap", mustURLHost(t, cfg.PublicURL), nil, "")
	addCookie(request, cookie)
	response := serve(server.Handler(), request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "\"Kept\"") {
		t.Fatalf("bootstrap lost the preserved project: %d %s", response.Code, response.Body.String())
	}
	response = settingsRequest(t, server.Handler(), cfg, cookie, http.MethodPost, settingsJSON(t, map[string]any{"projects": []settings.Project{{Path: project, Name: "Kept"}}}))
	if response.Code != http.StatusOK {
		t.Fatalf("projects-only update = %d %s", response.Code, response.Body.String())
	}
	reopened, err := settings.Open(cfg.StateDir)
	if err != nil || len(reopened.Snapshot().Projects) != 1 {
		t.Fatalf("project did not survive update/reopen: %v", err)
	}
}

func TestSettingsCanonicalizesProjectsAndRejectsBrokenSymlink(t *testing.T) {
	cfg := testConfig(t)
	server, err := New(cfg, testAssets(), testModules(t, cfg, Modules{}))
	if err != nil {
		t.Fatal(err)
	}
	cookie := authorize(t, server.Handler(), cfg)
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "project-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{"projects": []map[string]any{{"path": link}}})
	if err != nil {
		t.Fatal(err)
	}
	response := settingsRequest(t, server.Handler(), cfg, cookie, http.MethodPost, string(body))
	if response.Code != http.StatusOK {
		t.Fatalf("project symlink update = %d %s", response.Code, response.Body.String())
	}
	stored := server.settings.Snapshot().Projects[0]
	if stored.Path != target || stored.Name != filepath.Base(target) {
		t.Fatalf("normalized project = %#v", stored)
	}
	if _, err := settings.Open(cfg.StateDir); err != nil {
		t.Fatalf("persisted state not canonical: %v", err)
	}

	longName := strings.Repeat("λ", 121)
	body, err = json.Marshal(map[string]any{"projects": []map[string]any{{"path": target, "name": longName}}})
	if err != nil {
		t.Fatal(err)
	}
	response = settingsRequest(t, server.Handler(), cfg, cookie, http.MethodPost, string(body))
	if response.Code != http.StatusOK {
		t.Fatalf("long project name update = %d %s", response.Code, response.Body.String())
	}
	if got := server.settings.Snapshot().Projects[0].Name; got != strings.Repeat("λ", 120) {
		t.Fatalf("name truncation = %q", got)
	}

	broken := filepath.Join(t.TempDir(), "broken")
	if err := os.Symlink(filepath.Join(t.TempDir(), "absent"), broken); err != nil {
		t.Fatal(err)
	}
	badBody, err := json.Marshal(map[string]any{"projects": []map[string]any{{"path": broken}}})
	if err != nil {
		t.Fatal(err)
	}
	response = settingsRequest(t, server.Handler(), cfg, cookie, http.MethodPost, string(badBody))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("broken symlink = %d %s", response.Code, response.Body.String())
	}
	if got := server.settings.Snapshot().Projects[0].Name; got != strings.Repeat("λ", 120) {
		t.Fatalf("failed update lost prior project: %q", got)
	}

	for _, name := range []any{nil, true} {
		body, err = json.Marshal(map[string]any{"projects": []map[string]any{{"path": target, "name": name}}})
		if err != nil {
			t.Fatal(err)
		}
		response = settingsRequest(t, server.Handler(), cfg, cookie, http.MethodPost, string(body))
		if response.Code != http.StatusBadRequest {
			t.Errorf("invalid project name type %T = %d %s", name, response.Code, response.Body.String())
		}
	}
}
