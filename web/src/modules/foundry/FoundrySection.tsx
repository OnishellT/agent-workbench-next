import { useEffect, useRef, useState } from "react";
import { ArrowUpRight, Loader2, RefreshCw } from "lucide-react";
import { Button, buttonVariants } from "@/components/ui/button";
import type { Language } from "@/app/language";
import {
  FOUNDRY_PATH,
  LANG_MESSAGE,
  NAVIGATED_MESSAGE,
  THEME_MESSAGE,
  VIEW_MESSAGE,
  currentTheme,
  foundryViews,
  frameSource,
  probeFoundry,
  viewOf,
  type Availability,
} from "./domain";
import { translate } from "./i18n";

export interface FoundrySectionProps {
  locale: Language;
  /** Shell navigation: this section's own view id doubles as the framed Foundry page. */
  onNavigate?: (section: string, view?: string) => void;
  /** Page requested by the shell (?section=foundry&view=…). */
  view?: string;
}

/** Conversation Foundry, framed from this origin. The server starts it on demand and
 *  proxies it behind the dashboard session (it supplies Foundry's own token), so the frame
 *  needs no login and the browser never sees a second credential. The shell tab bar and
 *  the framed app share one page: the tabs drive it by message and the app reports back. */
export function FoundrySection({ locale, onNavigate, view }: FoundrySectionProps) {
  const t = (value: unknown) => translate(value, locale);
  const current = viewOf(view);
  const [availability, setAvailability] = useState<Availability | null>(null);
  const [revision, setRevision] = useState(0);
  const [loaded, setLoaded] = useState(false);
  const frame = useRef<HTMLIFrameElement>(null);
  // The frame address is fixed per load: following a tab by reloading would drop the
  // framed app's state, so later pages travel by message instead.
  const [source] = useState(() => frameSource(current, locale));
  const latest = useRef({ current, onNavigate });
  latest.current = { current, onNavigate };

  useEffect(() => {
    const controller = new AbortController();
    setAvailability(null);
    probeFoundry(controller.signal)
      .then(setAvailability)
      .catch(() => undefined);
    return () => controller.abort();
  }, [revision]);

  useEffect(() => {
    function onMessage(event: MessageEvent) {
      if (event.origin !== location.origin || event.source !== frame.current?.contentWindow) return;
      const data = event.data as { type?: unknown; view?: unknown } | null;
      if (data?.type !== NAVIGATED_MESSAGE || typeof data.view !== "string") return;
      const next = viewOf(data.view);
      if (next !== latest.current.current) latest.current.onNavigate?.("foundry", next);
    }
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, []);

  useEffect(() => {
    if (loaded) frame.current?.contentWindow?.postMessage({ type: VIEW_MESSAGE, view: current }, location.origin);
  }, [current, loaded]);

  // The shell's language selector drives Foundry's language the same way.
  useEffect(() => {
    if (loaded) frame.current?.contentWindow?.postMessage({ type: LANG_MESSAGE, lang: locale }, location.origin);
  }, [locale, loaded]);

  // The shell toggles `dark` on <html>; the frame follows without reloading.
  useEffect(() => {
    const send = () => frame.current?.contentWindow?.postMessage({ type: THEME_MESSAGE, theme: currentTheme() }, location.origin);
    const observer = new MutationObserver(send);
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ["class"] });
    if (loaded) send();
    return () => observer.disconnect();
  }, [loaded]);

  function reload() {
    setLoaded(false);
    setRevision((value) => value + 1);
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end justify-between gap-2 border-b">
        <nav className="flex flex-wrap gap-1" aria-label={`${t("Secciones de")} Foundry`}>
          {foundryViews.map(([id, label]) => (
            <button
              key={id}
              type="button"
              aria-current={current === id ? "page" : undefined}
              className={`-mb-px border-b-2 px-3 py-2 text-sm ${
                current === id
                  ? "border-primary font-medium text-foreground"
                  : "border-transparent text-muted-foreground hover:text-foreground"
              }`}
              onClick={() => onNavigate?.("foundry", id)}
            >
              {t(label)}
            </button>
          ))}
        </nav>
        <div className="flex gap-2 pb-1">
          <Button variant="outline" size="sm" onClick={reload}>
            <RefreshCw size={14} />
            {t("Recargar")}
          </Button>
          <a
            className={buttonVariants({ variant: "outline", size: "sm" })}
            href={FOUNDRY_PATH}
            target="_blank"
            rel="noopener noreferrer"
          >
            <ArrowUpRight size={14} />
            {t("Abrir en pestaña")}
          </a>
        </div>
      </div>

      {availability && !availability.ok ? (
        <div role="alert" className="space-y-2 rounded-xl border bg-card p-6">
          <h2 className="font-medium">{t("Foundry no está disponible")}</h2>
          <p className="text-sm text-muted-foreground">
            {t("Foundry necesita Python 3.11 o superior en el equipo que ejecuta Workbench. Se inicia solo al abrir esta sección.")}
          </p>
          <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-words rounded-md bg-muted p-3 text-xs">
            {availability.detail}
          </pre>
        </div>
      ) : (
        <section aria-label={t("Panel de Foundry")} aria-busy={!loaded} className="space-y-3">
          {(!availability || !loaded) && (
            <p role="status" className="flex items-center gap-2 rounded-xl border bg-card p-6 text-sm text-muted-foreground">
              <Loader2 className="animate-spin" size={18} />
              {t("Abriendo Foundry…")}
            </p>
          )}
          {availability?.ok && (
            <iframe
              key={revision}
              ref={frame}
              className={`w-full border-0 bg-transparent ${loaded ? "h-[calc(100vh-14rem)] min-h-[520px]" : "h-0"}`}
              title="Foundry"
              src={source}
              referrerPolicy="no-referrer"
              onLoad={() => setLoaded(true)}
            />
          )}
        </section>
      )}
    </div>
  );
}

export default FoundrySection;
