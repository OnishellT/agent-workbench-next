package hosts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordingHistoryImporter struct {
	inputs []HistoryImport
	data   [][]byte
}

func (i *recordingHistoryImporter) ImportHostConversation(_ context.Context, input HistoryImport, raw []byte) (HistoryImportResult, error) {
	i.inputs = append(i.inputs, input)
	i.data = append(i.data, append([]byte(nil), raw...))
	return HistoryImportResult{SessionID: input.Identity.HostID + "-session", Revision: "fixture-revision", Changed: true}, nil
}

func addHistoryFixtureHost(t *testing.T, module *Module, id string) (Host, string) {
	t.Helper()
	home := t.TempDir()
	address, port, pin := sshFixture(t, home)
	host, err := module.SSH.Save(HostInput{Host: Host{ID: id, Name: id, Address: address, Port: port, User: "dev", Auth: "password", Enabled: true}, Password: "fixture-secret"})
	if err != nil {
		t.Fatal(err)
	}
	trustSSHFixture(t, module.SSH, context.Background(), &host, pin)
	root := filepath.Join(home, ".codex", "sessions")
	conversationWrite(t, root, "session.jsonl", "{\"type\":\"item_completed\",\"payload\":{\"item\":{\"id\":\"m1\",\"type\":\"AgentMessage\",\"text\":\""+id+" conversation fixture\",\"channel\":\"final\"}}}\n")
	if err := module.Store.PutSource(Source{ID: id + "-codex", HostID: id, Name: "Codex fixture", Kind: "codex", Path: root, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	return host, root
}

func historyStatusFromResponse(t *testing.T, responseBody []byte) hostHistoryStatus {
	t.Helper()
	var status hostHistoryStatus
	if err := json.Unmarshal(responseBody, &status); err != nil {
		t.Fatalf("decode history status %s: %v", responseBody, err)
	}
	return status
}

func TestHostHistoryConsentSyncRevocationAndHostIsolation(t *testing.T) {
	module, _ := newTestModule(t)
	importer := &recordingHistoryImporter{}
	module.SetHistoryImporter(importer)
	hostA, _ := addHistoryFixtureHost(t, module, "history-a")
	hostB, _ := addHistoryFixtureHost(t, module, "history-b")
	handler := module.Handler()

	noConsent := moduleRequest(t, handler, http.MethodPost, "/api/hosts/"+hostA.ID+"/history/sync", "{}")
	if noConsent.Code != http.StatusForbidden || len(importer.inputs) != 0 {
		t.Fatalf("sync without consent must be blocked before import: %d %s inputs=%d", noConsent.Code, noConsent.Body.String(), len(importer.inputs))
	}
	for _, host := range []Host{hostA, hostB} {
		response := moduleRequest(t, handler, http.MethodPost, "/api/hosts/"+host.ID+"/history", `{"enabled":true}`)
		if response.Code != http.StatusOK || historyStatusFromResponse(t, response.Body.Bytes()).State != "ready" {
			t.Fatalf("grant %s: %d %s", host.ID, response.Code, response.Body.String())
		}
	}
	for _, host := range []Host{hostA, hostB} {
		response := moduleRequest(t, handler, http.MethodPost, "/api/hosts/"+host.ID+"/history/sync", "{}")
		status := historyStatusFromResponse(t, response.Body.Bytes())
		if response.Code != http.StatusOK || status.Files != 1 || status.Imported != 1 || status.State != "ready" {
			t.Fatalf("sync %s: %d %+v %s", host.ID, response.Code, status, response.Body.String())
		}
	}
	if len(importer.inputs) != 2 || len(importer.data) != 2 {
		t.Fatalf("expected exactly two real remote-file imports, got %d", len(importer.inputs))
	}
	for index, host := range []Host{hostA, hostB} {
		if importer.inputs[index].Identity.HostID != host.ID || !strings.Contains(string(importer.data[index]), host.ID+" conversation fixture") || strings.Contains(string(importer.data[index]), otherHistoryHost(host.ID)) {
			t.Fatalf("host history crossed identity or content: host=%s input=%+v data=%s", host.ID, importer.inputs[index], importer.data[index])
		}
	}

	revoked := moduleRequest(t, handler, http.MethodPost, "/api/hosts/"+hostA.ID+"/history", `{"enabled":false}`)
	if revoked.Code != http.StatusOK {
		t.Fatalf("revoke: %d %s", revoked.Code, revoked.Body.String())
	}
	status := historyStatusFromResponse(t, revoked.Body.Bytes())
	if status.Enabled || status.State != "disabled" || status.Files != 1 {
		t.Fatalf("revocation must preserve accepted receipt: %+v", status)
	}
	blocked := moduleRequest(t, handler, http.MethodPost, "/api/hosts/"+hostA.ID+"/history/sync", "{}")
	if blocked.Code != http.StatusForbidden || len(importer.inputs) != 2 {
		t.Fatalf("revoked sync must not read/import again: %d %s inputs=%d", blocked.Code, blocked.Body.String(), len(importer.inputs))
	}
	persisted := moduleRequest(t, handler, http.MethodGet, "/api/hosts/"+hostA.ID+"/history", "")
	if persisted.Code != http.StatusOK || historyStatusFromResponse(t, persisted.Body.Bytes()).Files != 1 {
		t.Fatalf("accepted history receipt disappeared after revoke: %d %s", persisted.Code, persisted.Body.String())
	}
}

func TestHostHistoryRequiresCurrentPinnedIdentity(t *testing.T) {
	module, _ := newTestModule(t)
	importer := &recordingHistoryImporter{}
	module.SetHistoryImporter(importer)
	host, err := module.SSH.Save(HostInput{Host: Host{ID: "history-unpinned", Name: "Unpinned", Address: "127.0.0.1", Port: 18998, User: "dev", Auth: "password", Enabled: true, Fingerprint: "SHA256:untrusted"}, Password: "fixture-secret"})
	if err != nil {
		t.Fatal(err)
	}
	handler := module.Handler()
	grant := moduleRequest(t, handler, http.MethodPost, "/api/hosts/"+host.ID+"/history", `{"enabled":true}`)
	if grant.Code != http.StatusConflict {
		t.Fatalf("untrusted fingerprint granted consent: %d %s", grant.Code, grant.Body.String())
	}
	sync := moduleRequest(t, handler, http.MethodPost, "/api/hosts/"+host.ID+"/history/sync", "{}")
	if sync.Code != http.StatusConflict || len(importer.inputs) != 0 {
		t.Fatalf("untrusted host started a history read: %d %s", sync.Code, sync.Body.String())
	}
	status := moduleRequest(t, handler, http.MethodGet, "/api/hosts/"+host.ID+"/history", "")
	if status.Code != http.StatusOK || historyStatusFromResponse(t, status.Body.Bytes()).State != "untrusted" {
		t.Fatalf("untrusted host status: %d %s", status.Code, status.Body.String())
	}
}

func otherHistoryHost(hostID string) string {
	if hostID == "history-a" {
		return "history-b"
	}
	return "history-a"
}

type gateHistoryImporter struct {
	entered chan string
	release chan struct{}
	mu      sync.Mutex
	calls   int
}

func (i *gateHistoryImporter) ImportHostConversation(ctx context.Context, input HistoryImport, _ []byte) (HistoryImportResult, error) {
	i.mu.Lock()
	i.calls++
	i.mu.Unlock()
	select {
	case i.entered <- input.Identity.HostID:
	default:
	}
	select {
	case <-i.release:
	case <-ctx.Done():
		return HistoryImportResult{}, ctx.Err()
	}
	return HistoryImportResult{SessionID: "gate-" + input.Identity.HostID + "-" + input.Path, Revision: "gate-revision", Changed: true}, nil
}

func (i *gateHistoryImporter) callCount() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.calls
}

func grantHistory(t *testing.T, handler http.Handler, id string) {
	t.Helper()
	response := moduleRequest(t, handler, http.MethodPost, "/api/hosts/"+id+"/history", `{"enabled":true}`)
	if response.Code != http.StatusOK {
		t.Fatalf("grant %s: %d %s", id, response.Code, response.Body.String())
	}
}

func TestHostHistoryRevokeCancelsInFlightAndRegrantDoesNotPublish(t *testing.T) {
	module, _ := newTestModule(t)
	importer := &gateHistoryImporter{entered: make(chan string, 4), release: make(chan struct{})}
	module.SetHistoryImporter(importer)
	hostA, _ := addHistoryFixtureHost(t, module, "history-flight-a")
	hostB, _ := addHistoryFixtureHost(t, module, "history-flight-b")
	handler := module.Handler()
	grantHistory(t, handler, hostA.ID)
	grantHistory(t, handler, hostB.ID)

	syncResult := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		syncResult <- moduleRequest(t, handler, http.MethodPost, "/api/hosts/"+hostA.ID+"/history/sync", "{}")
	}()
	select {
	case <-importer.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight sync never reached the importer")
	}

	revokeResult := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		revokeResult <- moduleRequest(t, handler, http.MethodPost, "/api/hosts/"+hostA.ID+"/history", `{"enabled":false}`)
	}()
	select {
	case revoke := <-revokeResult:
		if revoke.Code != http.StatusOK {
			t.Fatalf("revoke response: %d %s", revoke.Code, revoke.Body.String())
		}
	case <-time.After(2 * time.Second):
		close(importer.release)
		<-syncResult
		<-revokeResult
		t.Fatal("revoke waited for the in-flight sync instead of cancelling it")
	}

	// Re-enable before the old importer returns: the old generation must still be
	// invalid and must not publish after the quick revoke/regrant pair.
	grantHistory(t, handler, hostA.ID)
	close(importer.release)
	if response := <-syncResult; response.Code != http.StatusConflict {
		t.Fatalf("cancelled sync status = %d, want 409: %s", response.Code, response.Body.String())
	}
	status := moduleRequest(t, handler, http.MethodGet, "/api/hosts/"+hostA.ID+"/history", "")
	var after hostHistoryStatus
	if status.Code != http.StatusOK || json.Unmarshal(status.Body.Bytes(), &after) != nil || !after.Enabled || after.State != "ready" || after.Files != 0 || after.LastSync != "" {
		t.Fatalf("cancelled generation changed the record: %d %s", status.Code, status.Body.String())
	}
	record, err := module.server.readHistoryRecord(hostA.ID)
	if err != nil || len(record.Files) != 0 || record.LastSync != "" {
		t.Fatalf("cancelled generation saved a receipt/last_sync: %+v %v", record, err)
	}
	blockedB, err := module.server.readHistoryRecord(hostB.ID)
	if err != nil || !blockedB.Enabled || len(blockedB.Files) != 0 {
		t.Fatalf("host B consent was disturbed by A revocation: %+v %v", blockedB, err)
	}

	// A fresh generation after the explicit regrant is allowed to import and publish.
	imported := moduleRequest(t, handler, http.MethodPost, "/api/hosts/"+hostA.ID+"/history/sync", "{}")
	if imported.Code != http.StatusOK {
		t.Fatalf("post-regrant sync: %d %s", imported.Code, imported.Body.String())
	}
	var final hostHistoryStatus
	if json.Unmarshal(imported.Body.Bytes(), &final) != nil || final.Files != 1 || final.LastSync == "" {
		t.Fatalf("post-regrant sync did not publish its own receipt: %+v", final)
	}
}

func TestHostHistorySyncsSerializePerHost(t *testing.T) {
	module, _ := newTestModule(t)
	importer := &gateHistoryImporter{entered: make(chan string, 4), release: make(chan struct{})}
	module.SetHistoryImporter(importer)
	host, _ := addHistoryFixtureHost(t, module, "history-serialize")
	handler := module.Handler()
	grantHistory(t, handler, host.ID)

	first := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		first <- moduleRequest(t, handler, http.MethodPost, "/api/hosts/"+host.ID+"/history/sync", "{}")
	}()
	select {
	case <-importer.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first sync never reached the importer")
	}
	second := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		second <- moduleRequest(t, handler, http.MethodPost, "/api/hosts/"+host.ID+"/history/sync", "{}")
	}()
	time.Sleep(200 * time.Millisecond)
	if calls := importer.callCount(); calls != 1 {
		t.Fatalf("second sync entered the importer while the first was in flight: calls=%d", calls)
	}
	close(importer.release)
	firstResponse, secondResponse := <-first, <-second
	if firstResponse.Code != http.StatusOK || secondResponse.Code != http.StatusOK {
		t.Fatalf("serialized syncs: first=%d second=%d", firstResponse.Code, secondResponse.Code)
	}
	var final hostHistoryStatus
	if json.Unmarshal(secondResponse.Body.Bytes(), &final) != nil || final.Files != 1 || final.LastSync == "" {
		t.Fatalf("serialized sync lost the receipt: %+v", final)
	}
}
