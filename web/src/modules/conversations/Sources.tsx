import { useCallback, useEffect, useRef, useState } from "react";
import { AlertTriangle, FolderOpen, Plus, RefreshCw, Search } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select";
import type { Language } from "@/app/language";
import { Empty, Failure, FormDialog, Loading, Notice, Panel, Unsupported } from "./components";
import {
  addSource,
  conflict,
  failureMessage,
  discoverSources,
  fetchJob,
  fetchSources,
  notServed,
  startScan,
  updateSource,
  useResource,
} from "./client";
import {
  adapters,
  coverageFlags,
  coverageStatusLabels,
  formatBytes,
  instant,
  jobStatusLabels,
  scanStatusLabels,
  sourceName,
} from "./domain";
import { translate } from "./i18n";
import type {
  CaptureIssueDTO,
  DiscoverDTO,
  DiscoveredSourceDTO,
  JobDTO,
  SourceDTO,
} from "./types";


/** Issues of one capture, exactly as the installation reported them. A count above the
 *  shown ones is stated, never dropped silently. */
function IssueList({ locale, issues }: { locale: Language; issues: CaptureIssueDTO[] }) {
  const t = (value: unknown) => translate(value, locale);
  if (!issues.length) return null;
  return (
    <div className="space-y-2">
      {issues.slice(0, 20).map((issue, index) => (
        <div key={`${issue.file_name ?? "issue"}-${index}`} className="text-xs">
          <p className="break-all">{issue.file_name || t("Fuente")}</p>
          <p className="text-muted-foreground">
            {issue.message || issue.reason || t("No se pudo capturar este registro.")}
          </p>
        </div>
      ))}
      {issues.length > 20 && (
        <p className="text-xs text-muted-foreground">
          {t("Se muestran 20 de")} {issues.length} {t("incidencias")}
        </p>
      )}
    </div>
  );
}

function AdapterSelect({
  id,
  value,
  onChange,
}: {
  id: string;
  value: string;
  onChange: (next: string) => void;
}) {
  return (
    <NativeSelect id={id} value={value} onChange={(event) => onChange(event.target.value)}>
      {adapters.map((adapter) => (
        <NativeSelectOption key={adapter} value={adapter}>
          {adapter}
        </NativeSelectOption>
      ))}
    </NativeSelect>
  );
}

/** One capture job: the screen follows the queue the installation returned instead of
 *  pretending the scan finished when the request returned. */
function ScanJob({
  locale,
  job,
  onHide,
}: {
  locale: Language;
  job: JobDTO;
  onHide: () => void;
}) {
  const t = (value: unknown) => translate(value, locale);
  const active = job.status === "queued" || job.status === "running";
  return (
    <Panel
      title={t("Captura en curso")}
      actions={
        <>
          <Badge variant={active ? "secondary" : job.status === "succeeded" ? "default" : "destructive"}>
            {t(jobStatusLabels[job.status] ?? job.status)}
          </Badge>
          <Button size="sm" variant="ghost" onClick={onHide}>
            {t("Ocultar")}
          </Button>
        </>
      }
    >
      <p className="text-xs text-muted-foreground">
        {job.subject || job.job_id} · {instant(job.updated, locale)}
      </p>
      {job.progress && (
        <p className="text-sm">
          {t("Archivos")}: {job.progress.files ?? 0} · {t("Cambios")}: {job.progress.changed ?? 0} ·{" "}
          {t("Errores")}: {job.progress.errors ?? 0}
          {job.progress.max_files ? ` · ${t("tope de archivos")}: ${job.progress.max_files}` : ""}
        </p>
      )}
      {job.progress?.current_file && (
        <p className="break-all text-xs text-muted-foreground">{job.progress.current_file}</p>
      )}
      {job.progress?.limited && (
        <Notice tone="warn">
          {t("Se alcanzó el presupuesto de tiempo o archivos: la captura quedó incompleta y no se presenta como completa.")}
        </Notice>
      )}
      {job.result && (
        <div className="space-y-2">
          <p className="text-sm">
            {t("Sesiones capturadas")}: {job.result.sessions ?? t("sin dato")}
            {typeof job.result.coverage?.issue_count === "number" && (
              <> · {job.result.coverage.issue_count} {t("incidencias")}</>
            )}
          </p>
          {job.result.coverage?.status && (
            <Badge variant={job.result.coverage.status === "partial" ? "destructive" : "secondary"}>
              {t(coverageStatusLabels[job.result.coverage.status] ?? job.result.coverage.status)}
            </Badge>
          )}
          <IssueList locale={locale} issues={job.result.coverage?.issues ?? []} />
        </div>
      )}
      {job.error && <Notice tone="error">{job.error}</Notice>}
    </Panel>
  );
}

/** Consecutive failed reads of the scan job before the screen stops and asks for a retry. */
const JOB_RETRIES = 5;

export function Sources({ locale }: { locale: Language }) {
  const t = (value: unknown) => translate(value, locale);
  const [scanSeq, setScanSeq] = useState(0);
  const load = useCallback((signal: AbortSignal) => fetchSources(signal), [scanSeq]);
  const sources = useResource(load);
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [jobId, setJobId] = useState("");
  const [jobRetry, setJobRetry] = useState(0);
  const [jobStalled, setJobStalled] = useState(false);
  const localeRef = useRef(locale);
  localeRef.current = locale;
  const [job, setJob] = useState<JobDTO | null>(null);
  const [discovered, setDiscovered] = useState<DiscoverDTO | null>(null);
  const [addOpen, setAddOpen] = useState(false);
  const [discoverOpen, setDiscoverOpen] = useState(false);
  const [path, setPath] = useState("");
  const [adapter, setAdapter] = useState<string>(adapters[0]);
  const [roots, setRoots] = useState<DiscoveredSourceDTO[]>([{ path: "", adapter: adapters[0] }]);
  const [relink, setRelink] = useState<SourceDTO | null>(null);
  const [relinkPath, setRelinkPath] = useState("");
  const [relinkAdapter, setRelinkAdapter] = useState<string>(adapters[0]);

  // One timer per queued job, stopped at its terminal status; the list reloads then so
  // the screen shows what the capture really stored.
  useEffect(() => {
    if (!jobId) return;
    const controller = new AbortController();
    let timer: number | undefined;
    let failures = 0;
    async function tick() {
      try {
        const body = await fetchJob(jobId, controller.signal);
        if (controller.signal.aborted) return;
        failures = 0;
        setJobStalled(false);
        setJob(body);
        if (body.status === "queued" || body.status === "running") {
          timer = window.setTimeout(() => void tick(), 1500);
        } else {
          setScanSeq((value) => value + 1);
        }
      } catch (failure) {
        if (controller.signal.aborted) return;
        // A transient error must not end the follow-up: retry with backoff, and after
        // a few attempts state the failure and offer an explicit retry.
        failures += 1;
        if (failures < JOB_RETRIES) {
          timer = window.setTimeout(() => void tick(), Math.min(1500 * 2 ** failures, 15000));
          return;
        }
        setJobStalled(true);
        setError(failureMessage(failure, localeRef.current));
      }
    }
    void tick();
    return () => {
      controller.abort();
      if (timer) window.clearTimeout(timer);
    };
  }, [jobId, jobRetry]);

  function report(message: string, failure?: string) {
    setNotice(message);
    setError(failure ?? "");
  }

  async function scan(sourceId?: string) {
    setBusy(true);
    setError("");
    try {
      const queued = await startScan(sourceId);
      setJobId(queued.job_id);
      setJob(null);
      setNotice(`${t("Captura solicitada")} · ${queued.status}`);
    } catch (failure) {
      setNotice("");
      setError(
        conflict(failure)
          ? `${failure instanceof Error ? failure.message : ""} ${t("Activa la fuente y vuelve a escanear.")}`
          : failureMessage(failure, locale),
      );
    } finally {
      setBusy(false);
    }
  }

  async function toggle(row: SourceDTO, enabled: boolean) {
    setBusy(true);
    try {
      const receipt = await updateSource(row.id, { enabled });
      report(
        `${enabled ? t("Fuente activada.") : t("Fuente pausada.")}${
          receipt.revoked ? ` ${t("Se revocó la evidencia en vuelo de esa fuente.")}` : ""
        }`,
      );
      sources.reload();
    } catch (failure) {
      report("", failureMessage(failure, locale));
    } finally {
      setBusy(false);
    }
  }

  async function submitAdd() {
    if (!path.trim()) {
      setError(t("Escribe la ruta de la fuente."));
      return;
    }
    setBusy(true);
    try {
      await addSource({ path: path.trim(), adapter });
      setAddOpen(false);
      setPath("");
      report(t("Fuente registrada."));
      sources.reload();
    } catch (failure) {
      setError(failureMessage(failure, locale));
    } finally {
      setBusy(false);
    }
  }

  async function submitDiscover() {
    const chosen = roots.filter((row) => row.path.trim());
    if (!chosen.length) {
      setError(t("Escribe al menos una raíz que quieras revisar."));
      return;
    }
    setBusy(true);
    try {
      setDiscovered(await discoverSources(chosen.map((row) => ({ ...row, path: row.path.trim() }))));
      setError("");
      // The candidates are shown in the section, not behind the dialog: a successful
      // detection closes it so the operator sees what was found.
      setDiscoverOpen(false);
    } catch (failure) {
      setError(failureMessage(failure, locale));
    } finally {
      setBusy(false);
    }
  }

  async function register(candidate: DiscoveredSourceDTO) {
    setBusy(true);
    try {
      await addSource({ path: candidate.path, adapter: candidate.adapter });
      report(`${t("Fuente registrada.")} ${candidate.path}`);
      sources.reload();
    } catch (failure) {
      setError(failureMessage(failure, locale));
    } finally {
      setBusy(false);
    }
  }

  async function submitRelink() {
    if (!relink) return;
    const nextPath = relinkPath.trim();
    if (nextPath === relink.path && relinkAdapter === relink.adapter) {
      setError(t("Escribe una ruta nueva o elige otro adaptador."));
      return;
    }
    setBusy(true);
    try {
      const receipt =
        nextPath && nextPath !== relink.path
          ? await updateSource(relink.id, { path: nextPath })
          : await updateSource(relink.id, { adapter: relinkAdapter });
      setRelink(null);
      report(
        `${t("Fuente actualizada; sus identificadores y decisiones se conservan.")}${
          receipt.revoked ? ` ${t("Se revocó la evidencia en vuelo de esa fuente.")}` : ""
        }`,
      );
      sources.reload();
    } catch (failure) {
      report("", failureMessage(failure, locale));
    } finally {
      setBusy(false);
    }
  }

  if (sources.error !== undefined && !sources.data) {
    if (notServed(sources.error))
      return (
        <Unsupported
          locale={locale}
          what={t("las fuentes de conversaciones")}
          error={sources.error}
          onRetry={sources.reload}
        />
      );
    return <Failure locale={locale} error={sources.error} onRetry={sources.reload} />;
  }

  const rows = sources.data ?? [];

  return (
    <div className="space-y-5">
      <Panel
        title={t("Carpetas y archivos")}
        actions={
          <>
            <Button
              variant="outline"
              size="sm"
              onClick={() => {
                setError("");
                setDiscoverOpen(true);
              }}
            >
              <Search data-icon="inline-start" />
              {t("Detectar fuentes")}
            </Button>
            <Button variant="outline" size="sm" disabled={busy || job?.status === "running"} onClick={() => void scan()}>
              <RefreshCw data-icon="inline-start" />
              {t("Escanear ahora")}
            </Button>
            <Button
              size="sm"
              onClick={() => {
                setError("");
                setAddOpen(true);
              }}
            >
              <Plus data-icon="inline-start" />
              {t("Añadir fuente")}
            </Button>
          </>
        }
      >
        {notice && <Notice tone="ok">{notice}</Notice>}
        {error && <Notice tone="error">{error}</Notice>}
        {jobStalled && (
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              setError("");
              setJobStalled(false);
              setJobRetry((value) => value + 1);
            }}
          >
            {t("Reintentar")}
          </Button>
        )}
        {sources.loading && !sources.data && <Loading locale={locale} label="Leyendo las fuentes…" />}
        {!sources.loading && rows.length === 0 && (
          <Empty
            title={t("Sin fuentes")}
            action={
              <Button onClick={() => setAddOpen(true)}>
                <Plus data-icon="inline-start" />
                {t("Añadir fuente")}
              </Button>
            }
          />
        )}
        {rows.map((row) => {
          const flags = coverageFlags(row.coverage);
          return (
            <div key={row.id} className="flex flex-wrap items-start justify-between gap-3 border-b pb-3 last:border-0">
              <div className="min-w-0 space-y-1">
                <p className="flex items-center gap-2 text-sm font-medium">
                  <FolderOpen size={16} aria-hidden className="shrink-0 text-muted-foreground" />
                  <span className="break-all">{sourceName(row)}</span>
                </p>
                <p className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                  <Badge variant="outline">{row.adapter}</Badge>
                  <Badge variant={row.enabled ? "secondary" : "destructive"}>
                    {row.enabled ? t("activa") : t("pausada")}
                  </Badge>
                  <span>
                    {t("alta")}: {instant(row.created, locale)}
                  </span>
                  <span>
                    {t("última captura")}: {instant(row.last_capture, locale)}
                  </span>
                  {row.host_id && row.host_id !== "local" && (
                    <span>{row.host_name || row.host_id}</span>
                  )}
                </p>
                <p className="flex flex-wrap items-center gap-2 text-xs">
                  <Badge variant={flags.partial ? "destructive" : "secondary"}>
                    {t(coverageStatusLabels[flags.status] ?? flags.status)}
                  </Badge>
                  {flags.events !== null && (
                    <span className="text-muted-foreground">
                      {flags.events} {t("eventos")}
                    </span>
                  )}
                  {flags.bytes !== null && (
                    <span className="text-muted-foreground">{formatBytes(flags.bytes, locale)}</span>
                  )}
                  {typeof row.coverage?.issue_count === "number" && (
                    <span>{flags.issues} {t("incidencias")}</span>
                  )}
                  {row.last_scan && (
                    <span className="text-muted-foreground">
                      {t("último escaneo")}: {t(
                        scanStatusLabels[row.last_scan.status ?? ""] ?? row.last_scan.status ?? "sin dato",
                      )} ·{" "}
                      {row.last_scan.files ?? 0} {t("archivos")}
                      {row.last_scan.limited ? ` · ${t("limitado")}` : ""}
                    </span>
                  )}
                </p>
                <IssueList locale={locale} issues={row.coverage?.issues ?? []} />
                {row.error && (
                  <p className="break-words text-xs text-destructive">{row.error}</p>
                )}
              </div>
              <div className="flex flex-wrap gap-2">
                <Button
                  size="sm"
                  variant="outline"
                  disabled={busy}
                  onClick={() => {
                    setRelink(row);
                    setRelinkPath(row.path);
                    setRelinkAdapter(row.adapter);
                    setError("");
                  }}
                >
                  <FolderOpen data-icon="inline-start" />
                  {t("Cambiar ruta")}
                </Button>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={busy || !row.enabled}
                  onClick={() => void scan(row.id)}
                >
                  {t("Escanear")}
                </Button>
                <Button size="sm" variant={row.enabled ? "ghost" : "default"} disabled={busy} onClick={() => void toggle(row, !row.enabled)}>
                  {row.enabled ? t("Pausar") : t("Activar")}
                </Button>
              </div>
            </div>
          );
        })}
      </Panel>

      {job && <ScanJob locale={locale} job={job} onHide={() => setJobId("")} />}

      {discovered && (
        <Panel title={t("Raíces detectadas")}>
          {(discovered.candidates ?? []).length > 0 ? (
            (discovered.candidates ?? []).map((candidate) => (
              <div key={`${candidate.adapter}-${candidate.path}`} className="flex flex-wrap items-center justify-between gap-2 border-b pb-2 last:border-0">
                <div className="min-w-0">
                  <p className="break-all text-sm">{candidate.path}</p>
                  <p className="text-xs text-muted-foreground">
                    {candidate.adapter}
                    {typeof candidate.file_count === "number" && ` · ${candidate.file_count} ${t("archivos")}`}
                  </p>
                </div>
                <Button size="sm" variant="outline" disabled={busy} onClick={() => void register(candidate)}>
                  {t("Registrar fuente")}
                </Button>
              </div>
            ))
          ) : (
            <Empty title={t("No se encontraron conversaciones en esas raíces.")} />
          )}
          {(discovered.unsupported ?? []).length > 0 && (
            <div className="space-y-1">
              <p className="flex items-center gap-2 text-sm font-medium">
                <AlertTriangle size={14} aria-hidden />
                {t("Herramientas no soportadas")}
              </p>
              {(discovered.unsupported ?? []).map((row) => (
                <p key={`${row.kind}-${row.path}`} className="break-all text-xs text-muted-foreground">
                  {row.kind || t("Fuente")} · {row.path}
                  {row.reason ? ` · ${row.reason}` : ""}
                </p>
              ))}
            </div>
          )}
          <IssueList locale={locale} issues={discovered.issues ?? []} />
        </Panel>
      )}

      <FormDialog
        open={addOpen}
        locale={locale}
        title="Añadir fuente"
        error={error}
        busy={busy}
        submitLabel="Registrar fuente"
        onSubmit={() => void submitAdd()}
        onClose={() => setAddOpen(false)}
      >
        <div className="space-y-1.5">
          <Label htmlFor="source-path">{t("Ruta o archivo de la fuente")}</Label>
          <Input
            id="source-path"
            value={path}
            onChange={(event) => setPath(event.target.value)}
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="source-adapter">{t("Adaptador")}</Label>
          <AdapterSelect id="source-adapter" value={adapter} onChange={setAdapter} />
        </div>
      </FormDialog>

      <FormDialog
        open={discoverOpen}
        locale={locale}
        title="Detectar fuentes"
        error={error}
        busy={busy}
        submitLabel="Detectar"
        onSubmit={() => void submitDiscover()}
        onClose={() => setDiscoverOpen(false)}
      >
        {roots.map((root, index) => (
          <div key={index} className="grid gap-2 sm:grid-cols-[1fr_auto_auto]">
            <Input
              aria-label={`${t("Raíz")} ${index + 1}`}
              value={root.path}
              onChange={(event) =>
                setRoots(roots.map((row, at) => (at === index ? { ...row, path: event.target.value } : row)))
              }
            />
            <AdapterSelect
              id={`root-adapter-${index}`}
              value={root.adapter}
              onChange={(next) =>
                setRoots(roots.map((row, at) => (at === index ? { ...row, adapter: next } : row)))
              }
            />
            <Button
              variant="ghost"
              size="sm"
              disabled={roots.length === 1}
              onClick={() => setRoots(roots.filter((_, at) => at !== index))}
            >
              {t("Quitar")}
            </Button>
          </div>
        ))}
        <Button variant="outline" size="sm" onClick={() => setRoots([...roots, { path: "", adapter: adapters[0] }])}>
          <Plus data-icon="inline-start" />
          {t("Añadir raíz")}
        </Button>
      </FormDialog>

      <FormDialog
        open={relink !== null}
        locale={locale}
        title="Cambiar ruta"
        error={error}
        busy={busy}
        submitLabel="Guardar cambio"
        onSubmit={() => void submitRelink()}
        onClose={() => setRelink(null)}
      >
        <div className="space-y-1.5">
          <Label htmlFor="relink-path">{t("Nueva ruta")}</Label>
          <Input id="relink-path" value={relinkPath} onChange={(event) => setRelinkPath(event.target.value)} />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="relink-adapter">{t("Adaptador")}</Label>
          <AdapterSelect id="relink-adapter" value={relinkAdapter} onChange={setRelinkAdapter} />
        </div>
        {relink && relinkAdapter !== relink.adapter && (
          <Notice tone="warn">
            {t("Cambiar el adaptador reprocesa y crea una revisión nueva.")}
          </Notice>
        )}
      </FormDialog>
    </div>
  );
}
