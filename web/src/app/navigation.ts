import { Bot, FlaskConical, Link2, ListChecks, MessagesSquare, SlidersHorizontal, Workflow, type LucideIcon } from "lucide-react";
import { views as modelsViews } from "@/modules/models/domain";
import { conversationViews, tasksViews } from "@/modules/conversations/domain";
import { foundryViews } from "@/modules/foundry/domain";

export interface Section {
  id: string;
  label: string;
  group: string;
  icon: LucideIcon;
  /** Sub-views of the section in the order the shell shows them. Sections without
   *  views (Conexiones, Ajustes) render a single screen and keep ?view= absent. */
  views: [string, string][];
}

/** Implemented sections only: a section appears here when its screen is ported.
 *  Other sections land with their modules (M03–M08). */
export const sections: Section[] = [

  {
    id: "conversations",
    label: "Conversaciones",
    group: "Espacio de trabajo",
    icon: MessagesSquare,
    views: conversationViews,
  },
  {
    id: "tasks",
    label: "Tareas",
    group: "Espacio de trabajo",
    icon: ListChecks,
    views: tasksViews,
  },
  {
    id: "foundry",
    label: "Foundry",
    group: "Espacio de trabajo",
    icon: FlaskConical,
    views: foundryViews,
  },
  {
    id: "sim",
    label: "SIM",
    group: "Espacio de trabajo",
    icon: Workflow,
    views: [],
  },

  {
    id: "models",
    label: "Configuración de agentes",
    group: "Instalación",
    icon: Bot,
    views: modelsViews,
  },

  {
    id: "connections",
    label: "Conexiones",
    group: "Instalación",
    icon: Link2,
    views: [],
  },
  {
    id: "settings",
    label: "Ajustes",
    group: "Instalación",
    icon: SlidersHorizontal,
    views: [],
  },
];

export interface Route {
  section: Section | null;
  view: string;
}

/** Retired deep links. The section no longer exists, so an old bookmark resolves to
 *  the screen that owns its data now: the Equipos section (host registry), the selected
 *  host's Magpie usage under Modelos → Uso, the SIM section that replaced Orbit, or the
 *  Foundry section that replaced Aprendizajes.
 *  The URL is left as it came: no second router and no replaceState rewriting the
 *  address the operator typed. */
function retiredRoute(requested: string, view: string | null) {
  if (requested === "hosts") return { section: "connections", view: undefined };
  if (requested === "work" || requested === "terminal") return { section: "conversations", view: undefined };
  if (requested === "usage") return { section: "models", view: "usage" };
  if (requested === "orbit") return { section: "sim", view: undefined };
  // Learnings (memU) was replaced by Foundry; its old deep links open Foundry's learning page.
  if (requested === "learnings") return { section: "foundry", view: "learning" };
  if (requested !== "atlas") return null;
  return view === "hosts"
    ? { section: "connections", view: undefined }
    : { section: "models", view: "usage" };
}

/** Deep link target: ?section=<id>&view=<view>. Module filters share the same query. */
export function readRoute(value: string = location.href): Route {
  const params = new URL(value, "http://localhost").searchParams;
  const requested = params.get("section");
  if (requested === null)
    return { section: sections[0], view: sections[0].views[0]?.[0] ?? "" };
  const direct = sections.find((s) => s.id === requested) ?? null;
  const retarget = direct ? null : retiredRoute(requested, params.get("view"));
  const section =
    direct ?? sections.find((s) => s.id === retarget?.section) ?? null;
  if (!section) return { section: null, view: "" };
  const view = retarget ? (retarget.view ?? "") : (params.get("view") ?? "");
  return {
    section,
    view: section.views.some(([id]) => id === view)
      ? view
      : (section.views[0]?.[0] ?? ""),
  };
}

/** Keeps unrelated query parameters (host selection, module filters) so switching view
 *  keeps the dataset. A cross-section link that targets one host passes it here, using the
 *  same ?host= the sections already read, so no second authority over the selection exists. */
export function sectionURL(section: string, view?: string, host?: string) {
  const url = new URL(location.href);
  url.searchParams.set("section", section);
  if (view) url.searchParams.set("view", view);
  else url.searchParams.delete("view");
  if (host) {
    if (host === LOCAL_HOST) url.searchParams.delete("host");
    else url.searchParams.set("host", host);
  }
  return url;
}

/** The local installation is the absence of ?host=, exactly as the sections treat it. */
export const LOCAL_HOST = "local";
