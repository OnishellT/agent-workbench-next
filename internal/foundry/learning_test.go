// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestLearningChunkingPreservesEveryUnicodeTextAndMetadataCharacter(t *testing.T) {
	text := strings.Repeat("á🧭step-", 220)
	event := learningEvent{Index: 17, Text: text, Value: map[string]any{"event_index": 17, "role": "assistant", "kind": "tool_result", "tool_name": "shell", "attachments": []any{map[string]any{"name": "report.txt", "inert": true}, map[string]any{"kind": "image", "mime_type": "image/png"}}, "text": text}}
	metadata := map[string]any{}
	for key, value := range event.Value {
		if key != "text" {
			metadata[key] = value
		}
	}
	wantMetadata := mustJSON(metadata)
	chunks, receipts, err := chunkLearningEvidence([]learningEvent{event}, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 2 {
		t.Fatalf("large evidence should be partitioned, observed %d chunk(s)", len(chunks))
	}
	if len(receipts) != 1 || receipts[0]["event_index"] != 17 {
		t.Fatalf("missing event receipt: %+v", receipts)
	}
	textFragments, metadataFragments := []map[string]any{}, []map[string]any{}
	for _, chunk := range chunks {
		if got := utf8.RuneCountInString(chunk.Prompt); got > 1000 {
			t.Fatalf("chunk prompt has %d runes, limit 1000", got)
		}
		for _, fragment := range chunk.Fragments {
			switch fragment["part"] {
			case "text":
				textFragments = append(textFragments, fragment)
			case "metadata":
				metadataFragments = append(metadataFragments, fragment)
			default:
				t.Fatalf("unknown fragment part: %+v", fragment)
			}
		}
	}
	join := func(fragments []map[string]any, key string) string {
		sort.Slice(fragments, func(i, j int) bool { return fragments[i]["start"].(int) < fragments[j]["start"].(int) })
		var out strings.Builder
		next := 0
		for _, fragment := range fragments {
			if fragment["start"].(int) != next {
				t.Fatalf("fragment gap/overlap at %d, next %d", fragment["start"].(int), next)
			}
			value := fragment[key].(string)
			out.WriteString(value)
			next = fragment["end"].(int)
		}
		return out.String()
	}
	if got := join(textFragments, "text"); got != text {
		t.Fatal("text fragments did not reconstruct the exact Unicode event")
	}
	if got := join(metadataFragments, "data"); got != wantMetadata {
		t.Fatal("metadata fragments did not reconstruct the exact full event metadata")
	}
}

func TestLearningSpecRequiresExactEvidenceSafePathsAndValidSkillFrontmatter(t *testing.T) {
	events := []learningEvent{{Index: 4, Text: "Keep the user's exact wording in the final summary.", Value: map[string]any{"text": "Keep the user's exact wording in the final summary."}}}
	spec := map[string]any{"name": "retain-precise-summary", "description": "Retain precise summaries", "kind": "skill", "scope": "domain", "domain": "writing", "positive": map[string]any{"instructions": "Quote the exact user wording.", "files": []any{}}, "preventive": map[string]any{"instructions": "Do not generalize beyond the evidence.", "files": []any{}}, "evidence": []any{map[string]any{"event_index": 4, "quote": "Keep the user's exact wording"}}, "replaces": []any{}, "rationale": "The cited instruction is explicit."}
	validated, err := validateLearningSpec(spec, events, map[string]map[string]any{})
	if err != nil {
		t.Fatalf("valid minimal skill rejected: %v", err)
	}
	for _, key := range []string{"positive", "preventive"} {
		variant := validated[key].(map[string]any)
		files := variant["files"].([]any)
		if len(files) != 1 || files[0].(map[string]any)["path"] != "SKILL.md" {
			t.Fatalf("missing generated SKILL.md for %s: %+v", key, files)
		}
	}
	badCitation := map[string]any{}
	for key, value := range spec {
		badCitation[key] = value
	}
	badCitation["evidence"] = []any{map[string]any{"event_index": 4, "quote": "not an exact quote"}}
	if _, err := validateLearningSpec(badCitation, events, map[string]map[string]any{}); err == nil {
		t.Fatal("citation not present in the original event was accepted")
	}
	badPath := map[string]any{}
	for key, value := range spec {
		badPath[key] = value
	}
	badPath["positive"] = map[string]any{"instructions": "Use exact wording.", "files": []any{map[string]any{"path": "../escape.md", "content": "data"}}}
	if _, err := validateLearningSpec(badPath, events, map[string]map[string]any{}); err == nil {
		t.Fatal("parent-traversal file path was accepted")
	}
	if err := validateSkillFrontmatter("---\nname: wrong-name\ndescription: usable description\n---\nbody\n", "expected-name"); err == nil {
		t.Fatal("mismatched skill frontmatter name was accepted")
	}
}

func TestLearningDeriveRejectsUnfinishedUnlabeledAndRevokedTasksBeforeProvider(t *testing.T) {
	root := t.TempDir()
	m, server := newFoundryTestServer(t, filepath.Join(root, "state"), nil)
	sourceRoot := filepath.Join(root, "conversations")
	if err := os.MkdirAll(sourceRoot, 0700); err != nil {
		t.Fatal(err)
	}
	writeJSONLFixture(t, filepath.Join(sourceRoot, "learning-gates.jsonl"), map[string]any{"session_id": "learning-gates", "role": "user", "kind": "message", "text": "Keep this exact evidence in a human-reviewed task."}, map[string]any{"session_id": "learning-gates", "role": "assistant", "kind": "message", "text": "No work is performed automatically."})
	status, body := callFoundryJSON(t, server.Client(), server.URL, http.MethodPost, "/api/conversations/sources", map[string]any{"path": sourceRoot, "adapter": "canonical"})
	if status != http.StatusCreated {
		t.Fatalf("register source status=%d body=%+v", status, body)
	}
	source := foundryObject(t, body["source"])
	sourceID := source["id"].(string)
	queueFoundryScan(t, server.Client(), server.URL, sourceID)
	status, body = callFoundryJSON(t, server.Client(), server.URL, http.MethodGet, "/api/conversations?source_id="+sourceID, nil)
	if status != http.StatusOK {
		t.Fatalf("list conversations status=%d body=%+v", status, body)
	}
	sessions := foundryList(t, body["sessions"])
	if len(sessions) != 1 {
		t.Fatalf("expected one fixture conversation, got %+v", sessions)
	}
	session := foundryObject(t, sessions[0])
	sessionID, revision := session["id"].(string), session["revision"].(string)
	status, body = callFoundryJSON(t, server.Client(), server.URL, http.MethodPost, "/api/tasks", map[string]any{"session_id": sessionID, "revision": revision, "first_event": 0, "last_event": 1, "title": "Review exact evidence", "citations": []any{map[string]any{"event_index": 0, "quote": "Keep this exact evidence"}}})
	if status != http.StatusCreated {
		t.Fatalf("create task status=%d body=%+v", status, body)
	}
	taskID := foundryObject(t, body["task"])["id"].(string)
	status, body = callFoundryJSON(t, server.Client(), server.URL, http.MethodPost, "/api/foundry/tasks/"+taskID+"/derive", map[string]any{})
	if status != http.StatusGone {
		t.Fatalf("legacy derive route status=%d body=%+v, want 410", status, body)
	}
	foundryErrorCode(t, body, "legacy_inference_disabled")
	if _, apiErr := m.enqueueLearningDerive(t.Context(), taskID); apiErr == nil || apiErr.Code != "task_incomplete" {
		t.Fatalf("unfinished task internal derive = %+v", apiErr)
	}
	status, body = callFoundryJSON(t, server.Client(), server.URL, http.MethodPost, "/api/tasks/"+taskID+"/completion", map[string]any{"completed": true, "expected_generation": 0})
	if status != http.StatusOK {
		t.Fatalf("complete task status=%d body=%+v", status, body)
	}
	if _, apiErr := m.enqueueLearningDerive(t.Context(), taskID); apiErr == nil || apiErr.Code != "task_unlabeled" {
		t.Fatalf("unlabeled task internal derive = %+v", apiErr)
	}
	status, body = callFoundryJSON(t, server.Client(), server.URL, http.MethodPost, "/api/tasks/"+taskID+"/label", map[string]any{"label": "correct", "feedback": "Human reviewed", "expected_generation": 1})
	if status != http.StatusOK {
		t.Fatalf("label task status=%d body=%+v", status, body)
	}
	status, body = callFoundryJSON(t, server.Client(), server.URL, http.MethodPatch, "/api/conversations/sources/"+sourceID, map[string]any{"enabled": false})
	if status != http.StatusOK {
		t.Fatalf("disable source status=%d body=%+v", status, body)
	}
	if _, apiErr := m.enqueueLearningDerive(t.Context(), taskID); apiErr == nil || apiErr.Code != "source_revoked" {
		t.Fatalf("revoked task internal derive = %+v", apiErr)
	}
	jobs, err := server.Client().Get(server.URL + "/api/foundry/learning/state?compact=1")
	if err != nil {
		t.Fatal(err)
	}
	defer jobs.Body.Close()
	var state map[string]any
	if err := json.NewDecoder(jobs.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	for _, raw := range foundryList(t, state["jobs"]) {
		if foundryObject(t, raw)["kind"] == "derive" {
			t.Fatalf("gate failures must not create derive jobs: %+v", state["jobs"])
		}
	}
}

func TestLearningTaskAndSourceChangesAppendStaleCandidateHistory(t *testing.T) {
	root := t.TempDir()
	m, server := newFoundryTestServer(t, filepath.Join(root, "state"), nil)
	sourceRoot := filepath.Join(root, "conversations")
	if err := os.MkdirAll(sourceRoot, 0700); err != nil {
		t.Fatal(err)
	}
	writeJSONLFixture(t, filepath.Join(sourceRoot, "session.jsonl"), map[string]any{"session_id": "invalidation-source", "role": "user", "kind": "message", "text": "A human completed and labeled this source-bound task."})
	status, body := callFoundryJSON(t, server.Client(), server.URL, http.MethodPost, "/api/conversations/sources", map[string]any{"path": sourceRoot, "adapter": "canonical"})
	if status != http.StatusCreated {
		t.Fatalf("register source status=%d body=%+v", status, body)
	}
	sourceID := foundryObject(t, body["source"])["id"].(string)
	queueFoundryScan(t, server.Client(), server.URL, sourceID)
	status, body = callFoundryJSON(t, server.Client(), server.URL, http.MethodGet, "/api/conversations?source_id="+sourceID, nil)
	if status != http.StatusOK {
		t.Fatalf("list conversations status=%d body=%+v", status, body)
	}
	session := foundryObject(t, foundryList(t, body["sessions"])[0])
	status, body = callFoundryJSON(t, server.Client(), server.URL, http.MethodPost, "/api/tasks", map[string]any{"session_id": session["id"], "revision": session["revision"], "first_event": 0, "last_event": 0, "title": "Review source-bound task", "citations": []any{map[string]any{"event_index": 0, "quote": "A human completed and labeled"}}})
	if status != http.StatusCreated {
		t.Fatalf("create task status=%d body=%+v", status, body)
	}
	taskID := foundryObject(t, body["task"])["id"].(string)
	status, body = callFoundryJSON(t, server.Client(), server.URL, http.MethodPost, "/api/tasks/"+taskID+"/completion", map[string]any{"completed": true, "expected_generation": 0})
	if status != http.StatusOK {
		t.Fatalf("complete task status=%d body=%+v", status, body)
	}
	status, body = callFoundryJSON(t, server.Client(), server.URL, http.MethodPost, "/api/tasks/"+taskID+"/label", map[string]any{"label": "correct", "feedback": "Human checked", "expected_generation": 1})
	if status != http.StatusOK {
		t.Fatalf("label task status=%d body=%+v", status, body)
	}
	seedCandidate := func(id string) {
		t.Helper()
		spec := `{"name":"reviewed-example"}`
		now := nowSeconds()
		if _, err := m.db.Exec(`INSERT INTO candidates(id,task_id,spec_json,status,base_version,created) VALUES(?,?,?,'proposed','base-v1',?)`, id, taskID, spec, now); err != nil {
			t.Fatal(err)
		}
		if _, err := m.db.Exec(`INSERT INTO candidate_history(candidate_id,action,before_hash,after_hash,detail_json,created) VALUES(?,'created','',?,?,?)`, id, hashJSON(map[string]any{"status": "proposed"}), `{}`, now); err != nil {
			t.Fatal(err)
		}
		if _, err := m.db.Exec(`INSERT INTO experiments(id,candidate_id,base_version,report_json,status,created,updated) VALUES(?,?,'base-v1','{}','running',?,?)`, "experiment-"+id, id, now, now); err != nil {
			t.Fatal(err)
		}
	}
	assertStale := func(id string) {
		t.Helper()
		var status string
		if err := m.db.QueryRow(`SELECT status FROM candidates WHERE id=?`, id).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status != "stale" {
			t.Fatalf("candidate %s status=%q, want stale", id, status)
		}
		var actionCount int
		if err := m.db.QueryRow(`SELECT COUNT(*) FROM candidate_history WHERE candidate_id=?`, id).Scan(&actionCount); err != nil {
			t.Fatal(err)
		}
		if actionCount != 2 {
			t.Fatalf("candidate %s history rows=%d, want created+stale", id, actionCount)
		}
		var experimentStatus string
		if err := m.db.QueryRow(`SELECT status FROM experiments WHERE candidate_id=?`, id).Scan(&experimentStatus); err != nil {
			t.Fatal(err)
		}
		if experimentStatus != "stale" {
			t.Fatalf("candidate %s experiment status=%q, want stale", id, experimentStatus)
		}
	}
	seedCandidate("candidate-label-change")
	status, body = callFoundryJSON(t, server.Client(), server.URL, http.MethodPost, "/api/tasks/"+taskID+"/label", map[string]any{"label": "incorrect", "feedback": "Human correction", "expected_generation": 1})
	if status != http.StatusOK {
		t.Fatalf("revise human label status=%d body=%+v", status, body)
	}
	assertStale("candidate-label-change")
	seedCandidate("candidate-source-revocation")
	status, body = callFoundryJSON(t, server.Client(), server.URL, http.MethodPatch, "/api/conversations/sources/"+sourceID, map[string]any{"enabled": false})
	if status != http.StatusOK {
		t.Fatalf("disable source status=%d body=%+v", status, body)
	}
	assertStale("candidate-source-revocation")
}

func TestLearningReadinessSelectionOriginAndCandidateArchive(t *testing.T) {
	root := t.TempDir()
	m, server := newFoundryTestServer(t, filepath.Join(root, "state"), nil)
	t.Cleanup(func() {
		snapshotRoot := filepath.Join(m.stateDir, "foundry", "snapshots")
		_ = filepath.WalkDir(snapshotRoot, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if entry.IsDir() {
				_ = os.Chmod(path, 0700)
			} else if entry.Type().IsRegular() {
				_ = os.Chmod(path, 0600)
			}
			return nil
		})
	})
	client := server.Client()
	sourceRoot := filepath.Join(root, "conversations")
	if err := os.MkdirAll(sourceRoot, 0700); err != nil {
		t.Fatal(err)
	}
	writeJSONLFixture(t, filepath.Join(sourceRoot, "session.jsonl"), map[string]any{"session_id": "learning-ui-contract", "role": "user", "kind": "message", "text": "Preserve this exact human-reviewed behavior."})
	status, body := callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/conversations/sources", map[string]any{"path": sourceRoot, "adapter": "canonical"})
	if status != http.StatusCreated {
		t.Fatalf("register source status=%d body=%+v", status, body)
	}
	sourceID := foundryObject(t, body["source"])["id"].(string)
	queueFoundryScan(t, client, server.URL, sourceID)
	status, body = callFoundryJSON(t, client, server.URL, http.MethodGet, "/api/conversations?source_id="+sourceID, nil)
	if status != http.StatusOK {
		t.Fatalf("list conversations status=%d body=%+v", status, body)
	}
	session := foundryObject(t, foundryList(t, body["sessions"])[0])
	status, body = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/tasks", map[string]any{"session_id": session["id"], "revision": session["revision"], "first_event": 0, "last_event": 0, "title": "Preserve reviewed behavior", "citations": []any{map[string]any{"event_index": 0, "quote": "Preserve this exact human-reviewed behavior"}}})
	if status != http.StatusCreated {
		t.Fatalf("create task status=%d body=%+v", status, body)
	}
	taskID := foundryObject(t, body["task"])["id"].(string)
	status, body = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/tasks/"+taskID+"/completion", map[string]any{"completed": true, "expected_generation": 0})
	if status != http.StatusOK {
		t.Fatalf("complete task status=%d body=%+v", status, body)
	}
	status, body = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/tasks/"+taskID+"/label", map[string]any{"label": "correct", "feedback": "Reviewed", "expected_generation": 1})
	if status != http.StatusOK {
		t.Fatalf("label task status=%d body=%+v", status, body)
	}
	task, err := loadLearningTask(context.Background(), m.db, taskID)
	if err != nil {
		t.Fatal(err)
	}
	seedCase := func(id, split, prompt, content string, withControlReceipts bool) {
		t.Helper()
		folder := filepath.Join(m.stateDir, "foundry", "snapshots", id)
		project, verifier := filepath.Join(folder, "project"), filepath.Join(folder, "verifier")
		for _, dir := range []string{project, verifier} {
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(project, "README.md"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(verifier, "verify.py"), []byte("# frozen fixture\n"), 0600); err != nil {
			t.Fatal(err)
		}
		projectHash, err := learningTreeHash(project)
		if err != nil {
			t.Fatal(err)
		}
		verifierHash, err := learningTreeHash(verifier)
		if err != nil {
			t.Fatal(err)
		}
		manifest := map[string]any{"schema": 1, "directory": "snapshots/" + id, "project_key": hashJSON("new-consented-project"), "project_hash": projectHash, "verifier_hash": verifierHash}
		if withControlReceipts {
			manifest["controls"] = []any{map[string]any{"phase": "positive_verify", "status": "pass"}, map[string]any{"phase": "negative_verify", "status": "fail"}}
		}
		_, err = m.db.Exec("INSERT INTO cases(id,title,prompt,snapshot_json,verify_json,positive_control_json,negative_control_json,domain,split,created) VALUES(?,?,?,?,?,?,?,?,?,?)", id, id, prompt, mustJSON(manifest), mustJSON([]string{"python3", "-c", "exit(0)"}), mustJSON([]string{}), mustJSON([]string{"python3", "-c", "exit(0)"}), "writing", split, nowSeconds())
		if err != nil {
			t.Fatal(err)
		}
	}
	seedCase("case-learning-origin", "origin", "Write a concise result using the original requirement.", "origin snapshot", true)
	seedCase("case-learning-holdout", "holdout", "Summarize a different request while preserving its constraints.", "holdout snapshot", true)
	seedCase("case-learning-unverified-origin", "origin", "Write a different concise result without control receipts.", "unverified origin snapshot", false)
	fakeReplay := filepath.Join(root, "fake-replay.sh")
	fakeOutput := "{\"text\":\"fixture response\",\"model\":\"fake-AUX\",\"usage\":{\"input_tokens\":4,\"output_tokens\":3},\"turns\":1,\"tool_calls\":0,\"real_inference\":false}"
	fakeScript := "#!/bin/sh\ncat >/dev/null\nprintf '%s\\n' '" + fakeOutput + "'\n"
	if err := os.WriteFile(fakeReplay, []byte(fakeScript), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := m.db.Exec("UPDATE task_provider_configs SET kind='command',command_json=?,model='fixture',timeout_seconds=60,real_inference=0 WHERE role='replay'", mustJSON([]string{fakeReplay})); err != nil {
		t.Fatal(err)
	}
	if _, err := m.db.Exec("UPDATE task_settings SET repetitions=1,max_calls=64 WHERE singleton=1"); err != nil {
		t.Fatal(err)
	}
	spec := map[string]any{"name": "preserve-reviewed-behavior", "description": "Preserve only evidenced constraints", "kind": "skill", "scope": "project", "domain": "writing", "positive": map[string]any{"instructions": "Retain the exact user requirement.", "files": []any{}}, "preventive": map[string]any{"instructions": "Do not generalize beyond the cited evidence.", "files": []any{}}, "evidence": []any{map[string]any{"event_index": 0, "quote": "Preserve this exact human-reviewed behavior"}}, "replaces": []any{}, "rationale": "The request states this directly.", "completion_generation": strconv.FormatInt(task.CompletionGeneration, 10), "episode_binding": learningBindingHash(task)}
	candidateID := "candidate-learning-ui-contract"
	if _, err := m.db.Exec(`INSERT INTO candidates(id,task_id,spec_json,status,base_version,created) VALUES(?,?,?,'proposed','initial',?)`, candidateID, taskID, mustJSON(spec), nowSeconds()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.db.Exec(`INSERT INTO candidate_history(candidate_id,action,before_hash,after_hash,detail_json,created) VALUES(?,'created','',?,?,?)`, candidateID, hashJSON(spec), `{}`, nowSeconds()); err != nil {
		t.Fatal(err)
	}
	status, body = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/foundry/candidates/"+candidateID+"/scope", map[string]any{"scope": "project", "domain": "writing"})
	if status != http.StatusOK {
		t.Fatalf("set candidate scope status=%d body=%+v", status, body)
	}
	status, body = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/foundry/candidates/"+candidateID+"/setup", map[string]any{"case_ids": []string{"case-learning-unverified-origin", "case-learning-holdout"}})
	if status != http.StatusOK {
		t.Fatalf("select cases status=%d body=%+v", status, body)
	}
	readiness := foundryObject(t, body["readiness"])
	foundControlsBlocker := false
	for _, rawBlocker := range foundryList(t, readiness["blockers"]) {
		if foundryObject(t, rawBlocker)["code"] == "controls_unverified" {
			foundControlsBlocker = true
		}
	}
	if readiness["ready"] != false || !foundControlsBlocker {
		t.Fatalf("unverified control receipts must block readiness: %+v", readiness)
	}
	status, body = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/foundry/tasks/"+taskID+"/origin", map[string]any{"case_id": "case-learning-unverified-origin", "confirmed_initial_state": true, "note": "This new fixture was explicitly prepared from the task's initial state."})
	if status != http.StatusConflict {
		t.Fatalf("origin attestation without verified controls must fail closed, status=%d body=%+v", status, body)
	}
	foundryErrorCode(t, body, "origin_controls_unverified")
	status, body = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/foundry/candidates/"+candidateID+"/setup", map[string]any{"case_ids": []string{"case-learning-origin", "case-learning-holdout"}})
	if status != http.StatusOK {
		t.Fatalf("reselect cases with verified control receipts status=%d body=%+v", status, body)
	}
	readiness = foundryObject(t, body["readiness"])
	if readiness["ready"] != true || len(foundryList(t, readiness["blockers"])) != 0 {
		t.Fatalf("real snapshots and verified controls should pass readiness, got %+v", readiness)
	}
	status, body = callFoundryJSON(t, client, server.URL, http.MethodGet, "/api/foundry/candidates/"+candidateID+"/readiness?deep=1", nil)
	if status != http.StatusOK {
		t.Fatalf("deep readiness status=%d body=%+v", status, body)
	}
	if foundryObject(t, body)["integrity_checked"] != true {
		t.Fatalf("deep readiness did not report snapshot integrity check: %+v", body)
	}
	status, body = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/foundry/candidates/"+candidateID+"/evaluate", map[string]any{})
	if status != http.StatusGone {
		t.Fatalf("retired evaluation = %d %+v, want 410", status, body)
	}
	foundryErrorCode(t, body, "legacy_inference_disabled")
	var experiments int
	if err := m.db.QueryRow("SELECT COUNT(*) FROM experiments WHERE candidate_id=?", candidateID).Scan(&experiments); err != nil {
		t.Fatal(err)
	}
	if experiments != 0 {
		t.Fatal("retired evaluation created an experiment")
	}
	status, body = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/foundry/tasks/"+taskID+"/origin", map[string]any{"case_id": "case-learning-origin", "confirmed_initial_state": true, "note": "This new fixture was explicitly prepared from the task's initial state."})
	if status != http.StatusOK {
		t.Fatalf("confirm origin status=%d body=%+v", status, body)
	}
	if foundryObject(t, body)["promotion_eligible"] != true {
		t.Fatalf("human origin attestation not reflected: %+v", body)
	}
	status, body = callFoundryJSON(t, client, server.URL, http.MethodGet, "/api/foundry/candidates/"+candidateID, nil)
	if status != http.StatusOK {
		t.Fatalf("candidate detail status=%d body=%+v", status, body)
	}
	detail := foundryObject(t, body)
	if foundryObject(t, detail["origin"])["case_id"] != "case-learning-origin" {
		t.Fatalf("candidate detail omitted persisted origin: %+v", detail)
	}
	response, err := client.Get(server.URL + "/api/foundry/candidates/" + candidateID + "/files")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	archiveBytes, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("candidate archive expected 200 application/zip, got %d %q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	zipReader, err := zip.NewReader(bytes.NewReader(archiveBytes), int64(len(archiveBytes)))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, file := range zipReader.File {
		names[file.Name] = true
	}
	if !names["manifest.json"] || !names["positive/instructions.md"] || !names["preventive/instructions.md"] {
		t.Fatalf("candidate archive incomplete: %v", names)
	}
}

func TestLearningGitSnapshotRejectsOptionRefsAndPreservesTrackedArchiveNames(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is unavailable")
	}
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0700); err != nil {
		t.Fatal(err)
	}
	gitHome := filepath.Join(root, "git-home")
	if err := os.MkdirAll(gitHome, 0700); err != nil {
		t.Fatal(err)
	}
	gitEnv := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + gitHome, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_TERMINAL_PROMPT=0"}
	runGit := func(args ...string) []byte {
		t.Helper()
		cmd := exec.Command(git, args...)
		cmd.Dir = repo
		cmd.Env = gitEnv
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v: %s", args, err, out)
		}
		return out
	}
	runGit("init", "-q")
	runGit("config", "user.name", "foundry fixture")
	runGit("config", "user.email", "foundry-fixture@example.invalid")
	want := []byte("tracked project file named source.tar; must survive archive creation")
	if err := os.WriteFile(filepath.Join(repo, "source.tar"), want, 0600); err != nil {
		t.Fatal(err)
	}
	runGit("add", "--", "source.tar")
	runGit("commit", "-q", "-m", "fixture")
	commit := strings.TrimSpace(string(runGit("rev-parse", "HEAD")))
	destination := filepath.Join(root, "case", "project")
	if err := os.MkdirAll(destination, 0700); err != nil {
		t.Fatal(err)
	}
	resolved, coverage, err := learningGitSnapshot(context.Background(), repo, destination, commit, nil)
	if err != nil || resolved != commit {
		t.Fatalf("valid commit snapshot failed: resolved=%q err=%v", resolved, err)
	}
	if coverage.Files != 1 || coverage.Bytes != int64(len(want)) {
		t.Fatalf("unexpected git snapshot coverage: %+v", coverage)
	}
	got, err := os.ReadFile(filepath.Join(destination, "source.tar"))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("tracked source.tar was lost/corrupted by archive staging: got=%q err=%v", got, err)
	}
	if _, _, err := learningGitSnapshot(context.Background(), repo, destination, "--help", nil); err == nil {
		t.Fatal("Git option-like ref unexpectedly resolved as a valid commit")
	}
	entries, err := os.ReadDir(filepath.Dir(destination))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".foundry-source-") {
			t.Fatalf("temporary git archive leaked after snapshot: %s", entry.Name())
		}
	}
}
