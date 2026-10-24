import { useCallback, useEffect, useState } from "react";
import { Loader2 } from "lucide-react";
import { api } from "@/app/api";
import { Button } from "@/components/ui/button";
import type { Language } from "@/app/language";

export interface DependencyDTO {
  id: string;
  installed: boolean;
  version?: string;
  installable: boolean;
  detail?: string;
}

export interface DependenciesDTO {
  host_id: string;
  dependencies: DependencyDTO[];
}

export interface DependenciesProps {
  hostId: string;
  revision: number;
  locale: Language;
}

const english: Record<string, string> = {
  "Dependencias de ejecución": "Runtime dependencies",
  "No se pudieron leer las dependencias del host.": "The host dependencies could not be read.",
  "Leyendo dependencias…": "Reading dependencies…",
  "Reintentar": "Retry",
  "instalado": "installed",
  "ausente": "missing",
  "Instalación no disponible desde Workbench.": "Installation is not available from Workbench.",
  "Instalar": "Install",
  "Instalando": "Installing",
  "queda instalado sin sudo.": "was installed without sudo.",
  "No se pudo instalar la dependencia.": "The dependency could not be installed.",
};

function translate(value: unknown, locale: Language): string {
  if (typeof value !== "string") return "";
  if (locale === "en" && value in english) return english[value];
  return value;
}

function path(hostId: string) {
  return `/api/hosts/${encodeURIComponent(hostId)}/dependencies`;
}

export function Dependencies({ hostId, revision, locale }: DependenciesProps) {
  const t = (value: unknown) => translate(value, locale);
  const [rows, setRows] = useState<DependencyDTO[] | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState<string | null>(null);
  const [notice, setNotice] = useState("");

  const load = useCallback(
    async (signal?: AbortSignal) => {
      setError("");
      try {
        const payload = await api<DependenciesDTO>(path(hostId), { signal });
        if (signal?.aborted) return;
        setRows(payload.dependencies ?? []);
      } catch (value) {
        if (signal?.aborted) return;
        setRows(null);
        setError(
          value instanceof Error ? value.message : String(t("No se pudieron leer las dependencias del host.")),
        );
      }
    },
    [hostId, t],
  );

  useEffect(() => {
    const controller = new AbortController();
    void load(controller.signal);
    return () => controller.abort();
  }, [load]);

  async function install(dependency: string) {
    setBusy(dependency);
    setNotice("");
    setError("");
    try {
      await api<unknown>(path(hostId), {
        method: "POST",
        data: { dependency, revision },
      });
      setNotice(`${dependency} ${t("queda instalado sin sudo.")}`);
      await load();
    } catch (value) {
      setError(
        value instanceof Error ? value.message : String(t("No se pudo instalar la dependencia.")),
      );
    } finally {
      setBusy(null);
    }
  }

  return (
    <div className="space-y-3" aria-busy={rows === null && !error}>
      <h3 className="text-sm font-medium">{t("Dependencias de ejecución")}</h3>
      {error ? (
        <div className="space-y-2">
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
          <Button variant="outline" size="sm" onClick={() => void load()}>
            {t("Reintentar")}
          </Button>
        </div>
      ) : rows === null ? (
        <p role="status" className="flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 size={15} className="animate-spin" />
          {t("Leyendo dependencias…")}
        </p>
      ) : (
        <ul className="space-y-2">
          {rows.map((row) => (
            <li
              key={row.id}
              className="flex flex-wrap items-center gap-2 rounded-lg border px-3 py-2 text-sm"
            >
              <code className="font-mono">{row.id}</code>
              <span className="text-muted-foreground">
                {row.installed
                  ? `${t("instalado")}${row.version ? ` · ${row.version}` : ""}`
                  : t("ausente")}
              </span>
              {row.installable && !row.installed && (
                <Button
                  variant="outline"
                  size="sm"
                  disabled={busy !== null}
                  onClick={() => void install(row.id)}
                >
                  {busy === row.id && <Loader2 size={14} className="animate-spin" />}
                  {busy === row.id ? `${t("Instalando")} ${row.id}…` : `${t("Instalar")} ${row.id}`}
                </Button>
              )}
              {!row.installable && !row.installed && (
                <span className="text-xs text-muted-foreground">
                  {t("Instalación no disponible desde Workbench.")}
                </span>
              )}
            </li>
          ))}
        </ul>
      )}
      {notice ? <p role="status" className="text-sm text-muted-foreground">{notice}</p> : null}
    </div>
  );
}
