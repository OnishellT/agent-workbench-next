/** Tabs of the Magpie section, in the order the screen shows them. The ids are the ones
 *  callers already deep-link to (Work: `onNavigate("models", "codex")`, Equipos:
 *  `… , "usage"`, retired Atlas bookmarks: `usage`), so a bookmark keeps landing on the
 *  same subject; `viewOf` resolves an unknown or absent value to the first tab. */
export const views: [string, string][] = [
  ["codex", "Cuentas"],
  ["agents", "Agents"],
  ["catalog", "Catálogo"],
  ["routing", "Rutas"],
  ["keys", "Claves y LAN"],
  ["usage", "Uso"],
  ["settings", "Ajustes"],
];

export type ModelsView = (typeof views)[number][0];

export function viewOf(view: string | undefined): ModelsView {
  const requested = view?.trim();
  return (views.find(([id]) => id === requested)?.[0] ?? views[0][0]) as ModelsView;
}

/** The original screen of that host's Magpie the container frames: the native page boots
 *  on `?view=` (start.js of the pinned GUI: agents, providers and settings, plus the
 *  plugin pages, among them routing, gateway and usage). Two tabs share one native
 *  screen: the catalog lives inside Providers, with the accounts. */
export const nativeViews: Record<ModelsView, string> = {
  codex: "providers",
  agents: "agents",
  catalog: "providers",
  routing: "routing",
  keys: "gateway",
  usage: "usage",
  settings: "settings",
};
