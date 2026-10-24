// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"workbench/internal/hosts"
)

type historyHostScope struct {
	rows map[string]hosts.Host
}

func (s *historyHostScope) List(context.Context) ([]hosts.Host, error) {
	out := make([]hosts.Host, 0, len(s.rows))
	for _, host := range s.rows {
		out = append(out, host)
	}
	return out, nil
}

func (s *historyHostScope) Get(_ context.Context, id string) (hosts.Host, error) {
	host, ok := s.rows[id]
	if !ok {
		return hosts.Host{}, errors.New("host not found")
	}
	return host, nil
}

func (s *historyHostScope) Identity(ctx context.Context, id string) (hosts.Identity, error) {
	host, err := s.Get(ctx, id)
	if err != nil {
		return hosts.Identity{}, err
	}
	return host.Identity()
}

func (*historyHostScope) SSHOperation(context.Context, string, hosts.Operation) error {
	return errors.New("SSH not used by Foundry importer test")
}

func historyTestHost(id string) (hosts.Host, hosts.Identity) {
	host := hosts.Host{ID: id, Name: id, Address: "127.0.0.1", User: "fixture", Port: 22, Fingerprint: "SHA256:" + strings.Repeat("A", 43), Revision: 1, Enabled: true}
	identity, _ := host.Identity()
	return host, identity
}

func historyTestRevision() string {
	return base64.RawURLEncoding.EncodeToString(make([]byte, 32))
}

func foundryHistorySessions(t *testing.T, handler http.Handler, hostID string) []ConversationDTO {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/conversations?host_id="+hostID, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("list %s conversations: %d %s", hostID, response.Code, response.Body.String())
	}
	var result struct {
		Sessions []ConversationDTO `json:"sessions"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result.Sessions
}

func TestFoundryRemoteHistoryImportIsNormalizedRevisionedAndHostScoped(t *testing.T) {
	root := t.TempDir()
	hostA, identityA := historyTestHost("host-history-a")
	hostB, identityB := historyTestHost("host-history-b")
	scope := &historyHostScope{rows: map[string]hosts.Host{hostA.ID: hostA, hostB.ID: hostB}}
	module, err := New(Options{StateDir: filepath.Join(root, "state"), HostScope: scope})
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = module.Close(context.Background())
		}
	}()

	revision := historyTestRevision()
	inputA := hosts.HistoryImport{Identity: identityA, SourceID: "host-history-a-codex", SourceRevision: revision, Adapter: "codex", Path: "session.jsonl", Mtime: 1}
	first := []byte("{\"type\":\"item_completed\",\"payload\":{\"item\":{\"id\":\"m1\",\"type\":\"AgentMessage\",\"text\":\"first remote answer\",\"channel\":\"final\"}}}\n")
	result1, err := module.ImportHostConversation(context.Background(), inputA, first)
	if err != nil || !result1.Changed || result1.SessionID == "" || result1.Revision == "" {
		t.Fatalf("first import: %+v %v", result1, err)
	}
	result2, err := module.ImportHostConversation(context.Background(), inputA, first)
	if err != nil || result2.Changed || result2.SessionID != result1.SessionID || result2.Revision != result1.Revision {
		t.Fatalf("unchanged source lost stable identity/revision: first=%+v second=%+v err=%v", result1, result2, err)
	}
	second := []byte("{\"type\":\"item_completed\",\"payload\":{\"item\":{\"id\":\"m1\",\"type\":\"AgentMessage\",\"text\":\"revised remote answer\",\"channel\":\"final\"}}}\n")
	result3, err := module.ImportHostConversation(context.Background(), inputA, second)
	if err != nil || !result3.Changed || result3.SessionID != result1.SessionID || result3.Revision == result1.Revision {
		t.Fatalf("changed source should append one revision to the same conversation: first=%+v third=%+v err=%v", result1, result3, err)
	}
	inputB := inputA
	inputB.Identity = identityB
	inputB.SourceID = "host-history-b-codex"
	resultB, err := module.ImportHostConversation(context.Background(), inputB, first)
	if err != nil || resultB.SessionID == result1.SessionID {
		t.Fatalf("host B reused host A conversation identity: %+v %v", resultB, err)
	}
	if rows := foundryHistorySessions(t, module.Handler(), hostA.ID); len(rows) != 1 || rows[0].ID != result1.SessionID || rows[0].Revision != result3.Revision || rows[0].HostID != hostA.ID || rows[0].Adapter != AdapterCodex {
		t.Fatalf("host A conversation listing: %+v", rows)
	}
	if rows := foundryHistorySessions(t, module.Handler(), hostB.ID); len(rows) != 1 || rows[0].ID != resultB.SessionID || rows[0].HostID != hostB.ID {
		t.Fatalf("host B conversation listing: %+v", rows)
	}
	if err := module.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	closed = true

	module, err = New(Options{StateDir: filepath.Join(root, "state"), HostScope: scope})
	if err != nil {
		t.Fatal(err)
	}
	defer module.Close(context.Background())
	rows := foundryHistorySessions(t, module.Handler(), hostA.ID)
	if len(rows) != 1 || rows[0].ID != result1.SessionID || rows[0].Revision != result3.Revision {
		t.Fatalf("reload did not preserve conversation identity/revision: %+v", rows)
	}
	var eventText string
	var inert int
	if err := module.db.QueryRow("SELECT text,inert FROM events WHERE session_id=? AND revision=? LIMIT 1", result1.SessionID, result3.Revision).Scan(&eventText, &inert); err != nil || eventText != "revised remote answer" || inert != 1 {
		t.Fatalf("Foundry did not persist normalized inert events: text=%q inert=%d err=%v", eventText, inert, err)
	}
}

func TestFoundryRemoteHistoryRejectsEmptyIncompleteAndUnsafeSources(t *testing.T) {
	root := t.TempDir()
	host, identity := historyTestHost("history-validation")
	scope := &historyHostScope{rows: map[string]hosts.Host{host.ID: host}}
	module, err := New(Options{StateDir: filepath.Join(root, "state"), HostScope: scope})
	if err != nil {
		t.Fatal(err)
	}
	defer module.Close(context.Background())
	input := hosts.HistoryImport{Identity: identity, SourceID: "history-validation-codex", SourceRevision: historyTestRevision(), Adapter: "codex", Path: "session.jsonl"}
	for _, raw := range [][]byte{nil, []byte("not-json\n")} {
		if _, err := module.ImportHostConversation(context.Background(), input, raw); !errors.Is(err, hosts.ErrHistoryIncomplete) {
			t.Fatalf("invalid/empty source should not replace evidence, got %v", err)
		}
	}
	unsafe := input
	unsafe.Path = "../auth.jsonl"
	if _, err := module.ImportHostConversation(context.Background(), unsafe, []byte("{}\n")); !errors.Is(err, hosts.ErrHistoryUnsupported) {
		t.Fatalf("unsafe remote path accepted: %v", err)
	}
	tooLarge := input
	if _, err := module.ImportHostConversation(context.Background(), tooLarge, make([]byte, MaxSourceBytes+1)); !errors.Is(err, hosts.ErrHistoryTooLarge) {
		t.Fatalf("oversized remote source accepted: %v", err)
	}
}

var _ hosts.Service = (*historyHostScope)(nil)
