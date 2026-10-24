// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type exampleCheck struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Expected string `json:"expected"`
	Observed string `json:"observed"`
}

type exampleResult struct {
	Status    string         `json:"status"`
	SessionID string         `json:"session_id,omitempty"`
	TaskID    string         `json:"task_id,omitempty"`
	Checks    []exampleCheck `json:"checks"`
	Error     string         `json:"error,omitempty"`
}

func (m *Module) exampleStatus(w http.ResponseWriter, r *http.Request) {
	if err := m.db.PingContext(r.Context()); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "reason": "sqlite_unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"available": true, "reason": "fixture_runs_through_instance_store_and_http_handlers"})
}

func (m *Module) runExample(w http.ResponseWriter, r *http.Request) {
	if r.Body != nil && r.ContentLength != 0 {
		var request struct{}
		if apiErr := decodeBody(w, r, &request); apiErr != nil {
			writeAPIError(w, apiErr)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	runID, err := newID("example_")
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	// The run serves itself over a private loopback listener, so it needs its own capability:
	// another local client must not reach this store through that port.
	capability, err := newID("cap_")
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	sourceRoot := filepath.Join(m.stateDir, "foundry", "examples", runID, "conversations")
	if err := os.MkdirAll(sourceRoot, 0700); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	result := runEmbeddedExample(ctx, m, sourceRoot, runID, capability)
	status := http.StatusOK
	if result.Status != "succeeded" {
		status = http.StatusInternalServerError
	}
	writeJSON(w, status, result)
}

/** The capability of one run: every request of that private HTTP run carries it, and a
 *  request without it is refused before touching the store. */
func exampleGuard(next http.Handler, capability string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Example-Capability")), []byte(capability)) != 1 {
			writeAPIError(w, &APIError{Status: http.StatusUnauthorized, Code: "example_capability_required", Message: "La ejecución interna del ejemplo exige su propia capacidad."})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func runEmbeddedExample(ctx context.Context, module *Module, sourceRoot, sessionID, capability string) (result exampleResult) {
	result = exampleResult{Status: "failed", Checks: make([]exampleCheck, 0, 12)}
	check := func(name, expected, observed string) {
		state := "passed"
		if expected != observed {
			state = "failed"
		}
		result.Checks = append(result.Checks, exampleCheck{Name: name, Status: state, Expected: expected, Observed: observed})
	}
	fixture := strings.Join([]string{
		`{"session_id":"` + sessionID + `","role":"user","kind":"message","text":"Please inspect the sample conversation and identify one concrete task."}`,
		`{"session_id":"` + sessionID + `","role":"assistant","kind":"message","text":"I inspected the sample conversation; no work was performed automatically."}`,
	}, "\n") + "\n"
	fixturePath := filepath.Join(sourceRoot, sessionID+".jsonl")
	if err := os.WriteFile(fixturePath, []byte(fixture), 0600); err != nil {
		result.Error = err.Error()
		return result
	}
	server := httptest.NewServer(exampleGuard(module.Handler(), capability))
	defer server.Close()
	client := server.Client()
	var err error

	call := func(method, path string, body any) (int, map[string]any, error) {
		var encoded []byte
		if body != nil {
			encoded, err = json.Marshal(body)
			if err != nil {
				return 0, nil, err
			}
		}
		request, err := http.NewRequestWithContext(ctx, method, server.URL+path, strings.NewReader(string(encoded)))
		if err != nil {
			return 0, nil, err
		}
		request.Header.Set("X-Example-Capability", capability)
		if body != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		response, err := client.Do(request)
		if err != nil {
			return 0, nil, err
		}
		defer response.Body.Close()
		var data map[string]any
		if err := json.NewDecoder(response.Body).Decode(&data); err != nil {
			return response.StatusCode, nil, err
		}
		return response.StatusCode, data, nil
	}
	code, data, err := call(http.MethodPost, "/api/conversations/sources", map[string]any{"path": sourceRoot, "adapter": "canonical"})
	if err != nil {
		result.Error = err.Error()
		return result
	}
	observed := fmt.Sprint(code)
	check("explicit_source_registration", "201", observed)
	if code != http.StatusCreated {
		result.Error = fmt.Sprint(data["error"])
		return result
	}
	source, ok := data["source"].(map[string]any)
	if !ok {
		result.Error = "source DTO missing"
		return result
	}
	sourceID := fmt.Sprint(source["id"])
	code, data, err = call(http.MethodPost, "/api/conversations/sources/"+sourceID+"/scan", map[string]any{})
	if err != nil {
		result.Error = err.Error()
		return result
	}
	check("async_scan_queue", "202", fmt.Sprint(code))
	if code != http.StatusAccepted {
		result.Error = fmt.Sprint(data["error"])
		return result
	}
	jobID := fmt.Sprint(data["job_id"])
	jobStatus := ""
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, job, err := call(http.MethodGet, "/api/foundry/jobs/"+jobID, nil)
		if err != nil {
			result.Error = err.Error()
			return result
		}
		jobStatus = fmt.Sprint(job["status"])
		if jobStatus == "succeeded" || jobStatus == "failed" || jobStatus == "cancelled" {
			break
		}
		select {
		case <-ctx.Done():
			result.Error = ctx.Err().Error()
			return result
		case <-time.After(10 * time.Millisecond):
		}
	}
	check("scan_job_terminal", "succeeded", jobStatus)
	if jobStatus != "succeeded" {
		result.Error = "fixture scan did not succeed"
		return result
	}
	code, data, err = call(http.MethodGet, "/api/conversations?source_id="+sourceID, nil)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if code != http.StatusOK {
		result.Error = fmt.Sprint(data["error"])
		return result
	}
	sessions, ok := data["sessions"].([]any)
	if !ok || len(sessions) != 1 {
		check("captured_session", "1", fmt.Sprint(len(sessions)))
		result.Error = "fixture did not create one conversation"
		return result
	}
	session, ok := sessions[0].(map[string]any)
	if !ok {
		result.Error = "conversation DTO missing"
		return result
	}
	result.SessionID = fmt.Sprint(session["id"])
	revision := fmt.Sprint(session["revision"])
	coverage, _ := session["coverage"].(map[string]any)
	status := fmt.Sprint(coverage["status"])
	check("complete_revision_coverage", "complete", status)
	code, detail, err := call(http.MethodGet, "/api/conversations/"+result.SessionID+"/revisions/"+revision+"?limit=100", nil)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if code != http.StatusOK {
		result.Error = fmt.Sprint(detail["error"])
		return result
	}
	events, _ := detail["events"].([]any)
	check("two_inert_events", "2", fmt.Sprint(len(events)))
	code, taskResponse, err := call(http.MethodPost, "/api/tasks", map[string]any{"session_id": result.SessionID, "revision": revision, "first_event": 0, "last_event": 1, "title": "Inspect sample conversation", "citations": []any{map[string]any{"event_index": 0, "quote": "Please inspect the sample conversation"}}})
	if err != nil {
		result.Error = err.Error()
		return result
	}
	check("manual_task_and_exact_citation", "201", fmt.Sprint(code))
	if code != http.StatusCreated {
		result.Error = fmt.Sprint(taskResponse["error"])
		return result
	}
	task, ok := taskResponse["task"].(map[string]any)
	if !ok {
		result.Error = "task DTO missing"
		return result
	}
	result.TaskID = fmt.Sprint(task["id"])
	code, sessionDetail, err := call(http.MethodGet, "/api/conversations/"+result.SessionID, nil)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	conversation, _ := sessionDetail["conversation"].(map[string]any)
	check("session_same_instance_store", result.SessionID, fmt.Sprint(conversation["id"]))
	if code != http.StatusOK || fmt.Sprint(conversation["id"]) != result.SessionID {
		result.Error = "fixture session is not addressable in the instance store"
		return result
	}
	code, taskDetail, err := call(http.MethodGet, "/api/tasks/"+result.TaskID, nil)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	check("task_same_instance_store", result.TaskID, fmt.Sprint(taskDetail["id"]))
	if code != http.StatusOK || fmt.Sprint(taskDetail["id"]) != result.TaskID {
		result.Error = "fixture task is not addressable in the instance store"
		return result
	}
	// The example never completes, values or reopens the task: those are human decisions, so
	// the run only states that the new task is waiting for them and that its history is empty.
	code, taskDetail, err = call(http.MethodGet, "/api/tasks/"+result.TaskID, nil)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	observed = fmt.Sprintf("%v/%v/%v", taskDetail["status"], taskDetail["label"], taskDetail["completion_generation"])
	check("task_awaits_human_decision", "incomplete/pending/0", observed)
	if code != http.StatusOK {
		result.Error = fmt.Sprint(taskDetail["error"])
		return result
	}
	code, taskHistory, err := call(http.MethodGet, "/api/tasks/"+result.TaskID+"/history", nil)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	completions, _ := taskHistory["completions"].([]any)
	labels, _ := taskHistory["labels"].([]any)
	observed = fmt.Sprintf("%d/%d", len(completions), len(labels))
	check("task_history_has_no_human_decision", "0/0", observed)
	if code != http.StatusOK {
		result.Error = fmt.Sprint(taskHistory["error"])
		return result
	}
	var passed = true
	for _, item := range result.Checks {
		if item.Status != "passed" {
			passed = false
		}
	}
	if passed {
		result.Status = "succeeded"
	} else {
		result.Error = "one or more embedded HTTP checks failed"
	}
	return result
}
