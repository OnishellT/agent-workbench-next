import { useEffect, useState } from "react";
import { AlertCircle, ArrowUpRight, Loader2, RefreshCw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select";
import type { Language } from "@/app/language";
import { useHosts } from "@/modules/hosts/client";
import { nativeViews, viewOf, type ModelsView } from "./domain";
import { openNative, openNativeTab, type NativeOpenDTO } from "./native";
import { translate } from "./i18n";

interface ModelsSectionProps {
  locale: Language;
  /** Shell navigation: cross-section links and the section's own view id. */
  onNavigate?: (section: string, view?: string, host?: string) => void;
  /** View requested by the shell (?section=models&view=…). An unknown or absent
   *  value keeps the tab the operator is on instead of resetting the screen. */
  view?: string;
  /** Host chosen by the shell; without it the deep link ?host= decides, then local. */
  host?: string;
}

/** Modelos de un host: el contenedor de su Magpie ORIGINAL. Esta sección no dibuja
 *  cuentas, claves ni uso propios: pide al host el panel nativo, lo enmarca en su
 *  pantalla y no escribe ninguna configuración por su cuenta. El host elegido vive en
 *  ?host=, así que volver atrás o recargar conserva el mismo Magpie, y un host
 *  desconocido nunca cae en la instancia local. */
function ModelsSection({
  locale,
  onNavigate,
  view,
  host: chosen,
}: ModelsSectionProps) {
  const t = (value: unknown) => translate(value, locale);
  const { hosts, error: hostsError } = useHosts();
  const [current, setCurrent] = useState<ModelsView>(() => viewOf(view));
  const [host, setHost] = useState(
    () => chosen ?? new URLSearchParams(location.search).get("host") ?? "local",
  );
  const [panel, setPanel] = useState<NativeOpenDTO | null>(null);
  const [error, setError] = useState("");
  const [loaded, setLoaded] = useState(false);
  const [revision, setRevision] = useState(0);
  const [opening, setOpening] = useState(false);

  useEffect(() => {
    if (!view) return;
    setCurrent(viewOf(view));
  }, [view]);

  // The shell keeps the selected host in the URL and this screen stays mounted across
  // back/forward: a traversal or a shell-provided host must reach it.
  useEffect(() => {
    if (chosen !== undefined) setHost(chosen);
  }, [chosen]);
  useEffect(() => {
    const sync = () =>
      setHost(new URLSearchParams(location.search).get("host") ?? "local");
    window.addEventListener("popstate", sync);
    return () => window.removeEventListener("popstate", sync);
  }, []);

  // One request per host+screen: the installation says whether that Magpie is up. The
  // panel is asked again on reload and after a failure; nothing but this state holds it.
  useEffect(() => {
    const controller = new AbortController();
    setPanel(null);
    setError("");
    setLoaded(false);
    openNative(host, nativeViews[current], controller.signal)
      .then((opened) => {
        if (!controller.signal.aborted) setPanel(opened);
      })
      .catch((value: unknown) => {
        if (controller.signal.aborted) return;
        setError(
          value instanceof Error
            ? value.message
            : "No se pudo abrir el panel de Magpie.",
        );
      });
    return () => controller.abort();
  }, [host, current, revision]);

  const known = hosts.find((row) => row.id === host);
  const label = host === "local" ? t("este servidor") : known?.name || host;

  // One place keeps ?host= and the selector in sync, from the dropdown and from
  // the remote-mode shortcuts below.
  function choose(next: string) {
    setHost(next);
    const url = new URL(location.href);
    if (next === "local") url.searchParams.delete("host");
    else url.searchParams.set("host", next);
    history.replaceState(null, "", url);
  }

  async function newTab() {
    setOpening(true);
    setError("");
    try {
      await openNativeTab(host, nativeViews[current]);
    } catch (value) {
      setError(
        value instanceof Error ? value.message : "No se pudo abrir Magpie.",
      );
    } finally {
      setOpening(false);
    }
  }

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-end gap-3">
        <div className="space-y-1.5">
          <label className="block text-sm font-medium" htmlFor="models-host">
            {t("Host de los modelos")}
          </label>
          <NativeSelect
            id="models-host"
            value={host}
            onChange={(event) => choose(event.target.value)}
          >
            <NativeSelectOption value="local">{t("Este servidor")}</NativeSelectOption>
            {hosts.map((row) => (
              <NativeSelectOption key={row.id} value={row.id}>
                {row.name}
                {!row.enabled
                  ? ` · ${t("pausado")}`
                  : row.status !== "online"
                    ? ` · SSH ${row.ssh_status || t("sin comprobar")}`
                    : ""}
              </NativeSelectOption>
            ))}
            {host !== "local" && !known && (
              <NativeSelectOption value={host}>
                {t("Host no disponible")}
              </NativeSelectOption>
            )}
          </NativeSelect>
        </div>
        <Button
          variant="outline"
          onClick={() => setRevision((value) => value + 1)}
          disabled={!panel && !error}
        >
          <RefreshCw size={15} />
          {t("Recargar")}
        </Button>
        <Button variant="outline" onClick={() => void newTab()} disabled={opening}>
          <ArrowUpRight size={15} />
          {t("Abrir en pestaña")}
        </Button>
      </div>
      {hostsError && (
        <p role="alert" className="text-sm text-destructive">
          {t(hostsError)}
        </p>
      )}

      {error ? (
        <div className="space-y-3 rounded-xl border bg-card p-6" role="alert">
          <div className="flex items-center gap-2">
            <AlertCircle size={17} className="text-destructive" />
            <h3 className="font-medium">{t("No se pudo abrir Magpie")}</h3>
          </div>
          <p className="text-sm text-muted-foreground">
            {error === "magpie_not_running"
              ? t("El Magpie de ese host no está en ejecución.")
              : t(error)}
          </p>
          <div className="flex flex-wrap gap-2">
            <Button onClick={() => setRevision((value) => value + 1)}>
              {t("Reintentar")}
            </Button>
            {host !== "local" && (
              <Button variant="outline" onClick={() => onNavigate?.("hosts")}>
                {t("Preparar Magpie y revisar SSH")}
              </Button>
            )}
            {host === "local" &&
              hosts
                .filter((row) => row.enabled)
                .map((row) => (
                  <Button key={row.id} variant="outline" onClick={() => choose(row.id)}>
                    {row.name}
                  </Button>
                ))}
          </div>
        </div>
      ) : (
        <section aria-label={t("Panel nativo completo de Magpie")} aria-busy={!loaded}>
          {!loaded && (
            <div
              role="status"
              className="flex items-center gap-2 rounded-xl border bg-card p-6 text-sm text-muted-foreground"
            >
              <Loader2 className="animate-spin" size={18} />
              <span>{t("Abriendo Magpie…")}</span>
            </div>
          )}
          {panel && (
            <iframe
              key={`${host}:${current}:${revision}`}
              className="h-[70vh] w-full rounded-xl border bg-card"
              title={`Magpie · ${label}`}
              src={panel.url}
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

export default ModelsSection;
