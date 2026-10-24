/** Pages of the managed Conversation Foundry, in the order its own navigation shows them.
 *  The ids are Foundry's page ids (ui/src/App.tsx in third_party/foundry): the shell tab
 *  bar and the framed app address the same page by the same id. */
export const foundryViews: [string, string][] = [
  ["dashboard", "Vista general"],
  ["sources", "Fuentes"],
  ["conversations", "Conversaciones"],
  ["learning", "Aprendizajes"],
  ["experiments", "Experimentos"],
  ["settings", "Configuración"],
];
export type FoundryView = (typeof foundryViews)[number][0];

export function viewOf(view: string | undefined): FoundryView {
  return foundryViews.some(([id]) => id === view) ? (view as FoundryView) : "dashboard";
}

/** The Workbench server proxies Foundry here behind the dashboard session (internal/foundryloop). */
export const FOUNDRY_PATH = "/foundry/";

export type Theme = "light" | "dark";

/** The shell's own theme: it toggles the `dark` class on <html> (app/theme.ts). */
export function currentTheme(): Theme {
  return document.documentElement.classList.contains("dark") ? "dark" : "light";
}

/** Frame address: ?embedded hides Foundry's own sidebar, ?view opens the requested page,
 *  ?theme and ?lang start it in the shell's theme and language (later changes travel as
 *  THEME_MESSAGE and LANG_MESSAGE). */
export function frameSource(view: string, lang: "es" | "en", theme: Theme = currentTheme()): string {
  return `${FOUNDRY_PATH}?embedded=1&view=${encodeURIComponent(viewOf(view))}&theme=${theme}&lang=${lang}`;
}

/** Messages exchanged with the framed app (third_party/foundry/ui/src/main.tsx). */
export const VIEW_MESSAGE = "foundry:view";
export const THEME_MESSAGE = "foundry:theme";
export const LANG_MESSAGE = "foundry:lang";
export const NAVIGATED_MESSAGE = "foundry:navigated";

/** What the proxy answers while Foundry cannot run: `{error, detail}` with 503. */
export interface Availability {
  ok: boolean;
  detail: string;
}

export async function probeFoundry(signal?: AbortSignal): Promise<Availability> {
  try {
    const response = await fetch(`${FOUNDRY_PATH}health`, { credentials: "same-origin", cache: "no-store", signal });
    if (response.ok) return { ok: true, detail: "" };
    let detail = "";
    try {
      const body = (await response.json()) as { detail?: unknown; error?: unknown };
      detail = String(body.detail ?? body.error ?? "");
    } catch {
      /* non-JSON body: the status is all there is */
    }
    return { ok: false, detail: detail || `HTTP ${response.status}` };
  } catch (error) {
    if (signal?.aborted) throw error;
    return { ok: false, detail: error instanceof Error ? error.message : String(error) };
  }
}
