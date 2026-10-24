/** A project folder of this installation. The server resolves and validates the
 *  path; the browser only proposes a name derived from it. */
interface ProjectDTO {
  path: string;
  name: string;
}

/** Canonical settings of the frozen platform contract. Every field is optional:
 *  an absent field means "the installation did not report it", never a default. */
export interface SettingsDTO {
  projects?: ProjectDTO[];
}

/** Boot state of the instance: mirrors GET /api/bootstrap of the Go server
 *  (`application`, `status`, `modules` where each module reports "mounted" or
 *  "not_implemented", and `settings`). `status` is what the installation reports
 *  about itself: "ok", "partial" while ported routes are still missing, or
 *  "degraded" when a mounted module is unhealthy. Unknown JSON stays `unknown`. */
export interface BootstrapDTO {
  application?: string;
  /** Release identity this installation reports; absent means it did not report one. */
  version?: string;
  status?: string;
  modules?: Record<string, string>;
  /** Feature gaps this installation still reports as unported. */
  routes_not_ported?: string[];
  settings?: SettingsDTO;
  /** Top-level address of the original SIM this installation serves. Absent when the
   *  installation did not publish one: the shell then says so instead of inventing a
   *  panel, and the SIM screen is the only consumer. */
  sim_url?: string;
  /** Whether the server process can reach an SSH agent (SSH_AUTH_SOCK). */
  ssh_agent?: boolean;
}

/** One bounded lifecycle line; `detail` never carries credentials or raw output. */
interface ModuleEventDTO {
  time?: string;
  event?: string;
  detail?: string;
}

/** Real state of the module this installation manages (its own child process):
 *  "ready", "unready", "stopped" or "unavailable" as the server probed it. */
export interface ManagedModuleDTO {
  id?: string;
  state?: string;
  healthy?: boolean;
  detail?: string;
  pid?: number;
  started_at?: string;
  ready_at?: string;
  stopped_at?: string;
  probes?: number;
  probe_failures?: number;
  events?: ModuleEventDTO[];
}

/** Authenticated answer of GET /api/diagnostics: mount states, unported gaps and
 *  the managed module's real state plus bounded log. */
export interface DiagnosticsDTO {
  status?: string;
  modules?: Record<string, string>;
  routes_not_ported?: string[];
  native_module?: ManagedModuleDTO;
}

/** Client-side failures carry a stable `code` so screens can translate them at render
 *  time; server errors keep the server's own message and, when it sends one, its `code`. */
export type ApiErrorCode =
  | "connection"
  | "invalid_response"
  | "identity_changed"
  | "unreachable_hosts";

export class ApiError extends Error {
  readonly status: number;
  readonly code?: string;
  constructor(message: string, status: number, code?: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
  }
}

/** Spanish source text per client-side code; `translateError` maps it through i18n. */
export const API_ERROR_MESSAGES: Record<ApiErrorCode, string> = {
  connection: "No se pudo conectar con el servicio. Reintenta en unos segundos.",
  invalid_response: "El servicio no devolvió una respuesta válida.",
  identity_changed:
    "La identidad del host cambió; revisa su conexión antes de continuar.",
  unreachable_hosts: "No se pudieron consultar los hosts SSH.",
};

/** Message of a failure for display: client-side codes go through `translate`, anything
 *  else (server text) is shown as it came. */
export function translateError(
  error: unknown,
  translate: (text: string) => string,
  fallback = "",
) {
  if (error instanceof ApiError && error.code && error.code in API_ERROR_MESSAGES)
    return translate(API_ERROR_MESSAGES[error.code as ApiErrorCode]);
  if (error instanceof Error) return error.message;
  return fallback;
}

function errorCode(body: unknown) {
  if (body === null || typeof body !== "object" || !("code" in body)) return undefined;
  const { code } = body;
  return typeof code === "string" ? code : undefined;
}

function errorMessage(body: unknown, fallback: string) {
  if (body === null || typeof body !== "object" || !("error" in body))
    return fallback;
  const { error } = body;
  return typeof error === "string" ? error : fallback;
}

/** Same-origin call that resolves with the status and parsed body of any answered
 *  request; only a connection failure or a non-JSON body rejects. */
export async function apiResponse<T = unknown>(
  path: string,
  options: { method?: string; data?: unknown; signal?: AbortSignal } = {},
): Promise<{ status: number; ok: boolean; body: T }> {
  const { method = options.data === undefined ? "GET" : "POST", data, signal } =
    options;
  const response = await fetch(path, {
    method,
    signal,
    headers: data === undefined ? undefined : { "Content-Type": "application/json" },
    body: data === undefined ? undefined : JSON.stringify(data),
  }).catch((error: unknown) => {
    if (error instanceof TypeError && !signal?.aborted)
      throw new ApiError(API_ERROR_MESSAGES.connection, 0, "connection");
    throw error;
  });
  let body: unknown;
  try {
    body = await response.json();
  } catch {
    throw new ApiError(
      API_ERROR_MESSAGES.invalid_response,
      response.status,
      "invalid_response",
    );
  }
  return { status: response.status, ok: response.ok, body: body as T };
}

/** Same-origin call to the Workbench platform API (/api/auth, /api/bootstrap, /api/logout). */
export async function api<T = unknown>(
  path: string,
  options: { method?: string; data?: unknown; signal?: AbortSignal } = {},
): Promise<T> {
  const { status, ok, body } = await apiResponse<T>(path, options);
  if (!ok)
    throw new ApiError(
      errorMessage(body, `Error ${status}`),
      status,
      errorCode(body),
    );
  return body;
}

/** Canonical settings write: POST /api/settings with a partial body, answering the
 *  same bootstrap the GET returns. No PUT and no GET /api/settings exist. */
export function postSettings(patch: SettingsDTO) {
  return api<BootstrapDTO>("/api/settings", { data: patch });
}

/** GET /api/diagnostics: re-probes the managed module, restarts nothing. */
export function fetchDiagnostics(signal?: AbortSignal) {
  return api<DiagnosticsDTO>("/api/diagnostics", { signal });
}
