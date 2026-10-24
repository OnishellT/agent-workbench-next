/** DTOs of the M05 contract frozen with the backend owner (`internal/foundry`).
 *  Every field is optional except the ones the screens require to identify a row:
 *  an absent field means "the installation did not report it", never a default.
 *  Contract: /api/conversations, /api/tasks, /api/foundry/example (see the module
 *  client for the exact paths). */

/** Capture diagnostics of one source or revision. `status` is what the capture
 *  reported ("complete", "partial", "error"…); an unknown value stays unknown. The
 *  version and digest fields are the provenance of that normalization. */
export interface CoverageDTO {
  status?: string;
  event_count?: number;
  issue_count?: number;
  issues?: CaptureIssueDTO[];
  blocking_issue_count?: number;
  semantic_issue_count?: number;
  bytes?: number;
  activity_at?: number | string;
  normalizer_version?: string;
  redaction_version?: string;
  raw_sha256?: string;
  /** The capture was still appending when this revision was read. */
  append_pending?: boolean;
}

export interface CaptureIssueDTO {
  file_name?: string;
  line?: number;
  reason?: string;
  message?: string;
}

export interface SourceDTO {
  id: string;
  path: string;
  adapter: string;
  enabled: boolean;
  created?: number | string;
  last_capture?: number | string;
  last_scan_at?: number | string;
  last_scan?: ScanProgressDTO & {
    status?: string;
    finished_at?: number | string;
  };
  coverage?: CoverageDTO;
  error?: string;
  host_id?: string;
  project?: string;
  host_name?: string;
}

export interface SourcesDTO {
  sources?: SourceDTO[];
}

export interface SourceReceiptDTO {
  source: SourceDTO;
  /** Evidence the installation revoked when pausing, relinking or re-adapting. */
  revoked?: unknown;
}

export interface DiscoveredSourceDTO {
  path: string;
  adapter: string;
  file_count?: number;
}

interface UnsupportedSourceDTO {
  path: string;
  kind?: string;
  reason?: string;
}

export interface DiscoverDTO {
  candidates?: DiscoveredSourceDTO[];
  unsupported?: UnsupportedSourceDTO[];
  issues?: CaptureIssueDTO[];
}

/** A scan is queued: these are the counters of its live progress, as reported. */
interface ScanProgressDTO {
  files?: number;
  changed?: number;
  errors?: number;
  max_files?: number;
  current_file?: string;
  limited?: boolean;
}

interface ScanResultDTO extends ScanProgressDTO {
  sessions?: number;
  coverage?: CoverageDTO;
}

export interface ScanQueuedDTO {
  job_id: string;
  status: string;
  source_id?: string;
}

type JobStatus =
  "queued" | "running" | "succeeded" | "failed" | "cancelled" | "interrupted";

export interface JobDTO {
  job_id: string;
  kind: string;
  subject?: string;
  status: JobStatus;
  progress?: ScanProgressDTO;
  result?: ScanResultDTO;
  error?: string;
  created?: number | string;
  updated?: number | string;
}

export interface ConversationRowDTO {
  id: string;
  source_id?: string;
  path?: string;
  title?: string;
  revision?: string;
  updated?: number | string;
  captured_at?: number | string;
  activity_at?: number | string;
  completion?: string;
  coverage?: CoverageDTO;
  task_count?: number;
  host_id?: string;
  project?: string;
  host_name?: string;
  adapter?: string;
}

export interface ConversationsDTO {
  sessions?: ConversationRowDTO[];
  total_sessions?: number;
  next_cursor?: string;
}

/** One immutable revision of a conversation, as the installation publishes it. */
export interface RevisionInfoDTO {
  revision: string;
  created?: number | string;
  coverage?: CoverageDTO;
}

interface ConversationDTO extends ConversationRowDTO {}

/** `GET /api/conversations/{id}` answers this envelope: the projection of the
 *  conversation plus the revisions captured for it. */
export interface ConversationDetailDTO {
  conversation?: ConversationDTO;
  revisions?: RevisionInfoDTO[];
}

export interface EventDTO {
  event_index: number;
  id?: string;
  role?: string;
  kind?: string;
  text?: string;
  event_text_truncated?: boolean;
  timestamp?: number | string;
  channel?: string;
  tool_name?: string;
  call_id?: string;
  attachments?: unknown;
  /** The installation flagged this text as inert content (model output): it is
   *  displayed as text and never executed or interpreted. */
  inert?: boolean;
}

interface CitationDTO {
  event_index: number;
  quote: string;
}

/** Exact range a human fixed on one immutable revision (0-based, inclusive). */
export interface TaskDTO {
  id: string;
  session_id: string;
  revision: string;
  title?: string;
  first_event: number;
  last_event: number;
  label?: string;
  /** Derived filter state the list route publishes: "incomplete", "pending",
   *  "correct" or "incorrect". The screen keeps deriving it when absent. */
  status?: string;
  feedback?: string;
  citations?: CitationDTO[];
  completed?: boolean;
  /** Monotonic counter of the completion transition (int64 on the server). */
  completion_generation?: number;
  completion_confirmed_at?: number | string;
  created?: number | string;
  updated?: number | string;
  coverage?: CoverageDTO;
  source_id?: string;
  /** Casos de evaluación vinculados a esta tarea: la selección vigente que publica el
   *  detalle. Un caso creado para esta tarea entra aquí cuando su trabajo se guarda. */
  cases?: TaskCaseRefDTO[];
  /** Generación de esa selección: viaja de vuelta al guardar para no pisar otro cambio. */
  cases_generation?: number;
}

/** Un caso vinculado a la tarea, tal como lo nombra el detalle de la tarea. */
export interface TaskCaseRefDTO {
  id: string;
  title?: string;
  domain?: string;
  split?: string;
}

export interface TasksDTO {
  tasks?: TaskDTO[];
  total_tasks?: number;
  next_cursor?: string;
}

/** One completion transition as the append-only record keeps it. */
interface TaskCompletionEntryDTO {
  generation: number;
  completed: boolean;
  confirmed_at?: number | string;
  revoked_generation?: number;
  created?: number | string;
}

/** One label or feedback write of a task, with the note of that same change. */
interface TaskLabelEntryDTO {
  generation: number;
  action: string;
  label?: string;
  feedback?: string;
  created?: number | string;
}

export interface TaskHistoryDTO {
  task_id: string;
  completions?: TaskCompletionEntryDTO[];
  labels?: TaskLabelEntryDTO[];
}

export interface TaskReceiptDTO {
  task: TaskDTO;
  reused?: boolean;
}

export interface TaskCreateDTO extends TaskReceiptDTO {
  snapshot_is_latest?: boolean;
  title_preserved?: boolean;
}

interface RevisionHeaderDTO {
  id: string;
  source_id?: string;
  title?: string;
  revision: string;
  latest_revision?: string;
  is_latest?: boolean;
  revision_created?: number | string;
  coverage?: CoverageDTO;
  event_count?: number;
  completion?: string;
}

export interface RevisionPageDTO {
  conversation: RevisionHeaderDTO;
  events?: EventDTO[];
  next_cursor?: string;
  historical_task_count?: number;
  tasks?: TaskDTO[];
}

/** Analysis provider/replay of the task proposals (frozen 003 contract). The command is
 *  the real argv the server resolves; `real_inference` is what makes a receipt
 *  verifiable for acceptance. */
export interface TaskProviderConfigDTO {
  kind: string;
  command: string[];
  model: string;
  timeout: number;
  real_inference: boolean;
  provider?: string;
  reasoning?: string;
}

export interface ProviderDiagnosticsDTO {
  kind: string;
  command: string[];
  model: string;
  provider?: string;
  reasoning?: string;
  timeout: number;
  available: boolean;
  reason?: string;
}

export interface TaskConfigDiagnosticsDTO {
  provider: ProviderDiagnosticsDTO;
  replay: ProviderDiagnosticsDTO;
  ready: boolean;
  reason?: string;
}

export interface TaskConfigDTO {
  auto_tasks: boolean;
  task_idle_seconds: number;
  max_input_chars: number;
  max_calls: number;
  experiment_seconds: number;
  provider: TaskProviderConfigDTO;
  replay: TaskProviderConfigDTO;
}

export interface TaskConfigEnvelopeDTO {
  config?: TaskConfigDTO;
  diagnostics?: TaskConfigDiagnosticsDTO;
}

/** Partial patch: the server accepts only these keys and validates each range. */
export interface TaskConfigPatchDTO {
  auto_tasks?: boolean;
  task_idle_seconds?: number;
  max_input_chars?: number;
  max_calls?: number;
  experiment_seconds?: number;
  provider?: Partial<TaskProviderConfigDTO>;
  replay?: Partial<TaskProviderConfigDTO>;
}

interface TaskSuggestionDTO {
  title: string;
  first_event: number;
  last_event: number;
  evidence: CitationDTO[];
}

interface ProposalSkippedDTO {
  first_event: number;
  last_event: number;
  reason: string;
  task_ids?: string[];
}

interface ProposalRunDTO {
  provider: string;
  model: string;
  model_provider?: string;
  status: string;
  error?: string;
  usage?: Record<string, number>;
  seconds: number;
  turns?: number;
  tool_calls?: number;
  real_inference: boolean;
  command: string[];
}

interface ProposalInferenceDTO {
  calls: number;
  seconds: number;
  runs: ProposalRunDTO[];
}

interface ProposalProgressDTO {
  completed_chunks: number;
  total_chunks: number;
}

type ProposalStatus =
  "queued" | "running" | "completed" | "failed" | "cancelled" | "interrupted";

export interface TaskProposalDTO {
  job_id: string;
  session_id: string;
  revision: string;
  algorithm: string;
  automatic: boolean;
  status: ProposalStatus;
  suggestions: TaskSuggestionDTO[];
  skipped: ProposalSkippedDTO[];
  coverage?: CoverageDTO;
  inference: ProposalInferenceDTO;
  progress: ProposalProgressDTO;
  error?: string;
  started_at?: number | string;
  updated_at?: number | string;
  finished_at?: number | string;
}

export interface ProposalQueuedDTO {
  job_id: string;
  status: string;
  automatic: boolean;
}

export interface ProposalAcceptReceiptDTO {
  tasks: TaskDTO[];
  skipped: ProposalSkippedDTO[];
}

export interface JobCancelDTO {
  job_id: string;
  status: string;
  reused: boolean;
}

export interface ExampleStateDTO {
  available: boolean;
  reason?: string;
}

interface ExampleCheckDTO {
  name: string;
  status: string;
  expected?: string;
  observed?: string;
}

export interface ExampleRunDTO {
  status: string;
  session_id?: string;
  task_id?: string;
  checks?: ExampleCheckDTO[];
  error?: string;
}
