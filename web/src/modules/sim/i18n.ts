import type { Language } from "@/app/language";

export type { Language };

/** Module strings are authored in Spanish, the way the ported modules do it. English
 *  lives here; a missing entry stays Spanish and is visible in review. */
const english: Record<string, string> = {
  "Panel original del SIM": "Original SIM panel",
  Recargar: "Reload",
  "Abrir en pestaña": "Open in tab",
  "Abriendo el SIM…": "Opening SIM…",
  "El SIM no está configurado": "SIM is not configured",
  "El SIM no responde": "SIM is not responding",
};

/** Exact-text translation for this module: unknown text passes through unchanged. */
export function translate(value: unknown, language: Language) {
  const text = String(value ?? "");
  if (language === "es") return text;
  return english[text] ?? text;
}

/** Consumed by the contract test: this module's strings must be translatable. */
export const simEnglish = english;
