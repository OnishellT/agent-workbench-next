// SPDX-License-Identifier: AGPL-3.0-only
package hosts

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSSHHostSyncScansConversationFilesWithoutUsageTracker(t *testing.T) {
	home := t.TempDir()
	sessionPath := filepath.Join(home, ".codex", "sessions", "fixture.jsonl")
	if err := os.MkdirAll(filepath.Dir(sessionPath), 0700); err != nil {
		t.Fatal(err)
	}
	const transcript = `{"session_id":"remote-fixture","role":"user","kind":"message","text":"synthetic remote conversation"}` + "\n"
	if err := os.WriteFile(sessionPath, []byte(transcript), 0600); err != nil {
		t.Fatal(err)
	}
	address, port, pin := sshFixture(t, home)
	m, _ := newTestModule(t)
	host, err := m.SSH.Save(HostInput{Host: Host{ID: "ssh-sync-fixture", Address: address, Port: port, User: "dev", Auth: "password", Enabled: true}, Password: "fixture-secret"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := m.SSH.sync(ctx, &host); err == nil || host.ObservedFingerprint != pin {
		t.Fatalf("unknown fixture host key should require explicit trust: host=%+v err=%v", host, err)
	}
	host.Status, host.Error = "error", "fixture key observed"
	if err := m.Store.hostState(host); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SSH.TrustFingerprint(host.ID, host.Revision, pin); err != nil {
		t.Fatal(err)
	}
	started, err := m.SSH.Start(host.ID)
	if err != nil || !started {
		t.Fatalf("explicit SSH sync did not start: started=%v err=%v", started, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		host, err = m.Store.Host(host.ID)
		if err != nil {
			t.Fatal(err)
		}
		if host.Status == "connected" || host.Status == "warning" {
			break
		}
		if host.Status == "error" {
			t.Fatalf("local SSH fixture sync failed: %+v", host)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if host.Status != "connected" && host.Status != "warning" {
		t.Fatalf("local SSH fixture sync did not finish: %+v", host)
	}
	if host.Files != 1 || host.SyncedFiles != 1 || host.Home != home {
		t.Fatalf("SSH sync lost remote conversation file metadata: %+v", host)
	}
	var eventsTable int
	if err := m.Store.DB.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='events'").Scan(&eventsTable); err != nil || eventsTable != 0 {
		t.Fatalf("host-only sync created a token-usage ledger: events table count=%d err=%v", eventsTable, err)
	}
	var sources []Source
	sources, err = m.Store.Sources()
	if err != nil {
		t.Fatal(err)
	}
	var codex Source
	for _, source := range sources {
		if source.ID == host.ID+"-codex" {
			codex = source
			break
		}
	}
	if codex.ID == "" || codex.Files != 1 {
		t.Fatalf("conversation source scan was lost or became usage ingestion: %+v", codex)
	}
	response := moduleRequest(t, m.Handler(), http.MethodGet, "/api/hosts/"+host.ID+"/conversations", "")
	var catalog conversationCatalog
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &catalog) != nil || len(catalog.Files) != 1 {
		t.Fatalf("SSH conversation catalog status=%d body=%s", response.Code, response.Body.String())
	}
	file := catalog.Files[0]
	selected := moduleRequest(t, m.Handler(), http.MethodGet, conversationSelection(host, codex, file.Path), "")
	if selected.Code != http.StatusOK || !strings.Contains(selected.Body.String(), "synthetic remote conversation") {
		t.Fatalf("SSH conversation file read status=%d body=%s", selected.Code, selected.Body.String())
	}
	retired := moduleRequest(t, m.Handler(), http.MethodGet, "/api/atlas/api/summary", "")
	if retired.Code != http.StatusNotFound {
		t.Fatalf("Hosts module exposed retired Atlas route: %d %s", retired.Code, retired.Body.String())
	}
}

func TestHostsModulePreservesLegacyHostAndLedgerRows(t *testing.T) {
	stateDir := t.TempDir()
	legacy, err := OpenStore(filepath.Join(stateDir, "usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.DB.Exec("CREATE TABLE events(id TEXT PRIMARY KEY, payload TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.DB.Exec("INSERT INTO events(id,payload) VALUES('legacy-event','preserve-me')"); err != nil {
		t.Fatal(err)
	}
	legacyHost := Host{ID: "legacy-host", Name: "Legacy host", Address: "192.0.2.44", User: "operator", Port: 22, Auth: "agent", Enabled: false, Status: "paused", Revision: 7}
	if err := legacy.putHost(legacyHost, []byte{}); err != nil {
		t.Fatal(err)
	}
	if err := legacy.DB.Close(); err != nil {
		t.Fatal(err)
	}
	m, err := NewModule(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	got, err := m.Store.Host(legacyHost.ID)
	if err != nil || got.ID != legacyHost.ID || got.Revision != legacyHost.Revision || got.Address != legacyHost.Address {
		t.Fatalf("host migration lost legacy identity: %+v err=%v", got, err)
	}
	var payload string
	if err := m.Store.DB.QueryRow("SELECT payload FROM events WHERE id='legacy-event'").Scan(&payload); err != nil || payload != "preserve-me" {
		t.Fatalf("opening Hosts removed or changed legacy ledger rows: payload=%q err=%v", payload, err)
	}
}

func TestSSHNativeEnvironmentSourcesPreserveConfiguredPaths(t *testing.T) {
	home := t.TempDir()
	codex := filepath.Join(home, "custom codex")
	omp := filepath.Join(home, "shared agent")
	for _, dir := range []string{filepath.Join(codex, "sessions"), filepath.Join(omp, "sessions")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(codex, "sessions", "turn.jsonl"), []byte("{\"session_id\":\"custom-native-session\",\"role\":\"user\",\"kind\":\"message\",\"text\":\"fixture\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(omp, "sessions", "turn.jsonl"), []byte("{\"session_id\":\"custom-omp-session\",\"role\":\"user\",\"kind\":\"message\",\"text\":\"fixture\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	address, port, pin := sshFixture(t, home, map[string]string{"CODEX_HOME": codex, "PI_CODING_AGENT_DIR": omp, "PI_CONFIG_DIR": "ignored-config"})
	m, _ := newTestModule(t)
	host, err := m.SSH.Save(HostInput{Host: Host{ID: "native-path-host", Address: address, Port: port, User: "dev", Auth: "password", Enabled: true}, Password: "fixture-secret"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	trustSSHFixture(t, m.SSH, ctx, &host, pin)
	if err := m.SSH.sync(ctx, &host); err != nil {
		t.Fatal(err)
	}
	rows, err := m.Store.Sources()
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Source{}
	for _, row := range rows {
		byID[row.ID] = row
	}
	if byID[host.ID+"-codex"].Path != filepath.Join(codex, "sessions") || byID[host.ID+"-omp"].Path != filepath.Join(omp, "sessions") {
		t.Fatalf("custom environment paths ignored: %+v", byID)
	}
	if _, exists := byID[host.ID+"-pi"]; exists {
		t.Fatal("shared Pi/OMP root registered twice")
	}
	if byID[host.ID+"-codex"].Files != 1 || byID[host.ID+"-omp"].Files != 1 {
		t.Fatalf("host scan did not preserve conversation-only metadata: %+v", byID)
	}
	source := byID[host.ID+"-codex"]
	source.Path = filepath.Join(home, "user-selected")
	source.Enabled = false
	if err := m.Store.PutSource(source); err != nil {
		t.Fatal(err)
	}
	if err := m.SSH.sync(ctx, &host); err != nil {
		t.Fatal(err)
	}
	rows, err = m.Store.Sources()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID == source.ID && (row.Path != source.Path || row.Enabled) {
			t.Fatalf("configured source overwritten: %+v", row)
		}
	}
}

func TestSSHNativePathPrecedenceAndExistingPiIdentity(t *testing.T) {
	home := "/remote/home"
	for _, scenario := range []struct {
		env []string
		omp string
	}{
		{[]string{"", "", "", "", ".omp-custom", ""}, "/remote/home/.omp-custom/agent/sessions"},
		{[]string{"", "", "/isolated/omp", "/shared/pi", "ignored", ""}, "/isolated/omp/sessions"},
		{[]string{"", "", "", "", "/home-relative-config", ""}, "/remote/home/home-relative-config/agent/sessions"},
	} {
		rows := sshSourceDefaults(home, scenario.env)
		for _, row := range rows {
			if row.Kind == "omp" && row.Path != scenario.omp {
				t.Fatalf("OMP path=%q, want %q", row.Path, scenario.omp)
			}
		}
	}
	store := testStore(t)
	manager, err := NewSSHManager(store)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	existing := Source{ID: "host-pi", HostID: "host", Name: "Pi retained", Kind: "pi", Path: "/shared/agent/sessions", Enabled: true}
	if err := store.PutSource(existing); err != nil {
		t.Fatal(err)
	}
	if err := manager.ensureNativeHostSources("host", sshSourceDefaults(home, []string{"", "", "", "/shared/agent", "", ""})); err != nil {
		t.Fatal(err)
	}
	rows, err := store.Sources()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID == "host-omp" {
			t.Fatal("existing shared Pi source duplicated")
		}
		if row.ID == existing.ID && (row.Kind != "pi" || row.Name != "Pi retained") {
			t.Fatalf("source identity rewritten: %+v", row)
		}
	}
}
