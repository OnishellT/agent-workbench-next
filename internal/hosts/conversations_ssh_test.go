package hosts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func conversationFixture(t *testing.T) (*SSHManager, http.Handler, func(string) Host, func(Host, string, string, string) Source) {
	t.Helper()
	store := testStore(t)
	manager, err := NewSSHManager(store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	mux := http.NewServeMux()
	server := &Server{Store: store, SSH: manager}
	server.hostRoutes(mux)
	host := func(id string) Host {
		address, port, pin := sshFixture(t, t.TempDir())
		h, err := manager.Save(HostInput{Host: Host{ID: id, Address: address, Port: port, User: "dev", Auth: "password", Enabled: true, Fingerprint: pin}, Password: "fixture-secret"})
		if err != nil {
			t.Fatal(err)
		}
		trustSSHFixture(t, manager, context.Background(), &h, pin)
		return h
	}
	source := func(h Host, id, kind, root string) Source {
		s := Source{ID: id, HostID: h.ID, Name: id, Kind: kind, Path: root, Enabled: true}
		if err := store.PutSource(s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	return manager, mux, host, source
}

func conversationRequest(handler http.Handler, target string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", target, nil))
	return w
}

func conversationWrite(t *testing.T, root, relative, data string) {
	t.Helper()
	filename := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func conversationSelection(h Host, s Source, relative string) string {
	return "/api/hosts/" + h.ID + "/conversations/file?source_id=" + url.QueryEscape(s.ID) + "&path=" + url.QueryEscape(relative) + "&source_revision=" + conversationSourceRevision(h, s)
}

func TestSSHConversationCatalogPaginationAndHostIsolation(t *testing.T) {
	manager, handler, host, source := conversationFixture(t)
	a, b := host("history-a"), host("history-b")
	rootA, rootB := t.TempDir(), t.TempDir()
	for _, relative := range []string{"a.jsonl", "a/sub/session.jsonl", "z.jsonl"} {
		conversationWrite(t, rootA, relative, "{\"private\":\"host-a\"}\n")
	}
	conversationWrite(t, rootA, "settings.jsonl", "secret config")
	conversationWrite(t, rootA, ".git/evidence.jsonl", "private git")
	conversationWrite(t, rootA, "not-jsonl.txt", "private text")
	conversationWrite(t, rootB, "a.jsonl", "{\"private\":\"host-b\"}\n")
	sourceA := source(a, "a-codex", "codex", rootA)
	source(a, "a-absent", "claude", filepath.Join(rootA, "absent"))
	source(a, "a-unsupported", "opencode", "/not/a/real/database")
	sourceB := source(b, "b-codex", "codex", rootB)
	for _, adapter := range []string{"claude", "pi", "omp"} {
		root := t.TempDir()
		conversationWrite(t, root, "native.jsonl", "{\"adapter\":\""+adapter+"\"}\n")
		source(a, "a-"+adapter, adapter, root)
	}
	seen := map[string]bool{}
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > 20 {
			t.Fatal("cursor did not terminate")
		}
		w := conversationRequest(handler, "/api/hosts/"+a.ID+"/conversations?limit=1&cursor="+url.QueryEscape(cursor))
		if w.Code != 200 {
			t.Fatalf("catalog: %d %s", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), rootA) {
			t.Fatal("catalog leaked content or absolute root")
		}
		var page conversationCatalog
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if page.HostID != a.ID || len(page.Files) > 1 {
			t.Fatalf("unbounded or wrong host: %+v", page)
		}
		for _, f := range page.Files {
			key := f.SourceID + ":" + f.Path
			if seen[key] {
				t.Fatalf("duplicate: %s", key)
			}
			seen[key] = true
			if f.Size <= 0 || f.Mtime <= 0 || !conversationAdapter(f.Adapter) || !conversationRevisionValid(f.SourceRevision) {
				t.Fatalf("invalid metadata: %+v", f)
			}
			selected := conversationRequest(handler, "/api/hosts/"+a.ID+"/conversations/file?source_id="+f.SourceID+"&path="+url.QueryEscape(f.Path)+"&source_revision="+f.SourceRevision)
			if selected.Code != 200 || selected.Header().Get("Content-Type") != "application/x-ndjson" || selected.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("selected data: %d %s", selected.Code, selected.Body.String())
			}
		}
		if page.NextCursor == nil {
			if page.Limited {
				t.Fatal("limited without cursor")
			}
			break
		}
		if !page.Limited || *page.NextCursor == cursor {
			t.Fatal("cursor did not advance")
		}
		cursor = *page.NextCursor
	}
	if len(seen) != 6 {
		t.Fatalf("expected six native files, got %v", seen)
	}
	for _, relative := range []string{"a.jsonl", "a/sub/session.jsonl", "z.jsonl"} {
		if !seen["a-codex:"+relative] {
			t.Fatalf("older nested file skipped: %s", relative)
		}
	}
	w := conversationRequest(handler, conversationSelection(b, sourceB, "a.jsonl"))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "host-b") || strings.Contains(w.Body.String(), "host-a") {
		t.Fatalf("host isolation: %d %s", w.Code, w.Body.String())
	}
	w = conversationRequest(handler, conversationSelection(b, sourceA, "a.jsonl"))
	if w.Code != 404 {
		t.Fatalf("cross-host source: %d", w.Code)
	}
	observed, err := manager.Store.Host(a.ID)
	if err != nil || observed.Files != a.Files || observed.SyncedFiles != a.SyncedFiles || observed.LastSync != a.LastSync {
		t.Fatalf("history modified statistics: %+v %v", observed, err)
	}
}

func TestSSHConversationSelectionsAndRevocation(t *testing.T) {
	manager, handler, host, source := conversationFixture(t)
	h := host("history-security")
	root, outside := t.TempDir(), t.TempDir()
	conversationWrite(t, root, "session.jsonl", "{\"evidence\":1}\n")
	conversationWrite(t, root, "auth.jsonl", "private credentials")
	conversationWrite(t, outside, "other.jsonl", "outside content")
	s := source(h, "security-source", "pi", root)
	if err := os.Symlink(filepath.Join(outside, "other.jsonl"), filepath.Join(root, "link.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	linkedSource := source(h, "link-root", "codex", filepath.Join(root, "linked"))
	source(h, "link-file-root", "codex", filepath.Join(root, "link.jsonl"))
	if w := conversationRequest(handler, conversationSelection(h, linkedSource, "other.jsonl")); w.Code != 400 {
		t.Fatalf("symlink source root: %d", w.Code)
	}
	if command, err := exec.LookPath("mkfifo"); err == nil {
		if err := exec.Command(command, filepath.Join(root, "fifo.jsonl")).Run(); err != nil {
			t.Fatal(err)
		}
	}
	for _, relative := range []string{"../other.jsonl", "/absolute.jsonl", "a/../session.jsonl", "a//session.jsonl", "auth.jsonl", "credentials.backup.jsonl", "config.json", ".ssh/private.jsonl", "link.jsonl", "linked/other.jsonl", "session.jsonl/child.jsonl", "back\\slash.jsonl", "fifo.jsonl"} {
		w := conversationRequest(handler, conversationSelection(h, s, relative))
		if w.Code == 200 || strings.Contains(w.Body.String(), "outside content") || strings.Contains(w.Body.String(), root) {
			t.Fatalf("unsafe selection %q: %d %s", relative, w.Code, w.Body.String())
		}
	}
	for _, query := range []string{"limit=0", "limit=101", "limit=bad", "cursor=invalid"} {
		if w := conversationRequest(handler, "/api/hosts/"+h.ID+"/conversations?"+query); w.Code != 400 {
			t.Fatalf("invalid query %q: %d", query, w.Code)
		}
	}
	removed := source(h, "removed-source", "claude", root)
	removedRead, err := manager.openConversations(context.Background(), h.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Store.DB.Exec("DELETE FROM sources WHERE id=?", removed.ID); err != nil {
		t.Fatal(err)
	}
	if err := removedRead.check(&removed); !errors.Is(err, errConversationChanged) {
		t.Fatalf("deleted source: %v", err)
	}
	removedRead.close()
	if w := conversationRequest(handler, conversationSelection(h, removed, "session.jsonl")); w.Code != 404 {
		t.Fatalf("missing source: %d", w.Code)
	}
	read, err := manager.openConversations(context.Background(), h.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.Enabled = false
	if err := manager.Store.PutSource(s); err != nil {
		t.Fatal(err)
	}
	if _, err := read.file(s.ID, "session.jsonl", conversationSourceRevision(h, s)); err == nil {
		t.Fatal("revoked source readable")
	}
	read.close()
	w := conversationRequest(handler, "/api/hosts/"+h.ID+"/conversations")
	var page conversationCatalog
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || w.Code != 200 || len(page.Files) != 0 {
		t.Fatalf("disabled source: %d %s", w.Code, w.Body.String())
	}
	s.Enabled = true
	if err := manager.Store.PutSource(s); err != nil {
		t.Fatal(err)
	}
	read, err = manager.openConversations(context.Background(), h.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.Path = outside
	if err := manager.Store.PutSource(s); err != nil {
		t.Fatal(err)
	}
	if err := read.check(&Source{ID: s.ID, HostID: h.ID, Kind: "pi", Path: root, Enabled: true}); !errors.Is(err, errConversationChanged) {
		t.Fatalf("source identity changed: %v", err)
	}
	read.close()
	read, err = manager.openConversations(context.Background(), h.ID)
	if err != nil {
		t.Fatal(err)
	}
	h.Enabled = false
	if _, err := manager.Save(HostInput{Host: h}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-read.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("host disable did not cancel read")
	}
	if _, err := read.file(s.ID, "other.jsonl", conversationSourceRevision(h, s)); err == nil {
		t.Fatal("disabled host readable")
	}
	read.close()
	if w := conversationRequest(handler, "/api/hosts/"+h.ID+"/conversations"); w.Code != 403 {
		t.Fatalf("disabled host: %d", w.Code)
	}
	if w := conversationRequest(handler, "/api/hosts/missing/conversations"); w.Code != 404 {
		t.Fatalf("missing host: %d", w.Code)
	}
	h.Enabled = true
	h, err = manager.Save(HostInput{Host: h})
	if err != nil {
		t.Fatal(err)
	}
	read, err = manager.openConversations(context.Background(), h.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Save(HostInput{Host: h}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-read.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("host revision did not cancel read")
	}
	read.close()
	read, err = manager.openConversations(context.Background(), h.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Store.DB.Exec("DELETE FROM hosts WHERE id=?", h.ID); err != nil {
		t.Fatal(err)
	}
	if err := read.check(nil); !errors.Is(err, errConversationChanged) {
		t.Fatalf("deleted host: %v", err)
	}
	read.close()
	if w := conversationRequest(handler, "/api/hosts/"+h.ID+"/conversations"); w.Code != 404 {
		t.Fatalf("revoked host: %d", w.Code)
	}
}

func TestSSHConversationFileRootLimitsTrustAndCancel(t *testing.T) {
	manager, handler, host, source := conversationFixture(t)
	h := host("history-limits")
	root := t.TempDir()
	conversationWrite(t, root, "native.jsonl", "{\"raw\":true}\n")
	s := source(h, "one-file", "omp", filepath.Join(root, "native.jsonl"))
	w := conversationRequest(handler, "/api/hosts/"+h.ID+"/conversations")
	var page conversationCatalog
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || w.Code != 200 || len(page.Files) != 1 || page.Files[0].Path != "native.jsonl" {
		t.Fatalf("registered file root: %d %s", w.Code, w.Body.String())
	}
	selected := conversationSelection(h, s, "native.jsonl")
	if w := conversationRequest(handler, selected); w.Code != 200 || w.Body.String() != "{\"raw\":true}\n" {
		t.Fatalf("raw file bytes: %d %s", w.Code, w.Body.String())
	}
	if err := os.Truncate(s.Path, conversationMaxBytes+1); err != nil {
		t.Fatal(err)
	}
	if w := conversationRequest(handler, selected); w.Code != 413 {
		t.Fatalf("oversized file: %d %s", w.Code, w.Body.String())
	}
	ctx, cancel := context.WithCancel(context.Background())
	read, err := manager.openConversations(ctx, h.ID)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := read.catalog("", 32); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	read.close()
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	if read, err := manager.openConversations(ctx, h.ID); err == nil {
		read.close()
		t.Fatal("pre-cancelled request connected")
	}
	secret, err := manager.Store.hostSecret(h.ID)
	if err != nil {
		t.Fatal(err)
	}
	h.Fingerprint = "SHA256:wrong-fingerprint"
	if err := manager.Store.putHost(h, secret); err != nil {
		t.Fatal(err)
	}
	h, err = manager.Store.Host(h.ID)
	if err != nil {
		t.Fatal(err)
	}
	selected = conversationSelection(h, s, "native.jsonl")
	if w := conversationRequest(handler, selected); w.Code != http.StatusConflict || strings.Contains(w.Body.String(), "fixture-secret") || strings.Contains(w.Body.String(), root) {
		t.Fatalf("changed fingerprint must quarantine identity: %d %s", w.Code, w.Body.String())
	}
	h.Fingerprint = ""
	if err := manager.Store.putHost(h, secret); err != nil {
		t.Fatal(err)
	}
	h, err = manager.Store.Host(h.ID)
	if err != nil {
		t.Fatal(err)
	}
	selected = conversationSelection(h, s, "native.jsonl")
	if w := conversationRequest(handler, selected); w.Code != 403 {
		t.Fatalf("unpinned host: %d", w.Code)
	}
}

func TestSSHConversationCursorBindsSourceAndHost(t *testing.T) {
	manager, handler, host, source := conversationFixture(t)
	a, b := host("cursor-a"), host("cursor-b")
	root := t.TempDir()
	conversationWrite(t, root, "one.jsonl", "{}\n")
	conversationWrite(t, root, "two.jsonl", "{}\n")
	s := source(a, "cursor-source", "codex", root)
	source(b, "other-source", "codex", root)
	w := conversationRequest(handler, "/api/hosts/"+a.ID+"/conversations?limit=1")
	var page conversationCatalog
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || w.Code != 200 || page.NextCursor == nil {
		t.Fatalf("cursor: %d %s", w.Code, w.Body.String())
	}
	cursor := url.QueryEscape(*page.NextCursor)
	if w := conversationRequest(handler, "/api/hosts/"+b.ID+"/conversations?cursor="+cursor); w.Code != 400 {
		t.Fatalf("cross-host cursor: %d", w.Code)
	}
	s.Path = t.TempDir()
	if err := manager.Store.PutSource(s); err != nil {
		t.Fatal(err)
	}
	if w := conversationRequest(handler, "/api/hosts/"+a.ID+"/conversations?cursor="+cursor); w.Code != 400 {
		t.Fatalf("revoked cursor: %d", w.Code)
	}
}

func TestSSHConversationScanCursorAdvancesPastFilteredEntries(t *testing.T) {
	_, handler, host, source := conversationFixture(t)
	h := host("history-scan-budget")
	root := t.TempDir()
	for i := range conversationScanLimit {
		conversationWrite(t, root, fmt.Sprintf(".hidden-%04d.jsonl", i), "not conversation evidence")
	}
	conversationWrite(t, root, "z-session.jsonl", "{}\n")
	source(h, "scan-source", "codex", root)
	first := conversationRequest(handler, "/api/hosts/"+h.ID+"/conversations")
	var page conversationCatalog
	if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil || first.Code != 200 || len(page.Files) != 0 || page.NextCursor == nil || !page.Limited {
		t.Fatalf("scan budget: %d %s", first.Code, first.Body.String())
	}
	second := conversationRequest(handler, "/api/hosts/"+h.ID+"/conversations?cursor="+url.QueryEscape(*page.NextCursor))
	if err := json.Unmarshal(second.Body.Bytes(), &page); err != nil || second.Code != 200 || len(page.Files) != 1 || page.Files[0].Path != "z-session.jsonl" || page.NextCursor != nil {
		t.Fatalf("filtered cursor starvation: %d %s", second.Code, second.Body.String())
	}
}

func TestSSHConversationOversizedCursorFailsExplicitly(t *testing.T) {
	_, handler, host, source := conversationFixture(t)
	h := host("history-cursor-bound")
	root := t.TempDir()
	relative := ""
	for range 13 {
		relative = filepath.Join(relative, strings.Repeat("\"", 240))
	}
	conversationWrite(t, root, filepath.Join(relative, "a.jsonl"), "{}\n")
	conversationWrite(t, root, filepath.Join(relative, "b.jsonl"), "{}\n")
	source(h, "cursor-bound-source", "codex", root)
	w := conversationRequest(handler, "/api/hosts/"+h.ID+"/conversations?limit=1")
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "8192") || strings.Contains(w.Body.String(), root) {
		t.Fatalf("oversized continuation must not look complete: %d %s", w.Code, w.Body.String())
	}
}

func TestSSHConversationSourceRevisionRejectsCatalogSelectionRaces(t *testing.T) {
	manager, handler, host, source := conversationFixture(t)
	h := host("history-source-revision")
	firstRoot, secondRoot := t.TempDir(), t.TempDir()
	conversationWrite(t, firstRoot, "session.jsonl", "{\"evidence\":\"first-root\"}\n")
	conversationWrite(t, secondRoot, "session.jsonl", "{\"evidence\":\"second-root\"}\n")
	s := source(h, "revision-source", "codex", firstRoot)
	catalog := func() conversationFile {
		t.Helper()
		w := conversationRequest(handler, "/api/hosts/"+h.ID+"/conversations")
		var page conversationCatalog
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || w.Code != 200 || len(page.Files) != 1 || !conversationRevisionValid(page.Files[0].SourceRevision) {
			t.Fatalf("catalog revision: %d %s", w.Code, w.Body.String())
		}
		return page.Files[0]
	}
	selection := func(file conversationFile) string {
		return "/api/hosts/" + h.ID + "/conversations/file?source_id=" + url.QueryEscape(file.SourceID) + "&path=" + url.QueryEscape(file.Path) + "&source_revision=" + file.SourceRevision
	}
	old := catalog()
	for _, change := range []struct{ kind, root, content string }{{"claude", firstRoot, "first-root"}, {"claude", secondRoot, "second-root"}} {
		s.Kind, s.Path = change.kind, change.root
		if err := manager.Store.PutSource(s); err != nil {
			t.Fatal(err)
		}
		w := conversationRequest(handler, selection(old))
		if w.Code != http.StatusConflict || strings.Contains(w.Body.String(), "evidence") || strings.Contains(w.Body.String(), firstRoot) || strings.Contains(w.Body.String(), secondRoot) {
			t.Fatalf("stale catalog must not read changed source: %d %s", w.Code, w.Body.String())
		}
		current := catalog()
		if current.SourceRevision == old.SourceRevision || current.Adapter != change.kind {
			t.Fatalf("source identity change not reflected: %+v", current)
		}
		w = conversationRequest(handler, selection(current))
		if w.Code != 200 || !strings.Contains(w.Body.String(), change.content) {
			t.Fatalf("refreshed catalog must read current source: %d %s", w.Code, w.Body.String())
		}
		old = current
	}
	s.Name, s.Description, s.Status, s.LastScan = "Renamed", "Observation only", "warning", "later"
	if err := manager.Store.PutSource(s); err != nil {
		t.Fatal(err)
	}
	if current := catalog(); current.SourceRevision != old.SourceRevision {
		t.Fatal("observation and display fields changed semantic source revision")
	}
	base := "/api/hosts/" + h.ID + "/conversations/file?source_id=" + s.ID + "&path=session.jsonl"
	for _, invalid := range []string{"", "invalid", strings.Repeat("A", 42), strings.Repeat("!", 43), strings.Repeat("A", 43) + "="} {
		w := conversationRequest(handler, base+"&source_revision="+url.QueryEscape(invalid))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("missing or malformed revision %q: %d", invalid, w.Code)
		}
	}
	if w := conversationRequest(handler, base+"&source_revision="+strings.Repeat("A", 43)); w.Code != http.StatusConflict {
		t.Fatalf("well-formed wrong source revision: %d", w.Code)
	}
}

// A trusted host with history consent but no manual sync starts with zero
// sources: auto-capture discovers the native defaults over SSH (no helper,
// no manual sync) and imports their files, while a configured disabled source
// keeps its path and stays out of the catalog. Usage counters and host config
// are never written by the discovery.
func TestSSHConversationAutoDiscoversNativeSources(t *testing.T) {
	store := testStore(t)
	manager, err := NewSSHManager(store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	mux := http.NewServeMux()
	server := &Server{Store: store, SSH: manager}
	server.hostRoutes(mux)
	home := t.TempDir()
	address, port, pin := sshFixture(t, home)
	h, err := manager.Save(HostInput{Host: Host{ID: "auto-discover", Address: address, Port: port, User: "dev", Auth: "password", Enabled: true, Fingerprint: pin}, Password: "fixture-secret"})
	if err != nil {
		t.Fatal(err)
	}
	trustSSHFixture(t, manager, context.Background(), &h, pin)
	custom := filepath.Join(t.TempDir(), "custom-pi")
	if err := store.PutSource(Source{ID: h.ID + "-pi", HostID: h.ID, Name: "custom", Kind: "pi", Path: custom, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 9; i++ {
		conversationWrite(t, filepath.Join(home, ".codex", "sessions"), fmt.Sprintf("s%02d.jsonl", i), "{\"session\":1}\n")
	}
	read := func() conversationCatalog {
		t.Helper()
		w := conversationRequest(mux, "/api/hosts/"+h.ID+"/conversations?limit=100")
		if w.Code != 200 {
			t.Fatalf("catalog: %d %s", w.Code, w.Body.String())
		}
		var page conversationCatalog
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		return page
	}
	page := read()
	if page.HostID != h.ID || len(page.Files) != 9 {
		t.Fatalf("auto-capture must import the 9 native files, got %+v", page)
	}
	for _, f := range page.Files {
		if f.SourceID != h.ID+"-codex" || f.Adapter != "codex" || !conversationRevisionValid(f.SourceRevision) {
			t.Fatalf("unexpected file row: %+v", f)
		}
	}
	rows, err := store.Sources()
	if err != nil {
		t.Fatal(err)
	}
	kept := false
	for _, row := range rows {
		if row.HostID != h.ID {
			continue
		}
		if row.ID == h.ID+"-pi" {
			kept = row.Path == custom && !row.Enabled
		}
	}
	if !kept {
		t.Fatalf("disabled custom source was not preserved: %+v", rows)
	}
	again := read()
	if len(again.Files) != 9 {
		t.Fatalf("second catalog changed the import: %d files", len(again.Files))
	}
	observed, err := store.Host(h.ID)
	if err != nil || observed.Files != 0 || observed.SyncedFiles != 0 || observed.LastSync != "" || observed.Home != "" {
		t.Fatalf("discovery wrote usage or config: %+v %v", observed, err)
	}
}
