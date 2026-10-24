// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"context"
	"database/sql"
	"net/http"
	"sync"

	"workbench/internal/harness"
	"workbench/internal/hosts"
)

const (
	defaultPageLimit = 50
	maxPageLimit     = 100
	maxScanFiles     = 5000
	maxHTTPBody      = 2 << 20
	maxTaskTitle     = 240
	maxFeedback      = 2000
	maxCitationQuote = 2000
)

// Options bind Foundry to the canonical host scope and local agent engines.
// Foundry persists only in its own private SQLite database below StateDir.
type Options struct {
	StateDir         string
	HostScope        hosts.Service
	AgentEngines     *harness.Engines
	ProjectRoots     func() []string
	RemoteMemuRunner func(context.Context, string, string, string, string, string) error
}

// Module owns the Foundry schema, explicit conversation sources, immutable
// transcript revisions, human task decisions, and asynchronous scan jobs.
type Module struct {
	db               *sql.DB
	stateDir         string
	hostScope        hosts.Service
	agents           *harness.Engines
	projectRoots     func() []string
	remoteMemuRunner func(context.Context, string, string, string, string, string) error
	taskWake         chan struct{}
	captureWake      chan struct{}

	mu        sync.Mutex
	memuMu    sync.Mutex
	captureMu sync.Mutex
	jobs      map[string]context.CancelFunc
	closed    bool
	closeDone chan struct{}
	closeErr  error
	closeCtx  context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

// Handler returns the full-path HTTP surface so the root application can mount
// it without stripping route prefixes.
func (m *Module) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			writeAPIError(w, &APIError{Status: http.StatusServiceUnavailable, Code: "closed", Message: "Foundry se está cerrando."})
			return
		}
		m.wg.Add(1)
		m.mu.Unlock()
		defer m.wg.Done()
		m.serveHTTP(w, r)
	})
}

// CoverageSummary is the compact, revision-specific integrity and coverage view.
type CoverageSummary struct {
	Status             string  `json:"status"`
	EventCount         int     `json:"event_count"`
	IssueCount         int     `json:"issue_count"`
	Issues             []Issue `json:"issues,omitempty"`
	BlockingIssueCount int     `json:"blocking_issue_count"`
	SemanticIssueCount int     `json:"semantic_issue_count"`
	Bytes              int64   `json:"bytes"`
	ActivityAt         float64 `json:"activity_at,omitempty"`
	NormalizerVersion  string  `json:"normalizer_version,omitempty"`
	RedactionVersion   string  `json:"redaction_version,omitempty"`
	RawSHA256          string  `json:"raw_sha256,omitempty"`
	AppendPending      bool    `json:"append_pending,omitempty"`
}

type ScanResult struct {
	Files   int `json:"files"`
	Changed int `json:"changed"`
	// Sessions counts conversations retained by this scan, not files or events.
	Sessions    int             `json:"sessions"`
	Errors      int             `json:"errors"`
	MaxFiles    int             `json:"max_files"`
	CurrentFile string          `json:"current_file,omitempty"`
	Limited     bool            `json:"limited"`
	Coverage    CoverageSummary `json:"coverage"`
}

type LastScan struct {
	Status     string  `json:"status"`
	Files      int     `json:"files"`
	Changed    int     `json:"changed"`
	Errors     int     `json:"errors"`
	Limited    bool    `json:"limited"`
	FinishedAt float64 `json:"finished_at,omitempty"`
}

type SourceDTO struct {
	ID          string          `json:"id"`
	Path        string          `json:"path"`
	Adapter     Adapter         `json:"adapter"`
	Enabled     bool            `json:"enabled"`
	Created     float64         `json:"created"`
	LastCapture *float64        `json:"last_capture,omitempty"`
	LastScanAt  *float64        `json:"last_scan_at,omitempty"`
	Coverage    CoverageSummary `json:"coverage"`
	Error       string          `json:"error,omitempty"`
	HostID      string          `json:"host_id,omitempty"`
	HostName    string          `json:"host_name,omitempty"`
	LastScan    LastScan        `json:"last_scan"`
}

type ConversationDTO struct {
	Project    string          `json:"project,omitempty"`
	ID         string          `json:"id"`
	SourceID   string          `json:"source_id"`
	Path       string          `json:"path"`
	Title      string          `json:"title"`
	Revision   string          `json:"revision"`
	Updated    float64         `json:"updated"`
	CapturedAt float64         `json:"captured_at"`
	ActivityAt float64         `json:"activity_at"`
	Completion string          `json:"completion"`
	Coverage   CoverageSummary `json:"coverage"`
	TaskCount  int             `json:"task_count"`
	HostID     string          `json:"host_id,omitempty"`
	HostName   string          `json:"host_name,omitempty"`
	Adapter    Adapter         `json:"adapter"`
}

type TaskCitation struct {
	EventIndex int    `json:"event_index"`
	Quote      string `json:"quote"`
}

// Un caso vinculado a una tarea, como lo nombra la pantalla; sólo lo publican rutas de tarea.
type TaskCaseRef struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Domain string `json:"domain"`
	Split  string `json:"split"`
}

type TaskDTO struct {
	ID                    string          `json:"id"`
	SessionID             string          `json:"session_id"`
	Revision              string          `json:"revision"`
	Title                 string          `json:"title"`
	FirstEvent            int             `json:"first_event"`
	LastEvent             int             `json:"last_event"`
	Label                 string          `json:"label"`
	Feedback              string          `json:"feedback"`
	Status                string          `json:"status"`
	Citations             []TaskCitation  `json:"citations"`
	Completed             bool            `json:"completed"`
	CompletionGeneration  int64           `json:"completion_generation"`
	CompletionConfirmedAt *float64        `json:"completion_confirmed_at,omitempty"`
	Created               float64         `json:"created"`
	Updated               float64         `json:"updated"`
	Coverage              CoverageSummary `json:"coverage"`
	SourceID              string          `json:"source_id"`
	Cases                 []TaskCaseRef   `json:"cases,omitempty"`
	CasesGeneration       *int64          `json:"cases_generation,omitempty"`
	HostID                string          `json:"-"`
}

// One completion transition of a task, exactly as the append-only record keeps it: the
// generation it opened, whether it confirmed or reopened the task, and which generation a
// reopen revoked.
type TaskCompletionEntry struct {
	Generation        int64    `json:"generation"`
	Completed         bool     `json:"completed"`
	ConfirmedAt       *float64 `json:"confirmed_at,omitempty"`
	RevokedGeneration *int64   `json:"revoked_generation,omitempty"`
	Created           float64  `json:"created"`
}

// One label or feedback write of a task: the generation it belongs to, what the operator
// wrote and the note of that same change.
type TaskLabelEntry struct {
	Generation int64   `json:"generation"`
	Action     string  `json:"action"`
	Label      string  `json:"label"`
	Feedback   string  `json:"feedback"`
	Created    float64 `json:"created"`
}

type ConversationEvent struct {
	EventIndex         int    `json:"event_index"`
	ID                 string `json:"id"`
	Role               string `json:"role"`
	Kind               string `json:"kind"`
	Text               string `json:"text"`
	EventTextTruncated bool   `json:"event_text_truncated,omitempty"`
	Timestamp          any    `json:"timestamp,omitempty"`
	Channel            string `json:"channel,omitempty"`
	ToolName           string `json:"tool_name,omitempty"`
	CallID             string `json:"call_id,omitempty"`
	Attachments        []any  `json:"attachments"`
	Inert              bool   `json:"inert"`
}

type RevisionDTO struct {
	Revision string          `json:"revision"`
	Created  float64         `json:"created"`
	Coverage CoverageSummary `json:"coverage"`
}

type ScanJobDTO struct {
	JobID    string      `json:"job_id"`
	Kind     string      `json:"kind"`
	Subject  string      `json:"subject"`
	Status   string      `json:"status"`
	Progress ScanResult  `json:"progress"`
	Result   *ScanResult `json:"result,omitempty"`
	Error    string      `json:"error,omitempty"`
	Created  float64     `json:"created"`
	Updated  float64     `json:"updated"`
}

// TaskProviderConfig is the local process adapter for semantic task analysis or replay.
type TaskProviderConfig struct {
	Kind          string   `json:"kind"`
	Command       []string `json:"command"`
	Model         string   `json:"model"`
	Timeout       float64  `json:"timeout"`
	RealInference bool     `json:"real_inference"`
	Provider      string   `json:"provider,omitempty"`
	Reasoning     string   `json:"reasoning,omitempty"`
}

type ProviderDiagnostics struct {
	Kind      string   `json:"kind"`
	Command   []string `json:"command"`
	Model     string   `json:"model"`
	Provider  string   `json:"provider,omitempty"`
	Reasoning string   `json:"reasoning,omitempty"`
	Timeout   float64  `json:"timeout"`
	Available bool     `json:"available"`
	Reason    string   `json:"reason,omitempty"`
}

type TaskConfig struct {
	AutoTasks         bool               `json:"auto_tasks"`
	AutoDerive        bool               `json:"auto_derive"`
	AutoEvaluate      bool               `json:"auto_evaluate"`
	AutoPromote       bool               `json:"auto_promote"`
	PollSeconds       int                `json:"poll_seconds"`
	TaskIdleSeconds   int                `json:"task_idle_seconds"`
	MaxInputChars     int                `json:"max_input_chars"`
	Repetitions       int                `json:"repetitions"`
	MaxCalls          int                `json:"max_calls"`
	ExperimentSeconds float64            `json:"experiment_seconds"`
	MinQualityGain    float64            `json:"min_quality_gain"`
	MaxTimeRatio      float64            `json:"max_time_ratio"`
	MinEfficiencyGain float64            `json:"min_efficiency_gain"`
	MaxSourceBytes    int64              `json:"max_source_bytes"`
	MaxFilesPerScan   int                `json:"max_files_per_scan"`
	MaxScanSeconds    int                `json:"max_scan_seconds"`
	Provider          TaskProviderConfig `json:"provider"`
	Replay            TaskProviderConfig `json:"replay"`
}

type TaskConfigResponse struct {
	Config      TaskConfig            `json:"config"`
	Diagnostics TaskConfigDiagnostics `json:"diagnostics"`
}

type TaskConfigDiagnostics struct {
	Provider ProviderDiagnostics `json:"provider"`
	Replay   ProviderDiagnostics `json:"replay"`
	Ready    bool                `json:"ready"`
	Reason   string              `json:"reason,omitempty"`
}

type APIError struct {
	Code    string `json:"code"`
	Message string `json:"error"`
	Status  int    `json:"-"`
}
