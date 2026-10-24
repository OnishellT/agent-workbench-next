export type Language = "es" | "en";

const KEY = "workbench-language";

/** Language preference: explicit ?lang= wins, then the saved choice, else Spanish. */
export function initialLanguage(): Language {
  const query = new URLSearchParams(location.search).get("lang");
  if (query === "en" || query === "es") return query;
  try {
    return localStorage.getItem(KEY) === "en" ? "en" : "es";
  } catch {
    return "es";
  }
}

export function applyLanguage(language: Language) {
  document.documentElement.lang = language;
  try {
    localStorage.setItem(KEY, language);
  } catch {
    /* private mode: the preference is optional */
  }
}
