package hosts

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// lockedHistoryImporter is a race-safe recorder for the automatic capture loop,
// which imports from up to historyAutoMaxHosts hosts concurrently.
type lockedHistoryImporter struct {
	mu     sync.Mutex
	inputs []HistoryImport
}

func (i *lockedHistoryImporter) ImportHostConversation(_ context.Context, input HistoryImport, raw []byte) (HistoryImportResult, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.inputs = append(i.inputs, input)
	return HistoryImportResult{SessionID: input.Identity.HostID + "-session", Revision: "fixture-revision", Changed: true}, nil
}

func (i *lockedHistoryImporter) count() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return len(i.inputs)
}

func (i *lockedHistoryImporter) hostCount(hostID string) int {
	i.mu.Lock()
	defer i.mu.Unlock()
	total := 0
	for _, input := range i.inputs {
		if input.Identity.HostID == hostID {
			total++
		}
	}
	return total
}

func grantHistoryAuto(t *testing.T, handler http.Handler, id string) {
	t.Helper()
	response := moduleRequest(t, handler, http.MethodPost, "/api/hosts/"+id+"/history", `{"enabled":true}`)
	if response.Code != http.StatusOK || historyStatusFromResponse(t, response.Body.Bytes()).State != "ready" {
		t.Fatalf("grant %s: %d %s", id, response.Code, response.Body.String())
	}
}

func waitForImports(t *testing.T, importer *lockedHistoryImporter, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for importer.count() < want {
		if time.Now().After(deadline) {
			t.Fatalf("automatic capture imported %d files, want at least %d", importer.count(), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestHistoryAutoSyncCapturesConsentedHostsWithoutBrowser(t *testing.T) {
	module, _ := newTestModule(t)
	importer := &lockedHistoryImporter{}
	module.SetHistoryImporter(importer)
	hostA, _ := addHistoryFixtureHost(t, module, "auto-a")
	hostB, _ := addHistoryFixtureHost(t, module, "auto-b")
	handler := module.Handler()
	grantHistoryAuto(t, handler, hostA.ID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	module.StartHistoryAutoSync(ctx, 20*time.Millisecond)
	defer module.StopHistoryAutoSync()
	waitForImports(t, importer, 1, 10*time.Second)

	if got := importer.hostCount(hostB.ID); got != 0 {
		t.Fatalf("automatic capture imported %d files from the unconsented host", got)
	}
	status := moduleRequest(t, handler, http.MethodGet, "/api/hosts/"+hostA.ID+"/history", "")
	if status.Code != http.StatusOK || historyStatusFromResponse(t, status.Body.Bytes()).Files != 1 {
		t.Fatalf("automatic capture left no receipt: %d %s", status.Code, status.Body.String())
	}
}

func TestHistoryAutoSyncStopsImportingAfterRevoke(t *testing.T) {
	module, _ := newTestModule(t)
	importer := &lockedHistoryImporter{}
	module.SetHistoryImporter(importer)
	host, _ := addHistoryFixtureHost(t, module, "auto-revoke")
	handler := module.Handler()
	grantHistoryAuto(t, handler, host.ID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	module.StartHistoryAutoSync(ctx, 20*time.Millisecond)
	defer module.StopHistoryAutoSync()
	waitForImports(t, importer, 1, 10*time.Second)

	revoked := moduleRequest(t, handler, http.MethodPost, "/api/hosts/"+host.ID+"/history", `{"enabled":false}`)
	if revoked.Code != http.StatusOK {
		t.Fatalf("revoke: %d %s", revoked.Code, revoked.Body.String())
	}
	time.Sleep(50 * time.Millisecond)
	settled := importer.count()
	time.Sleep(300 * time.Millisecond)
	if importer.count() != settled {
		t.Fatalf("capture kept importing after revoke: %d -> %d", settled, importer.count())
	}
}

func TestHistoryAutoSyncPaginatesBeyondOnePageWithoutDuplicates(t *testing.T) {
	module, _ := newTestModule(t)
	importer := &lockedHistoryImporter{}
	module.SetHistoryImporter(importer)
	home := t.TempDir()
	address, port, pin := sshFixture(t, home)
	host, err := module.SSH.Save(HostInput{Host: Host{ID: "auto-pages", Name: "pages", Address: address, Port: port, User: "dev", Auth: "password", Enabled: true}, Password: "fixture-secret"})
	if err != nil {
		t.Fatal(err)
	}
	trustSSHFixture(t, module.SSH, context.Background(), &host, pin)
	for index := 0; index < historyPageSize+2; index++ {
		name := string(rune('a'+index)) + ".jsonl"
		conversationWrite(t, home, name, "{\"type\":\"item_completed\",\"payload\":{\"item\":{\"id\":\"m"+string(rune('0'+index))+"\",\"type\":\"AgentMessage\",\"text\":\"page fixture\",\"channel\":\"final\"}}}\n")
	}
	root := home
	if err := module.Store.PutSource(Source{ID: "auto-pages-codex", HostID: host.ID, Name: "Codex pages", Kind: "codex", Path: root, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	handler := module.Handler()
	grantHistoryAuto(t, handler, host.ID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	module.StartHistoryAutoSync(ctx, 20*time.Millisecond)
	defer module.StopHistoryAutoSync()
	waitForImports(t, importer, historyPageSize+2, 15*time.Second)

	// Later ticks must find every receipt current and import nothing new.
	time.Sleep(300 * time.Millisecond)
	if importer.count() != historyPageSize+2 {
		t.Fatalf("automatic capture re-imported files: %d, want %d", importer.count(), historyPageSize+2)
	}
	status := moduleRequest(t, handler, http.MethodGet, "/api/hosts/"+host.ID+"/history", "")
	if status.Code != http.StatusOK || historyStatusFromResponse(t, status.Body.Bytes()).Files != historyPageSize+2 {
		t.Fatalf("automatic capture receipts: %d %s", status.Code, status.Body.String())
	}
}

func TestHistoryAutoSyncStartStopIsIdempotent(t *testing.T) {
	module, _ := newTestModule(t)
	module.SetHistoryImporter(&lockedHistoryImporter{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	module.StartHistoryAutoSync(ctx, 20*time.Millisecond)
	module.StartHistoryAutoSync(ctx, 20*time.Millisecond)
	module.StopHistoryAutoSync()
	module.StopHistoryAutoSync()
	cancel()
	module.StopHistoryAutoSync()
}

// incompleteHistoryImporter rejects empty transcripts the way the real
// importer rejects an empty or incomplete native history, and records every
// successful import per path.
type incompleteHistoryImporter struct {
	mu        sync.Mutex
	successes map[string]int
}

func (i *incompleteHistoryImporter) ImportHostConversation(_ context.Context, input HistoryImport, raw []byte) (HistoryImportResult, error) {
	if len(raw) == 0 {
		return HistoryImportResult{}, ErrHistoryIncomplete
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.successes == nil {
		i.successes = map[string]int{}
	}
	i.successes[input.Path]++
	return HistoryImportResult{SessionID: "sess-" + input.Path, Revision: "rev-" + input.Path, Changed: true}, nil
}

func (i *incompleteHistoryImporter) total() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	total := 0
	for _, count := range i.successes {
		total += count
	}
	return total
}

// An incomplete file must not wedge the rest: the run imports every valid
// file past it (across pages via the cursor), keeps the failure visible, and
// invents no receipt for the bad file. A bad file retried on later rounds
// still imports nothing new.
func TestHistoryAutoSyncSkipsIncompleteFileWithoutWedgingRest(t *testing.T) {
	module, _ := newTestModule(t)
	importer := &incompleteHistoryImporter{}
	module.SetHistoryImporter(importer)
	host, root := addHistoryFixtureHost(t, module, "auto-skip")
	conversationWrite(t, root, "a-bad.jsonl", "")
	for index := 0; index < historyPageSize-1; index++ {
		conversationWrite(t, root, fmt.Sprintf("b%02d.jsonl", index), "{}\n")
	}
	handler := module.Handler()
	grantHistoryAuto(t, handler, host.ID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	module.StartHistoryAutoSync(ctx, 20*time.Millisecond)
	defer module.StopHistoryAutoSync()

	deadline := time.Now().Add(15 * time.Second)
	for {
		if importer.total() >= historyPageSize && historyStatusFromResponse(t, moduleRequest(t, handler, http.MethodGet, "/api/hosts/"+host.ID+"/history", "").Body.Bytes()).Error != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("capture imported %d files; want %d with a visible error", importer.total(), historyPageSize)
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Settle: later rounds must neither duplicate imports nor hide the error.
	time.Sleep(300 * time.Millisecond)
	if total := importer.total(); total != historyPageSize {
		t.Fatalf("capture imported %d files, want %d without duplicates", total, historyPageSize)
	}
	status := moduleRequest(t, handler, http.MethodGet, "/api/hosts/"+host.ID+"/history", "")
	observed := historyStatusFromResponse(t, status.Body.Bytes())
	if observed.Files != historyPageSize {
		t.Fatalf("receipts invented or missing: %+v", observed)
	}
	if !strings.Contains(observed.Error, "incomplete") {
		t.Fatalf("skipped file error hidden: %+v", observed)
	}
}

// appendingHistoryImporter binds the revision to the imported bytes with a
// stable session per path, the way a real importer confirms each append as a
// new revision of the same session.
type appendingHistoryImporter struct {
	mu        sync.Mutex
	paths     []string
	sessions  []string
	revisions []string
	raws      [][]byte
}

func (i *appendingHistoryImporter) ImportHostConversation(_ context.Context, input HistoryImport, raw []byte) (HistoryImportResult, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.paths = append(i.paths, input.Path)
	i.sessions = append(i.sessions, "sess-"+input.Path)
	i.revisions = append(i.revisions, fmt.Sprintf("rev-%d", len(raw)))
	i.raws = append(i.raws, append([]byte(nil), raw...))
	return HistoryImportResult{SessionID: "sess-" + input.Path, Revision: fmt.Sprintf("rev-%d", len(raw)), Changed: true}, nil
}

func (i *appendingHistoryImporter) callsFor(path string) int {
	i.mu.Lock()
	defer i.mu.Unlock()
	total := 0
	for _, p := range i.paths {
		if p == path {
			total++
		}
	}
	return total
}

// A file append after the first import must re-import once with a new
// revision under the same session: SourceRevision identifies the source
// config, never the file version, so the receipt binds mtime and size too.
func TestHistoryAutoSyncReimportsAppendedFileWithNewRevision(t *testing.T) {
	module, _ := newTestModule(t)
	importer := &appendingHistoryImporter{}
	module.SetHistoryImporter(importer)
	host, root := addHistoryFixtureHost(t, module, "auto-append")
	before, err := os.ReadFile(filepath.Join(root, "session.jsonl"))
	if err != nil || len(before) == 0 {
		t.Fatalf("fixture file unread: %v", err)
	}
	handler := module.Handler()
	grantHistoryAuto(t, handler, host.ID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	module.StartHistoryAutoSync(ctx, 20*time.Millisecond)
	defer module.StopHistoryAutoSync()

	waitForCalls := func(want int) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for importer.callsFor("session.jsonl") < want {
			if time.Now().After(deadline) {
				t.Fatalf("appended file imported %d times, want %d", importer.callsFor("session.jsonl"), want)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitForCalls(1)
	conversationWrite(t, root, "session.jsonl", string(before)+"{\"appended\":true}\n")
	waitForCalls(2)
	// Settle: no duplicate session, no further imports without new appends.
	time.Sleep(300 * time.Millisecond)
	if total := importer.callsFor("session.jsonl"); total != 2 {
		t.Fatalf("append re-imported %d times, want exactly 2", total)
	}
	importer.mu.Lock()
	sameSession := importer.sessions[0] == importer.sessions[1]
	newRevision := importer.revisions[0] != importer.revisions[1]
	secondSawAppend := strings.Contains(string(importer.raws[1]), "appended")
	importer.mu.Unlock()
	if !sameSession {
		t.Fatalf("append duplicated the session: %v", importer.sessions)
	}
	if !newRevision {
		t.Fatalf("append did not create a new revision: %v", importer.revisions)
	}
	if !secondSawAppend {
		t.Fatalf("second import did not read the appended bytes")
	}
	status := moduleRequest(t, handler, http.MethodGet, "/api/hosts/"+host.ID+"/history", "")
	observed := historyStatusFromResponse(t, status.Body.Bytes())
	if observed.Files != 1 || observed.Error != "" {
		t.Fatalf("append left %+v, want one receipt and no error", observed)
	}
}
