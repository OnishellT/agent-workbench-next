import { useEffect, useState } from "react";
import { ArrowUpRight, Loader2, RefreshCw } from "lucide-react";
import { Button, buttonVariants } from "@/components/ui/button";
import { api } from "@/app/api";
import type { Language } from "@/app/language";
import { translate } from "./i18n";

export interface SimSectionProps {
  locale: Language;
  /** Canonical SIM origin this installation publishes (bootstrap.sim_url, validated by the
   *  server). Absent means it published none: this screen says so instead of inventing one. */
  url?: string;
}

/** La interfaz ORIGINAL del SIM, enmarcada tal cual: la sección no dibuja panel propio, no
 *  habla con su API y no inventa un origen. */
export function SimSection({ locale, url }: SimSectionProps) {
  const t = (value: unknown) => translate(value, locale);
  const [revision, setRevision] = useState(0);
  const [loaded, setLoaded] = useState(false);
  // The browser cannot probe the SIM itself (connect-src 'self'), so the server says
  // whether it answers. A failed probe is "unknown" and still frames the SIM.
  const [probe, setProbe] = useState<"checking" | "up" | "down">("checking");

  useEffect(() => {
    if (!url) return;
    const controller = new AbortController();
    setProbe("checking");
    api<{ reachable: boolean }>("/api/sim/status", { signal: controller.signal })
      .then((status) => setProbe(status.reachable ? "up" : "down"))
      .catch(() => {
        if (!controller.signal.aborted) setProbe("up");
      });
    return () => controller.abort();
  }, [url, revision]);

  function reload() {
    setLoaded(false);
    setRevision((value) => value + 1);
  }

  if (!url)
    return (
      <div role="status" className="space-y-2 rounded-xl border bg-card p-6">
        <h3 className="font-medium">{t("El SIM no está configurado")}</h3>
        <p className="text-sm">
          <code>WORKBENCH_SIM_URL</code>
        </p>
      </div>
    );

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <Button variant="outline" onClick={reload}>
          <RefreshCw size={15} />
          {t("Recargar")}
        </Button>
        <a
          className={buttonVariants({ variant: "outline" })}
          href={url}
          target="_blank"
          rel="noopener noreferrer"
        >
          <ArrowUpRight size={15} />
          {t("Abrir en pestaña")}
        </a>
      </div>
      {probe === "down" ? (
        <div role="alert" className="space-y-2 rounded-xl border bg-card p-6">
          <h3 className="font-medium">{t("El SIM no responde")}</h3>
          <p className="text-sm">
            <code>{url}</code>
          </p>
        </div>
      ) : (
        <section
          aria-label={t("Panel original del SIM")}
          aria-busy={!loaded}
          className="space-y-3"
        >
          {(probe === "checking" || !loaded) && (
            <p
              role="status"
              className="flex items-center gap-2 rounded-xl border bg-card p-6 text-sm text-muted-foreground"
            >
              <Loader2 className="animate-spin" size={18} />
              {t("Abriendo el SIM…")}
            </p>
          )}
          {probe === "up" && (
            <iframe
              key={revision}
              className="h-[70vh] w-full rounded-xl border bg-card"
              title={`SIM · ${url}`}
              src={url}
              allow="clipboard-write; fullscreen"
              referrerPolicy="no-referrer"
              onLoad={() => setLoaded(true)}
            />
          )}
        </section>
      )}
    </div>
  );
}

export default SimSection;
