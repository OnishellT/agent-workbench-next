import React, { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import { asLang, browserLang, setLang, startTranslation, type Lang } from "./i18n";
import "./index.css";

// Standalone, the app owns the whole window. A host shell (Agent Workbench) embeds it in a
// same-origin frame with ?embedded=1&view=<page>: the host's navigation drives the page via
// postMessage and the app reports its own page changes back, so the frame keeps no sidebar.
const query = new URLSearchParams(location.search);
const embedded = query.has("embedded") && window.parent !== window;

// Light/dark: the host passes its theme (?theme=, then foundry:theme messages); standalone
// follows the system preference.
type Theme = "light" | "dark";
const asTheme = (value: unknown): Theme | null => (value === "light" || value === "dark" ? value : null);
const applyTheme = (theme: Theme) => document.documentElement.classList.toggle("dark", theme === "dark");
const system = window.matchMedia("(prefers-color-scheme: dark)");
applyTheme(asTheme(query.get("theme")) ?? (system.matches ? "dark" : "light"));
if (!embedded) system.addEventListener("change", (event) => applyTheme(event.matches ? "dark" : "light"));

// Language: the host passes its own (?lang=, then foundry:lang messages); standalone follows the browser.
const initialLang = asLang(query.get("lang")) ?? browserLang();
startTranslation(initialLang);

function Root() {
  const [view, setView] = useState(query.get("view") || "dashboard");
  const [lang, setLangState] = useState<Lang>(initialLang);
  useEffect(() => {
    if (!embedded) return;
    const onMessage = (event: MessageEvent) => {
      if (event.origin !== location.origin || event.source !== window.parent) return;
      if (event.data?.type === "foundry:view" && typeof event.data.view === "string") setView(event.data.view);
      const theme = event.data?.type === "foundry:theme" ? asTheme(event.data.theme) : null;
      if (theme) applyTheme(theme);
      const next = event.data?.type === "foundry:lang" ? asLang(event.data.lang) : null;
      if (next) {
        setLang(next);
        setLangState(next);
      }
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, []);
  // The key remounts the app on a language change so dates and every built string re-render.
  return (
    <App
      key={lang}
      embedded={embedded}
      initialView={view}
      onNavigate={embedded ? (page) => window.parent.postMessage({ type: "foundry:navigated", view: page }, location.origin) : undefined}
    />
  );
}

createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <Root />
  </React.StrictMode>,
);
