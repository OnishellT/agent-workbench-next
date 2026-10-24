import type { Language } from "@/app/language";
import type {
  CoverageDTO,
  EventDTO,
  RevisionInfoDTO,
  TaskConfigDTO,
  TaskConfigPatchDTO,
  TaskDTO,
  TaskProviderConfigDTO,
} from "./types";

/** Routes of this installation: the local instance serves the M05 contract under the
 *  root path, so a section never reaches a host on its own. */
export const CONVERSATIONS_API = "/api/conversations";
export const TASKS_API = "/api/tasks";
export const FOUNDRY_API = "/api/foundry";
export const EXAMPLE_API = `${FOUNDRY_API}/example`;

/** Full path of one route; ids travel encoded and are never interpolated raw. */
export function path(base: string, parts: (string | number)[], tail = "") {
  return base + parts.map((part) => `/${encodeURIComponent(String(part))}`).join("") + tail;
}

/** Views of the Conversaciones section, in the order the module shows them. */
export const conversationViews: [string, string][] = [
  ["conversations", "Conversaciones"],
  ["sources", "Fuentes"],
  ["example", "Ejemplo guiado"],
];
export type ConversationView = (typeof conversationViews)[number][0];

export const tasksViews: [string, string][] = [
  ["tasks", "Tareas"],
  ["proposals", "Propuestas"],
];

export function viewOf(view: string | undefined): ConversationView {
  const found = conversationViews.find(([id]) => id === view);
  return found ? (found[0] as ConversationView) : "conversations";
}

/** Adapters of the frozen normalization contract. A source of another kind is shown
 *  with the raw adapter the installation reported, never renamed to a supported one. */
export const adapters = ["canonical", "codex", "claude", "pi", "omp"] as const;

export interface ConversationFilters {
  q: string;
  source: string;
  adapter: string;
  status: string;
}
export const emptyFilters: ConversationFilters = {
  q: "",
  source: "",
  adapter: "",
  status: "",
};
const filterKeys = Object.keys(emptyFilters) as (keyof ConversationFilters)[];

/** Filters live in the shared query string; section/view/host belong to the shell. */
export function readFilters(value: string | URL): ConversationFilters {
  const url = new URL(value, "http://localhost");
  const filters = { ...emptyFilters };
  for (const key of filterKeys) filters[key] = url.searchParams.get(key) ?? "";
  return filters;
}

export function filtersURL(value: string | URL, filters: ConversationFilters) {
  const url = new URL(value, "http://localhost");
  for (const key of filterKeys) {
    if (filters[key] === emptyFilters[key]) url.searchParams.delete(key);
    else url.searchParams.set(key, filters[key]);
  }
  return url;
}

/** Query of the list route: only the filters the operator set, in the contract names. */
export function conversationQuery(
  filters: ConversationFilters,
  cursor?: string,
  limit = 50,
) {
  const params = new URLSearchParams();
  if (filters.q) params.set("q", filters.q);
  if (filters.source) params.set("source_id", filters.source);
  if (filters.adapter) params.set("adapter", filters.adapter);
  if (filters.status) params.set("status", filters.status);
  params.set("limit", String(limit));
  if (cursor) params.set("cursor", cursor);
  return `?${params.toString()}`;
}

/** Deep link of one conversation revision: a citation can be followed to the exact
 *  revision and event, and a reload keeps the destination. */
export interface Focus {
  conversation: string;
  revision: string;
  event: string;
}
export const emptyFocus: Focus = { conversation: "", revision: "", event: "" };
const focusKeys = Object.keys(emptyFocus) as (keyof Focus)[];

export function readFocus(value: string | URL): Focus {
  const url = new URL(value, "http://localhost");
  const focus = { ...emptyFocus };
  for (const key of focusKeys) focus[key] = url.searchParams.get(key) ?? "";
  return focus;
}

export function focusURL(value: string | URL, focus: Focus) {
  const url = new URL(value, "http://localhost");
  for (const key of focusKeys) {
    if (focus[key]) url.searchParams.set(key, focus[key]);
    else url.searchParams.delete(key);
  }
  return url;
}

/** Inclusive range of a task, 0-based, exactly what the contract binds. */
export interface Range {
  first: number;
  last: number;
}

export function wholeRange(eventCount: number | undefined): Range {
  const count = Number(eventCount) || 0;
  return { first: 0, last: Math.max(0, count - 1) };
}

/** A range is only sendable when it exists inside the selected revision; the message
 *  is the one the screen shows next to the form, so the server never has to guess. */
export function rangeProblem(range: Range, eventCount: number | undefined): string | null {
  if (!Number.isSafeInteger(range.first) || !Number.isSafeInteger(range.last) || range.first < 0)
    return "El inicio y el fin son números enteros de evento; el primero es el 1.";
  if (typeof eventCount !== "number" || eventCount <= 0)
    return "Esta revisión no tiene eventos que se puedan citar.";
  if (range.last < range.first) return "El fin no puede ser anterior al inicio.";
  if (range.last >= eventCount)
    return `Esta revisión tiene ${eventCount} eventos: el fin no puede pasar del ${eventCount}.`;
  return null;
}

/** The range editor works in the 1-based numbering the transcript shows. */
export function toDisplayIndex(index: number) {
  return index + 1;
}
export function fromDisplayIndex(value: number) {
  return value - 1;
}

/** One more page of a cursor list: rows keep their order and never repeat an id. */
export function mergePage<T>(previous: T[], page: T[] | undefined, id: (row: T) => string) {
  const seen = new Set(previous.map(id));
  return [...previous, ...(page ?? []).filter((row) => !seen.has(id(row)))];
}

/** Options of the revision selector of a conversation detail. The route publishes
 *  `{conversation, revisions}` and each revision carries `revision` (never `id`), so this
 *  is the only place that turns them into options: a deep link asking for a revision the
 *  envelope does not list keeps its own option instead of showing an empty selector or
 *  silently switching to another revision. */
export function revisionOptions(
  revisions: RevisionInfoDTO[] | undefined,
  selected: string,
  language: Language,
) {
  const options = (revisions ?? []).map((row) => ({
    value: row.revision,
    label: [
      instant(row.created, language),
      row.revision.slice(0, 8),
      coverageStatusLabels[row.coverage?.status ?? "unknown"] ??
        row.coverage?.status ??
        "sin dato",
    ].join(" · "),
  }));
  if (!selected || options.some((option) => option.value === selected)) return options;
  return [...options, { value: selected, label: selected.slice(0, 8) }];
}

/** Budget and consent ranges the server enforces on PATCH /api/tasks/config. The form
 *  offers exactly these bounds, so a rejected write is an operator mistake no longer. */
export const configRanges = {
  task_idle_seconds: [15, 86400],
  max_input_chars: [1000, 500000],
  max_calls: [2, 200],
  experiment_seconds: [30, 28800],
} as const;

export type ConfigNumberField = keyof typeof configRanges;

/** A number inside its published range, or the reason the field cannot be sent. */
export function configNumberProblem(field: ConfigNumberField, raw: string) {
  const [min, max] = configRanges[field];
  const value = Number(raw);
  if (raw.trim() === "" || !Number.isFinite(value) || !Number.isInteger(value))
    return "Escribe un número entero.";
  if (value < min || value > max) return `El valor debe estar entre ${min} y ${max}.`;
  return null;
}

/** The command is the real argv the server resolves: edited as one line of arguments and
 *  sent as the array it expects. A quoted path is not a DSL this screen invents. */
export function parseCommand(text: string) {
  return text.trim() === "" ? [] : text.trim().split(/\s+/);
}

export function formatCommand(command: string[] | undefined) {
  return (command ?? []).join(" ");
}

/** The model an engine will really run. OMP without an override runs the default
 *  model of this host's Magpie, so the screens never present that as no model. */
export function engineModelLabel(engine: TaskProviderConfigDTO) {
  return engine.model || (engine.kind === "omp" ? "modelo nativo de Magpie" : "sin modelo");
}

export function formatUsage(usage: Record<string, number> | undefined) {
  const rows = Object.entries(usage ?? {});
  return rows.length === 0 ? null : rows.map(([key, value]) => `${key} ${value}`).join(" · ");
}

/** Partial patch: only the fields that really changed travel, so the server's audit and
 *  its provider-changed cancellation only react to a real edit. */
export function taskConfigPatch(
  current: TaskConfigDTO,
  next: TaskConfigDTO,
): TaskConfigPatchDTO {
  const patch: TaskConfigPatchDTO = {};
  if (next.auto_tasks !== current.auto_tasks) patch.auto_tasks = next.auto_tasks;
  for (const field of Object.keys(configRanges) as ConfigNumberField[]) {
    if (next[field] !== current[field]) patch[field] = next[field];
  }
  const provider = providerPatch(current.provider, next.provider);
  if (provider) patch.provider = provider;
  const replay = providerPatch(current.replay, next.replay);
  if (replay) patch.replay = replay;
  return patch;
}

function providerPatch(
  current: TaskProviderConfigDTO,
  next: TaskProviderConfigDTO,
): Partial<TaskProviderConfigDTO> | null {
  const patch: Partial<TaskProviderConfigDTO> = {};
  if (next.kind !== current.kind) patch.kind = next.kind;
  if (formatCommand(next.command) !== formatCommand(current.command))
    patch.command = next.command;
  if (next.model !== current.model) patch.model = next.model;
  if (next.timeout !== current.timeout) patch.timeout = next.timeout;
  if (next.real_inference !== current.real_inference) patch.real_inference = next.real_inference;
  if ((next.provider ?? "") !== (current.provider ?? "")) patch.provider = next.provider ?? "";
  if ((next.reasoning ?? "") !== (current.reasoning ?? ""))
    patch.reasoning = next.reasoning ?? "";
  return Object.keys(patch).length === 0 ? null : patch;
}

/** A proposal is active while the installation still works on it. */
export function proposalActive(status: string) {
  return status === "queued" || status === "running";
}

export const proposalStatusLabels: Record<string, string> = {
  queued: "En cola",
  running: "En curso",
  completed: "Terminada",
  failed: "Falló",
  cancelled: "Cancelada",
  interrupted: "Interrumpida",
};

/** Reason of a skipped suggestion, in the words the review shows. */
export const skippedReasonLabels: Record<string, string> = {
  already_represented: "Ya representada por otra tarea de esta revisión",
};

/** A suggestion is only acceptable with a verifiable real-inference receipt: the screen
 *  says which one is missing instead of offering an acceptance that would fail. */
export function acceptanceProblem(proposal: {
  status: string;
  inference?: { runs?: { real_inference?: boolean }[] };
}) {
  if (proposal.status !== "completed") return "La propuesta todavía no está completa.";
  const runs = proposal.inference?.runs ?? [];
  if (!runs.some((run) => run.real_inference === true))
    return "La propuesta no tiene recibo verificable de inferencia real.";
  return null;
}

/** Transitions of the two human gates, in the words the screen shows. A task is only
 *  "pending" or labelled once it was completed: a new label never reaches it alone. */
export type TaskState = "incomplete" | "pending" | "correct" | "incorrect";

export function taskState(task: TaskDTO): TaskState {
  if (!task.completed) return "incomplete";
  if (task.label === "correct" || task.label === "incorrect") return task.label;
  return "pending";
}

export const taskFilters: [TaskState | "all", string][] = [
  ["all", "Todas"],
  ["incomplete", "Sin completar"],
  ["pending", "Sin valorar"],
  ["correct", "Correctas"],
  ["incorrect", "Incorrectas"],
];

/** Labels the human may set once completion is confirmed. */
export const taskLabels: [TaskState, string][] = [
  ["pending", "Pendiente de valoración"],
  ["correct", "Correcta"],
  ["incorrect", "Incorrecta"],
];

/** What a revision reports about its own capture. A partial or failed capture is
 *  never presented as complete evidence, so the screen states it explicitly. */
export interface CoverageFlags {
  status: string;
  partial: boolean;
  blocking: number;
  semantic: number;
  issues: number;
  events: number | null;
  bytes: number | null;
}

export function coverageFlags(coverage: CoverageDTO | undefined): CoverageFlags {
  const status = coverage?.status ?? "unknown";
  const blocking = Number(coverage?.blocking_issue_count) || 0;
  const semantic = Number(coverage?.semantic_issue_count) || 0;
  const issues = Number(coverage?.issue_count) || 0;
  return {
    status,
    partial: status !== "complete" && status !== "unknown",
    blocking,
    semantic,
    issues,
    events: typeof coverage?.event_count === "number" ? coverage.event_count : null,
    bytes: typeof coverage?.bytes === "number" ? coverage.bytes : null,
  };
}

/** Backend enum values are named through these maps, never as bare translation keys:
 *  a short value like `error` would otherwise be replaced inside other sentences. */
export const coverageStatusLabels: Record<string, string> = {
  complete: "completa",
  partial: "parcial",
  error: "con error",
  unknown: "sin dato",
};

export const jobStatusLabels: Record<string, string> = {
  queued: "En cola",
  running: "En curso",
  succeeded: "Terminada",
  failed: "Falló",
  cancelled: "Cancelada",
  interrupted: "Interrumpida",
};

// Last scan may contain a capture outcome or a job state.
export const scanStatusLabels: Record<string, string> = {
  ...jobStatusLabels,
  complete: "completa",
  partial: "parcial",
};

const locale = (language: Language) => (language === "es" ? "es-DO" : "en-US");

/** Epoch seconds, epoch milliseconds or an ISO string, as the legacy screen accepted
 *  for the same rows; anything absent or invalid stays an em dash, never "now". */
export function instant(value: number | string | undefined, language: Language) {
  if (value === undefined || value === null || value === "") return "—";
  const numeric = typeof value === "number" || /^-?\d+(?:\.\d+)?$/.test(String(value).trim());
  const timestamp = numeric
    ? Number(value) * (Math.abs(Number(value)) > 100_000_000_000 ? 1 : 1000)
    : Date.parse(String(value));
  if (!Number.isFinite(timestamp) || timestamp <= 0) return "—";
  return new Intl.DateTimeFormat(locale(language), {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(timestamp);
}

export function formatBytes(value: number | undefined, language: Language = "es") {
  if (typeof value !== "number" || !Number.isFinite(value) || value < 0) return null;
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let size = value;
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024;
    unit += 1;
  }
  const digits = unit === 0 ? 0 : 1;
  return `${size.toLocaleString(locale(language), {
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
  })} ${units[unit]}`;
}

/** Quote of one pinned citation, cut only for display; the stored text is untouched. */
export function quoteText(text: string | undefined, max = 240) {
  const value = (text ?? "").replace(/\s+/g, " ").trim();
  return value.length > max ? `${value.slice(0, max)}…` : value;
}

/** Event text is always rendered as text: a transcript may contain markup or commands
 *  and this screen never executes, sanitizes into HTML or interprets any of it. */
export function inertNote(event: EventDTO) {
  // `inert` covers human and model events alike in the captured contract, so the note
  // names the content, not a role it may not have.
  if (event.inert) return "Contenido inerte: se muestra como texto y no se ejecuta ni se interpreta.";
  if (event.event_text_truncated)
    return "Vista abreviada · la evidencia completa sigue guardada. Activa el texto completo para leerla aquí.";
  return null;
}

/** Source rows the operator can still act on; a source without an id is not actionable
 *  and the screen keeps it visible but without controls. */
export function sourceName(row: { path?: string; adapter?: string }) {
  return row.path || row.adapter || "—";
}
