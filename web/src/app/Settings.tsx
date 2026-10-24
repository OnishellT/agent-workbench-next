import { useCallback, useEffect, useState } from "react";
import { ExternalLink, FolderOpen, ListChecks, Plus, RefreshCw } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  fetchDiagnostics,
  type BootstrapDTO,
  type DiagnosticsDTO,
  type ManagedModuleDTO,
  type SettingsDTO,
} from "./api";
import { translateShell } from "./i18n";
import type { Language } from "./language";

/** What the server probed, never what a mount flag suggests. */
const managedStateLabels: Record<string, string> = {
  ready: "Listo",
  unready: "Sin responder",
  stopped: "Detenido",
  unavailable: "Sin módulo gestionado",
};

/** Routine events already say everything in their label; only the others carry a detail. */
const routineManagedEvents = new Set(["start", "ready", "probe_ok"]);

const managedEventLabels: Record<string, string> = {
  start: "proceso iniciado",
  ready: "listo",
  unready: "sin respuesta en el arranque",
  probe_ok: "sonda correcta",
  probe_failed: "sonda fallida",
  exit: "proceso terminado",
};

/** Server timestamps as local clock; absent is never "now". */
function clock(value: string | undefined, language: Language) {
  const timestamp = value ? Date.parse(value) : Number.NaN;
  if (!Number.isFinite(timestamp)) return "—";
  return new Intl.DateTimeFormat(language === "es" ? "es-DO" : "en-US", {
    timeStyle: "medium",
  }).format(timestamp);
}

function managedBadgeVariant(state: string | undefined) {
  if (state === "ready") return "secondary" as const;
  if (state === undefined) return "outline" as const;
  return "destructive" as const;
}

/** M02 settings of this installation: project folders, over the frozen
 *  `bootstrap.settings` shape and POST /api/settings.
 *  Nothing flips locally: every control shows the state the installation last
 *  confirmed, and a rejected write keeps that state with the server message. */
export default function Settings({
  boot,
  save,
  onOpenSection,
  language,
}: {
  boot: BootstrapDTO | null;
  save: (patch: SettingsDTO) => Promise<void>;
  onOpenSection: (section: string, view?: string) => void;
  language: Language;
}) {
  const t = (value: unknown) => translateShell(value, language);
  const settings = boot?.settings ?? {};
  const known = boot !== null && boot.settings !== undefined;
  const projects = settings.projects ?? [];
  const [path, setPath] = useState("");
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [report, setReport] = useState<DiagnosticsDTO | null>(null);
  const [reading, setReading] = useState(false);
  const [readError, setReadError] = useState("");

  // A failed read never leaves a previous healthy report on screen.
  const readDiagnostics = useCallback(async () => {
    setReading(true);
    setReadError("");
    try {
      setReport(await fetchDiagnostics());
    } catch (value) {
      setReport(null);
      setReadError((value as Error).message);
    } finally {
      setReading(false);
    }
  }, []);

  const diagnosticsMounted = boot?.modules?.module_diagnostics === "mounted";
  useEffect(() => {
    if (diagnosticsMounted) void readDiagnostics();
  }, [diagnosticsMounted, readDiagnostics]);

  async function write(key: string, patch: SettingsDTO, message: string) {
    if (busy) return false;
    setBusy(key);
    setError("");
    setNotice("");
    try {
      await save(patch);
      setNotice(message);
      return true;
    } catch (value) {
      setError((value as Error).message);
      return false;
    } finally {
      setBusy("");
    }
  }

  const modules = report?.modules ?? boot?.modules ?? {};
  const mountedModules = Object.entries(modules)
    .filter(([, state]) => state === "mounted")
    .map(([name]) => name);
  const unmountedModules = Object.entries(modules)
    .filter(([, state]) => state !== "mounted")
    .map(([name]) => name);
  const unported = report?.routes_not_ported ?? boot?.routes_not_ported ?? [];
  const managed: ManagedModuleDTO | undefined = report?.native_module;
  const events = managed?.events ?? [];

  return (
    <div className="grid items-start gap-5 xl:grid-cols-2">
      {!known && (
        <p
          role="alert"
          className="rounded-lg border border-destructive/30 bg-destructive/5 p-4 text-sm text-destructive xl:col-span-2"
        >
          {t(
            "Esta instalación no reportó ajustes en bootstrap. No se puede confirmar ni cambiar la configuración desde aquí.",
          )}
        </p>
      )}
      <Card>
        <CardHeader className="flex flex-row items-start justify-between gap-3">
          <CardTitle role="heading" aria-level={3}>
            {t("Proyectos")}
          </CardTitle>
          <FolderOpen className="mt-1 size-4 shrink-0 text-muted-foreground" />
        </CardHeader>
        <CardContent className="space-y-4">
          {projects.length ? (
            <ul className="divide-y">
              {projects.map((project) => (
                <li key={project.path} className="flex items-center gap-3 py-3">
                  <FolderOpen className="size-4 shrink-0 text-muted-foreground" />
                  <span className="min-w-0 flex-1">
                    <strong className="block truncate">{project.name}</strong>
                    <span className="mt-0.5 block break-all text-xs text-muted-foreground">
                      {project.path}
                    </span>
                  </span>
                  <Button
                    variant="ghost"
                    size="sm"
                    disabled={busy !== ""}
                    aria-label={`${t("Quitar")}: ${project.path}`}
                    onClick={() =>
                      write(
                        "projects",
                        {
                          projects: projects.filter((p) => p.path !== project.path),
                        },
                        "Proyecto quitado.",
                      )
                    }
                  >
                    {t("Quitar")}
                  </Button>
                </li>
              ))}
            </ul>
          ) : null}
          <form
            className="flex flex-wrap items-end gap-2"
            onSubmit={async (event) => {
              event.preventDefault();
              const folder = path.trim();
              if (!folder) return;
              const name =
                folder.replace(/\/+$/, "").split("/").filter(Boolean).pop() || folder;
              const saved = await write(
                "projects",
                { projects: [...projects, { path: folder, name: name.slice(0, 120) }] },
                "Proyecto añadido.",
              );
              if (saved) setPath("");
            }}
          >
            <label className="grid min-w-48 flex-1 gap-2 text-sm font-medium" htmlFor="project-path">
              {t("Ruta de la carpeta")}
              <Input
                id="project-path"
                value={path}
                onChange={(event) => setPath(event.target.value)}
                placeholder="~/Projects/mi-proyecto"
                maxLength={1000}
                disabled={busy !== ""}
                required
              />
            </label>
            <Button
              type="submit"
              variant="outline"
              disabled={busy !== "" || !path.trim()}
            >
              <Plus className="size-3.5" />
              {t("Añadir")}
            </Button>
          </form>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="flex flex-row items-start justify-between gap-3">
          <CardTitle role="heading" aria-level={3}>
            {t("Equipos y agentes")}
          </CardTitle>
          <ListChecks className="mt-1 size-4 shrink-0 text-muted-foreground" />
        </CardHeader>
        <CardContent className="flex flex-wrap gap-2">
          <Button variant="outline" onClick={() => onOpenSection("connections")}>
            <ExternalLink className="size-3.5" />
            {t("Abrir Conexiones")}
          </Button>
          <Button variant="outline" onClick={() => onOpenSection("models")}>
            <ExternalLink className="size-3.5" />
            {t("Abrir configuración de agentes")}
          </Button>
        </CardContent>
      </Card>

      <Card className="xl:col-span-2" aria-busy={reading}>
        <CardHeader className="flex flex-row items-center justify-between gap-3">
          <CardTitle role="heading" aria-level={3}>
            {t("Diagnóstico")}
          </CardTitle>
          <Button
            variant="outline"
            onClick={() => void readDiagnostics()}
            disabled={reading || !diagnosticsMounted}
          >
            <RefreshCw className="size-3.5" />
            {reading ? t("Consultando…") : t("Reintentar")}
          </Button>
        </CardHeader>
        <CardContent className="space-y-4" data-diagnostics-modules={mountedModules.join(",")}>
          {(unmountedModules.length > 0 || unported.length > 0) && (
            <p role="status" className="text-sm text-destructive">
              {unmountedModules.length > 0 &&
                `${t("Módulos sin montar")}: ${unmountedModules.join(", ")}`}
              {unmountedModules.length > 0 && unported.length > 0 && " · "}
              {unported.length > 0 && `${t("sin portar")}: ${unported.join(", ")}`}
            </p>
          )}

          {!diagnosticsMounted ? (
            <p
              role="alert"
              className="rounded-lg border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive"
            >
              {t(
                "Esta instalación no expone sonda ni registro del módulo gestionado: no se muestra un estado saludable.",
              )}
            </p>
          ) : readError ? (
            <p
              role="alert"
              className="rounded-lg border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive"
            >
              {t(readError)}{" "}
              {t("No se pudo leer el diagnóstico y no se muestra ningún estado anterior.")}
            </p>
          ) : !report ? (
            <p role="status" className="text-sm text-muted-foreground">
              {t("Consultando el módulo gestionado…")}
            </p>
          ) : (
            <div className="space-y-4">
              <div className="flex flex-wrap items-center gap-x-6 gap-y-2 text-sm">
                <span className="flex items-center gap-2">
                  <Badge
                    variant={managedBadgeVariant(managed?.state)}
                    data-managed-state={managed?.state ?? "unknown"}
                  >
                    {t(managedStateLabels[managed?.state ?? ""] ?? managed?.state ?? "—")}
                  </Badge>
                  <span className="text-muted-foreground">{managed?.id ?? "—"}</span>
                </span>
                <dl className="flex flex-wrap gap-x-6 gap-y-1">
                  <div className="flex gap-2">
                    <dt className="text-muted-foreground">PID</dt>
                    <dd data-managed-pid={managed?.pid ?? ""}>
                      {typeof managed?.pid === "number" ? managed.pid : "—"}
                    </dd>
                  </div>
                  <div className="flex gap-2">
                    <dt className="text-muted-foreground">{t("Sondas")}</dt>
                    <dd data-probes={managed?.probes ?? ""}>
                      {typeof managed?.probes === "number" ? managed.probes : "—"}
                    </dd>
                  </div>
                  <div className="flex gap-2">
                    <dt className="text-muted-foreground">{t("Fallos de sonda")}</dt>
                    <dd data-failures={managed?.probe_failures ?? ""}>
                      {typeof managed?.probe_failures === "number"
                        ? managed.probe_failures
                        : "—"}
                    </dd>
                  </div>
                  <div className="flex gap-2">
                    <dt className="text-muted-foreground">{t("Última lectura correcta")}</dt>
                    <dd>{clock(managed?.ready_at, language)}</dd>
                  </div>
                </dl>
              </div>
              {managed?.detail && (
                <p className="rounded-lg border bg-muted/30 p-3 text-sm break-all" data-managed-detail>
                  {managed.detail}
                </p>
              )}
              {events.length > 0 && (
              <ol
                className="max-h-56 space-y-1 overflow-y-auto rounded-lg border bg-muted/30 p-3 font-mono text-xs"
                aria-label={t("Registro del módulo gestionado")}
              >
                {events.map((event, index) => (
                  <li key={index} className="flex flex-wrap gap-2" data-event={event.event ?? ""}>
                    <span className="shrink-0 text-muted-foreground">
                      {clock(event.time, language)}
                    </span>
                    <span className="shrink-0">
                      {t(managedEventLabels[event.event ?? ""] ?? event.event ?? "—")}
                    </span>
                    {event.detail && !routineManagedEvents.has(event.event ?? "") && (
                      <span className="min-w-0 break-all text-muted-foreground">
                        {event.detail}
                      </span>
                    )}
                  </li>
                ))}
              </ol>
              )}
            </div>
          )}
        </CardContent>
      </Card>

      {notice && (
        <p role="status" className="rounded-lg border bg-card p-4 text-sm xl:col-span-2">
          {t(notice)}
        </p>
      )}
      {error && (
        <p
          role="alert"
          className="rounded-lg border border-destructive/30 bg-destructive/5 p-4 text-sm text-destructive xl:col-span-2"
        >
          {t(error)}{" "}
          {t(
            "No se pudo confirmar el cambio. Se muestra el último estado confirmado por la instalación.",
          )}
        </p>
      )}
    </div>
  );
}
