// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"context"
	"testing"
)

func TestRevokedSourceStopsTaskProposalAndAcceptance(t *testing.T) {
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
	if _, err := module.db.Exec(`INSERT INTO sources(id,path,adapter,enabled,created) VALUES('source-revoked','/consented/new/source','canonical',0,?)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := module.db.Exec(`INSERT INTO sessions(id,source_id,path,title,revision,updated,captured_at,activity_at,coverage_json) VALUES('session-revoked','source-revoked','/consented/new/session.jsonl','Fixture','rev1',?,?,?,?)`, now, now, now, `{"status":"complete","event_count":1,"raw_sha256":"fixture"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := module.db.Exec(`INSERT INTO jobs(id,kind,subject,status,created,updated) VALUES('proposal-revoked','propose_tasks','session-revoked','running',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	cfg, err := module.loadTaskConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := module.proposalFence(context.Background(), "proposal-revoked", "session-revoked", "rev1", hashJSON(cfg.Provider), false); err == nil || err.Error() != "source revoked" {
		t.Fatalf("proposal fence error = %v, want source revoked", err)
	}
	if _, apiErr := module.enqueueTaskProposal(context.Background(), "session-revoked", "rev1", false); apiErr == nil || apiErr.Code != "source_revoked" {
		t.Fatalf("manual proposal admission error = %+v, want source_revoked", apiErr)
	}
	if _, err := module.db.Exec(`INSERT INTO task_proposal_receipts(job_id,session_id,revision,algorithm,provider_hash,identity_hash,automatic,status,attempt,started_at,lease_until,coverage_json,inference_json,created,updated) VALUES('proposal-completed-revoked','session-revoked','rev1','semantic-tasks-v1','p','i',0,'completed','attempt',?,?,?, ?,?,?)`,
		now, now, `{"status":"complete","event_count":1}`, `{"calls":0,"seconds":0,"runs":[]}`, now, now); err != nil {
		t.Fatal(err)
	}
	_, _, apiErr := module.acceptTaskSuggestions(context.Background(), "proposal-completed-revoked", "rev1", []int{0})
	if apiErr == nil || apiErr.Code != "source_revoked" {
		t.Fatalf("acceptance error = %+v, want source_revoked", apiErr)
	}
}
