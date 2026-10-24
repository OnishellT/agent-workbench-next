import { useEffect, useRef, useState } from "react";
import { api, apiResponse, ApiError } from "@/app/api";
import type { Language } from "@/app/language";
import {
  CONVERSATIONS_API,
  EXAMPLE_API,
  FOUNDRY_API,
  TASKS_API,
  conversationQuery,
  path,
  type ConversationFilters,
} from "./domain";
import { translate } from "./i18n";
import type {
  ConversationDetailDTO,
  ConversationsDTO,
  DiscoverDTO,
  DiscoveredSourceDTO,
  ExampleRunDTO,
  ExampleStateDTO,
  JobCancelDTO,
  JobDTO,
  RevisionPageDTO,
  ScanQueuedDTO,
  SourceReceiptDTO,
  SourcesDTO,
  ProposalAcceptReceiptDTO,
  ProposalQueuedDTO,
  TaskCaseRefDTO,
  TaskConfigEnvelopeDTO,
  TaskConfigPatchDTO,
  TaskCreateDTO,
  TaskDTO,
  TaskHistoryDTO,
  TaskProposalDTO,
  TaskReceiptDTO,
  TasksDTO,
} from "./types";

/** Reads one route of this installation with the browser session cookie and one abort
 *  per unmount: a screen never keeps a stale answer after a mutation or a change of
 *  revision. The loader must be stable (bind the ids with useCallback). */
export function useResource<T>(load: (signal: AbortSignal) => Promise<T>) {
  const [revision, setRevision] = useState(0);
  const [state, setState] = useState<{
    data?: T;
    error?: unknown;
    loading: boolean;
  }>({
    loading: true,
  });
  const lastLoad = useRef(load);
  useEffect(() => {
    const controller = new AbortController();
    // Same loader (a manual reload): keep the previous data while it is in flight. A new
    // loader means another resource, whose answer must not show the old one.
    const sameResource = lastLoad.current === load;
    lastLoad.current = load;
    setState((previous) =>
      sameResource ? { data: previous.data, loading: true } : { loading: true },
    );
    load(controller.signal).then(
      (data) => {
        if (!controller.signal.aborted) setState({ data, loading: false });
      },
      (error: unknown) => {
        if (!controller.signal.aborted)
          setState((previous) => ({ data: previous.data, error, loading: false }));
      },
    );
    return () => controller.abort();
  }, [load, revision]);
  return { ...state, reload: () => setRevision((value) => value + 1) };
}

/** A route this release does not serve yet (the product's own 501 or a 404 on a
 *  control route) is stated as a gap, never rendered as an empty list. */
export function notServed(error: unknown) {
  return (
    error instanceof ApiError && (error.status === 501 || error.status === 404)
  );
}

/** Conflict the human must resolve by looking at the current state: a moved revision,
 *  a stale completion generation or capture paused. The server message is shown as it
 *  came, and the screen reloads instead of keeping its optimistic value.
 *  shortcut: the shared api() keeps status and message but not the contract `code`, so
 *  every 409 reloads the affected resource; surface codes if a case needs them apart. */
export function conflict(error: unknown) {
  return error instanceof ApiError && error.status === 409;
}

/** What a failed request shows the human: the message the shared api() raised, or the
 *  localized "service did not respond" line for anything that is not an Error. */
export function failureMessage(failure: unknown, language: Language) {
  return failure instanceof Error
    ? failure.message
    : translate("El servicio no respondió.", language);
}

export function fetchConversations(
  filters: ConversationFilters,
  cursor: string | undefined,
  signal?: AbortSignal,
) {
  return api<ConversationsDTO>(
    CONVERSATIONS_API + conversationQuery(filters, cursor),
    { signal },
  );
}

/** The route answers the conversation projection plus its revisions envelope. */
export function fetchConversation(id: string, signal?: AbortSignal) {
  return api<ConversationDetailDTO>(path(CONVERSATIONS_API, [id]), { signal });
}

/** One page of the immutable revision. `compact` keeps the response bounded; the
 *  screen switches it off when the operator wants the full text of the loaded page. */
export function fetchRevision(
  id: string,
  revision: string,
  cursor: string | undefined,
  compact: boolean,
  signal?: AbortSignal,
) {
  const params = new URLSearchParams();
  if (cursor) params.set("cursor", cursor);
  params.set("limit", "100");
  if (compact) params.set("compact", "1");
  return api<RevisionPageDTO>(
    path(
      CONVERSATIONS_API,
      [id, "revisions", revision],
      `?${params.toString()}`,
    ),
    { signal },
  );
}

export function fetchSources(signal?: AbortSignal) {
  return api<SourcesDTO>(path(CONVERSATIONS_API, ["sources"]), { signal }).then(
    (body) => body.sources ?? [],
  );
}

export function addSource(input: { path: string; adapter: string }) {
  return api<SourceReceiptDTO>(path(CONVERSATIONS_API, ["sources"]), {
    data: input,
  });
}

/** Consent of one discovery run: only the roots the operator wrote here are read, and
 *  the run registers nothing. Confirming a candidate is a separate source add. */
export function discoverSources(roots: DiscoveredSourceDTO[]) {
  return api<DiscoverDTO>(path(CONVERSATIONS_API, ["sources", "discover"]), {
    data: { roots },
  });
}

/** Pause/reactivate, relink and re-adapt. The contract accepts exactly one key. */
export function updateSource(
  id: string,
  patch: { enabled: boolean } | { path: string } | { adapter: string },
) {
  return api<SourceReceiptDTO>(path(CONVERSATIONS_API, ["sources", id]), {
    method: "PATCH",
    data: patch,
  });
}

/** A scan is queued, never synchronous: the screen follows the job it got back. */
export function startScan(sourceId?: string) {
  return sourceId
    ? api<ScanQueuedDTO>(
        path(CONVERSATIONS_API, ["sources", sourceId, "scan"]),
        { data: {} },
      )
    : api<ScanQueuedDTO>(path(CONVERSATIONS_API, ["scan"]), { data: {} });
}

export function fetchJob(jobId: string, signal?: AbortSignal) {
  return api<JobDTO>(path(FOUNDRY_API, ["jobs", jobId]), { signal });
}

export function fetchTasks(
  query: {
    status: string;
    session_id?: string;
    source_id?: string;
    q?: string;
  },
  cursor: string | undefined,
  signal?: AbortSignal,
) {
  const params = new URLSearchParams();
  const filters: Record<string, string | undefined> = {
    status: query.status === "all" ? "" : query.status,
    session_id: query.session_id,
    source_id: query.source_id,
    q: query.q,
  };
  for (const [key, value] of Object.entries(filters)) {
    if (value) params.set(key, value);
  }
  if (cursor) params.set("cursor", cursor);
  return api<TasksDTO>(`${TASKS_API}?${params.toString()}`, { signal });
}

export function fetchTask(id: string, signal?: AbortSignal) {
  return api<TaskDTO>(path(TASKS_API, [id]), { signal });
}

/** The append-only record of one task: a detail read, never part of the list. */
export function fetchTaskHistory(id: string, signal?: AbortSignal) {
  return api<TaskHistoryDTO>(path(TASKS_API, [id, "history"]), { signal });
}

/** The human range: the server binds the citations and returns the stored task, so the
 *  screen shows what really got pinned. `expected_generation` never travels here. */
export function createTask(input: {
  session_id: string;
  revision: string;
  first_event: number;
  last_event: number;
  title?: string;
}) {
  return api<TaskCreateDTO>(TASKS_API, { data: input });
}

export function setCompletion(
  id: string,
  completed: boolean,
  expectedGeneration?: number,
) {
  return api<TaskReceiptDTO>(path(TASKS_API, [id, "completion"]), {
    data: { completed, expected_generation: expectedGeneration },
  });
}

export function setLabel(
  id: string,
  label: string,
  feedback: string,
  expectedGeneration?: number,
) {
  return api<TaskReceiptDTO>(path(TASKS_API, [id, "label"]), {
    data: { label, feedback, expected_generation: expectedGeneration },
  });
}

/** Los casos de la instalación: la pantalla elige entre los que ya existen. */
export function fetchCases(signal?: AbortSignal) {
  return api<{ cases?: TaskCaseRefDTO[] }>(`${FOUNDRY_API}/cases`, { signal });
}

/** Selección manual: el servidor escribe la generación y la pantalla relee el vínculo. */
export function setTaskCases(
  id: string,
  caseIds: string[],
  expectedGeneration: number,
) {
  return api<{ ok?: boolean }>(
    `${FOUNDRY_API}/tasks/${encodeURIComponent(id)}/cases`,
    {
      data: { case_ids: caseIds, expected_generation: expectedGeneration },
    },
  );
}

/** Task analysis configuration with its diagnostics: this is the whole consent surface
 *  of the proposals (provider, budget, automatic opt-in) and the reason each analysis
 *  engine is or is not available. */
export function fetchTaskConfig(signal?: AbortSignal) {
  return api<TaskConfigEnvelopeDTO>(path(TASKS_API, ["config"]), { signal });
}

/** Sends only the fields the operator changed; every range is validated by the server. */
export function saveTaskConfig(patch: TaskConfigPatchDTO) {
  return api<TaskConfigEnvelopeDTO>(path(TASKS_API, ["config"]), {
    method: "PATCH",
    data: { config: patch },
  });
}

/** Manual proposal for one immutable revision: 202 with the queued job. */
export function startTaskProposal(sessionId: string, expectedRevision: string) {
  return api<ProposalQueuedDTO>(path(TASKS_API, ["proposals"]), {
    data: { session_id: sessionId, expected_revision: expectedRevision },
  });
}

/** Receipt of the proposal of that revision: progress while it runs, suggestions when it
 *  completed. 404 means that revision has no proposal yet. */
export function fetchTaskProposal(
  sessionId: string,
  revision: string,
  signal?: AbortSignal,
) {
  const params = new URLSearchParams();
  if (revision) params.set("revision", revision);
  return api<TaskProposalDTO>(
    `${path(TASKS_API, ["proposals", sessionId])}?${params.toString()}`,
    { signal },
  );
}

/** Cancelling is a job action; the receipt keeps its terminal state and its receipts. */
export function cancelJob(jobId: string) {
  return api<JobCancelDTO>(path(FOUNDRY_API, ["jobs", jobId, "cancel"]), {
    data: {},
  });
}

/** Explicit acceptance of the suggestions the human selected: nothing is accepted by
 *  default and the server skips a suggestion already represented by another task. */
export function acceptTaskProposal(
  jobId: string,
  expectedRevision: string,
  indices: number[],
) {
  return api<ProposalAcceptReceiptDTO>(
    path(TASKS_API, ["proposals", jobId, "accept"]),
    {
      data: {
        expected_revision: expectedRevision,
        suggestion_indices: indices,
      },
    },
  );
}

/** The example may run in its own isolated fixture store: only an artifact this
 *  installation really serves is offered as a link, and the screen states the isolation
 *  instead of sending the operator to a 404. */
export async function exampleArtifacts(receipt: ExampleRunDTO) {
  const check = (target?: string) =>
    target
      ? api<unknown>(target).then(
          () => true,
          () => false,
        )
      : Promise.resolve(false);
  const [conversation, task] = await Promise.all([
    check(
      receipt.session_id
        ? path(CONVERSATIONS_API, [receipt.session_id])
        : undefined,
    ),
    check(receipt.task_id ? path(TASKS_API, [receipt.task_id]) : undefined),
  ]);
  return { conversation, task };
}

export function fetchExampleState(signal?: AbortSignal) {
  return api<ExampleStateDTO>(EXAMPLE_API, { signal });
}

/** The run answers 200 when it succeeded and 500 with the same receipt when a check
 *  failed: both carry the checks, so the screen shows them instead of a bare status.
 *  A body without checks and with the API error shape is still a rejected call. */
export async function runExample(): Promise<ExampleRunDTO> {
  const { ok, status, body } = await apiResponse<ExampleRunDTO | null>(
    EXAMPLE_API,
    { method: "POST", data: {} },
  );
  // Boundary of this route: the receipt is the body itself, which the screen asserts.
  const receipt = (body && typeof body === "object" ? body : {}) as ExampleRunDTO;
  if (!receipt.checks) {
    if (ok) throw new ApiError(`Error ${status}`, status);
    throw new ApiError(
      typeof receipt.error === "string" && receipt.error
        ? receipt.error
        : `Error ${status}`,
      status,
    );
  }
  return { ...receipt, status: receipt.status || (ok ? "succeeded" : "failed") };
}
