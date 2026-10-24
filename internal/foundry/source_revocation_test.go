// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDisablingSourceCancelsActiveProposalWorker(t *testing.T) {
	module, err := New(Options{StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := module.Close(context.Background()); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()
	now := nowSeconds()
	if _, err := module.db.Exec(`INSERT INTO sources(id,path,adapter,enabled,created) VALUES('source-live','/new/consented/source','canonical',1,?)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := module.db.Exec(`INSERT INTO sessions(id,source_id,path,title,revision,updated,captured_at,activity_at,coverage_json) VALUES('session-live','source-live','/new/consented/session.jsonl','Fixture','rev1',?,?,?,?)`, now, now, now, `{"status":"complete","event_count":1}`); err != nil {
		t.Fatal(err)
	}
	if _, err := module.db.Exec(`INSERT INTO jobs(id,kind,subject,status,created,updated) VALUES('proposal-live','propose_tasks','session-live','running',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := module.db.Exec(`INSERT INTO task_proposal_receipts(job_id,session_id,revision,algorithm,provider_hash,identity_hash,automatic,status,attempt,started_at,lease_until,created,updated) VALUES('proposal-live','session-live','rev1','semantic-tasks-v1','p','i',0,'running','attempt',?,?,?,?)`, now, now, now, now); err != nil {
		t.Fatal(err)
	}
	workerCtx, cancelWorker := context.WithCancel(context.Background())
	module.mu.Lock()
	module.jobs["proposal-live"] = cancelWorker
	module.mu.Unlock()

	request := httptest.NewRequest(http.MethodPatch, "/api/conversations/sources/source-live", bytes.NewBufferString(`{"enabled":false}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	module.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("disable source status=%d body=%s", response.Code, response.Body.String())
	}
	var receipt map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt["revoked"] != true {
		t.Fatalf("source mutation receipt=%+v, want revoked=true", receipt)
	}
	select {
	case <-workerCtx.Done():
	default:
		t.Fatal("source revocation did not cancel the active proposal context")
	}
}
