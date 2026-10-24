import type { Language } from "@/app/language";

export type { Language };

/** Module strings are authored in Spanish, the way the ported modules do it. English
 *  lives here; a missing entry stays Spanish and is visible in review. */
const english: Record<string, string> = {
  "Secciones de": "Sections of",
  "Host de los modelos": "Models host",
  "Este servidor": "This server",
  "este servidor": "this server",
  "Host no disponible": "Host unavailable",
  pausado: "paused",
  "sin comprobar": "not checked",
  Cuentas: "Accounts",
  Agents: "Agents",
  Catálogo: "Catalog",
  Rutas: "Routing",
  "Claves y LAN": "Keys and LAN",
  Uso: "Usage",
  Ajustes: "Settings",
  Recargar: "Reload",
  "Abrir en pestaña": "Open in a tab",
  "Abriendo Magpie…": "Opening Magpie…",
  "No se pudo abrir Magpie": "Magpie could not be opened",
  Reintentar: "Retry",
  "Preparar Magpie y revisar SSH": "Prepare Magpie and check SSH",
  "Panel nativo completo de Magpie": "Full native Magpie panel",
  "El Magpie de ese host no está en ejecución.": "That host's Magpie is not running.",
};

const escape = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
const pattern = new RegExp(
  "(?<![\\p{L}\\p{N}_-])(?:" +
    Object.keys(english)
      .sort((a, b) => b.length - a.length)
      .map(escape)
      .join("|") +
    ")(?![\\p{L}\\p{N}_-])",
  "gu",
);

export function translate(value: unknown, language: Language) {
  const text = String(value ?? "");
  return language === "es"
    ? text
    : text.replace(pattern, (key) => english[key]);
}
