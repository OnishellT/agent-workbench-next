import type { Language } from "@/app/language";

export type { Language };

/** Module strings are authored in Spanish, like the other modules. Foundry's own screens
 *  inside the frame are Spanish; this covers the Workbench chrome around them. */
const english: Record<string, string> = {
  // Pages of the framed app (domain.ts foundryViews), the shell tab bar's labels.
  "Vista general": "Overview",
  Fuentes: "Sources",
  Conversaciones: "Conversations",
  Aprendizajes: "Learning",
  Experimentos: "Experiments",
  Configuración: "Configuration",
  "Recargar": "Reload",
  "Abrir en pestaña": "Open in tab",
  "Abriendo Foundry…": "Opening Foundry…",
  "Foundry no está disponible": "Foundry is not available",
  "Foundry necesita Python 3.11 o superior en el equipo que ejecuta Workbench. Se inicia solo al abrir esta sección.":
    "Foundry needs Python 3.11 or newer on the machine that runs Workbench. It starts by itself when this section opens.",
  "Panel de Foundry": "Foundry panel",
  "Secciones de": "Sections of",
};

/** Exact-text translation for this module: unknown text passes through unchanged. */
export function translate(value: unknown, language: Language) {
  const text = String(value ?? "");
  if (language === "es") return text;
  return english[text] ?? text;
}

/** Consumed by the contract test: this module's strings must be translatable. */
export const foundryEnglish = english;
