import { localeTag } from "./i18n";
export type Row = Record<string, any>;
export type AppState = {
  config: Row;
  sources: Row[];
  sessions: Row[];
  episodes: Row[];
  candidates: Row[];
  cases: Row[];
  jobs: Row[];
  experiments: Row[];
  versions: Row[];
  active_version: string;
  diagnostics: Row;
  capture_status?: Row;
  total_sessions?: number;
};
const hash = new URLSearchParams(location.hash.slice(1));
if (hash.has("token")) {
  sessionStorage.setItem("foundry-token", hash.get("token") || "");
  history.replaceState(null, "", location.pathname + location.search);
}
export function setToken(token: string) {
  sessionStorage.setItem("foundry-token", token);
}
// Requests resolve against the document URL, not the origin root, so the same build
// works standalone at "/" and behind a path prefix (Workbench serves it at "/foundry/",
// authenticates the session and supplies the token itself).
export function apiFetch(path: string, init: RequestInit = {}) {
  return fetch(path.replace(/^\//, ""), {
    ...init,
    headers: { "X-Foundry-Token": sessionStorage.getItem("foundry-token") || "", ...init.headers },
  });
}
export async function api(path: string, body?: Row) {
  const response = await apiFetch(path, {
    method: body ? "POST" : "GET",
    headers: {
      ...(body ? { "Content-Type": "application/json" } : {}),
    },
    ...(body ? { body: JSON.stringify(body) } : {}),
  });
  let value;
  try {
    value = await response.json();
  } catch {
    throw Error(
      "El servidor no devolvió una respuesta válida. Comprueba el servicio local.",
    );
  }
  if (!response.ok || value.error)
    throw Object.assign(new Error(value.error || `Error ${response.status}`), {
      status: response.status,
    });
  return value;
}
export function date(value: unknown) {
  if (typeof value !== "number" && typeof value !== "string") return "—";
  const text = typeof value === "string" ? value.trim() : "";
  if (typeof value === "string" && !text) return "—";
  const numeric = typeof value === "number" || /^-?\d+(?:\.\d+)?$/.test(text);
  const epoch = numeric ? Number(value) : NaN;
  const timestamp = numeric
    ? epoch > 100_000_000_000 ? epoch : epoch * 1000
    : Date.parse(text);
  if (!Number.isFinite(timestamp) || timestamp <= 0) return "—";
  const instant = new Date(timestamp);
  return Number.isFinite(instant.getTime())
    ? instant.toLocaleString(localeTag(), { dateStyle: "medium", timeStyle: "short" })
    : "—";
}
export const pretty = (v: any) => JSON.stringify(v, null, 2);
export function argv(v: string) {
  let parsed;
  try {
    parsed = JSON.parse(v);
  } catch {
    throw Error(
      'El comando debe ser un array JSON, por ejemplo ["python3", "verify.py"].',
    );
  }
  if (
    !Array.isArray(parsed) ||
    !parsed.length ||
    parsed.some((x) => typeof x !== "string" || !x)
  )
    throw Error("Cada comando necesita una lista de argumentos de texto.");
  return parsed;
}
export function episode(e: Row): Row {
  return { ...e, cases: e.cases || JSON.parse(e.cases_json || "[]") };
}
