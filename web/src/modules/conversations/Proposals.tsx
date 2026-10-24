import { useCallback, useEffect, useRef, useState } from "react";
import { ChevronRight, CircleSlash, Sparkles, X } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import type { Language } from "@/app/language";
import { ApiError } from "@/app/api";
import { Empty, Notice, Panel } from "./components";
import {
  acceptTaskProposal,
  cancelJob,
  conflict,
  failureMessage,
  fetchTaskConfig,
  fetchTaskProposal,
  startTaskProposal,
  useResource,
} from "./client";
import {
  acceptanceProblem,
  engineModelLabel,
  formatUsage,
  proposalActive,
  proposalStatusLabels,
  skippedReasonLabels,
  toDisplayIndex,
} from "./domain";
import { translate } from "./i18n";
import type { ProposalAcceptReceiptDTO, TaskProposalDTO } from "./types";

/** POLL_MS is short enough to watch chunks of one analysis and long enough not to hammer
 *  the installation while a provider works. */
const POLL_MS = 1500;

/** Manual proposals of one immutable revision: the consent of the analysis configuration
 *  gates the action, the job reports its own progress, and acceptance is a separate
 *  explicit choice of the human. Nothing here accepts a suggestion by itself. */
export function Proposals({
  locale,
  sessionId,
  revision,
  isLatest,
  onOpenTask,
  onOpenConfig,
}: {
  locale: Language;
  sessionId: string;
  revision: string;
  isLatest: boolean;
  onOpenTask: (id: string) => void;
  onOpenConfig?: () => void;
}) {
  const t = (value: unknown) => translate(value, locale);
  const loadConfig = useCallback((signal: AbortSignal) => fetchTaskConfig(signal), []);
  const config = useResource(loadConfig);
  const [proposal, setProposal] = useState<TaskProposalDTO | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [jobId, setJobId] = useState("");
  const [chosen, setChosen] = useState<number[]>([]);
  const [receipt, setReceipt] = useState<ProposalAcceptReceiptDTO | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [loadError, setLoadError] = useState("");
  const [notice, setNotice] = useState("");

  const engine = config.data?.config?.provider;
  const diagnostic = config.data?.diagnostics?.provider;
  const ready = diagnostic?.available === true;
  const reason =
    config.data?.diagnostics?.reason ??
    diagnostic?.reason ??
    (config.data ? "" : t("Leyendo el diagnóstico de análisis…"));

  // The pair this panel shows now: an answer for another session or revision is dropped.
  const currentKey = useRef(`${sessionId}\u0000${revision}`);
  currentKey.current = `${sessionId}\u0000${revision}`;
  const localeRef = useRef(locale);
  localeRef.current = locale;

  // One read of the receipt of this revision; the job itself is followed while active.
  // Only a 404 means "no proposal yet"; any other failure is stated, not hidden.
  const loadProposal = useCallback(
    (signal?: AbortSignal) => {
      const key = `${sessionId}\u0000${revision}`;
      const stale = () => signal?.aborted || currentKey.current !== key;
      return fetchTaskProposal(sessionId, revision, signal).then(
        (body) => {
          if (stale()) return null;
          setProposal(body);
          setLoadError("");
          setLoaded(true);
          return body;
        },
        (failure: unknown) => {
          if (stale()) return null;
          setLoaded(true);
          if (failure instanceof ApiError && failure.status === 404) {
            setLoadError("");
            return null;
          }
          setLoadError(
            failureMessage(failure, localeRef.current),
          );
          return null;
        },
      );
    },
    [sessionId, revision],
  );

  useEffect(() => {
    const controller = new AbortController();
    setProposal(null);
    setLoaded(false);
    setLoadError("");
    setChosen([]);
    setReceipt(null);
    void loadProposal(controller.signal);
    return () => controller.abort();
  }, [loadProposal]);

  const active = proposal ? proposalActive(proposal.status) : Boolean(jobId);
  useEffect(() => {
    if (!active) return;
    const controller = new AbortController();
    const timer = window.setInterval(() => {
      void loadProposal(controller.signal);
    }, POLL_MS);
    return () => {
      controller.abort();
      window.clearInterval(timer);
    };
  }, [active, loadProposal]);

  async function propose() {
    setBusy(true);
    setError("");
    setNotice("");
    setReceipt(null);
    try {
      const queued = await startTaskProposal(sessionId, revision);
      setJobId(queued.job_id);
      setNotice(`${t("Propuesta en cola")} · ${queued.job_id}`);
      await loadProposal();
    } catch (failure) {
      setError(failureMessage(failure, locale));
      if (conflict(failure)) await loadProposal();
    } finally {
      setBusy(false);
    }
  }

  async function cancel() {
    if (!proposal) return;
    setBusy(true);
    try {
      const cancelled = await cancelJob(proposal.job_id);
      setNotice(
        `${t("Cancelación solicitada")} · ${t(proposalStatusLabels[cancelled.status] ?? cancelled.status)}`,
      );
      await loadProposal();
    } catch (failure) {
      setError(failureMessage(failure, locale));
    } finally {
      setBusy(false);
    }
  }

  async function accept() {
    if (!proposal || chosen.length === 0) return;
    setBusy(true);
    setError("");
    try {
      const result = await acceptTaskProposal(proposal.job_id, revision, chosen);
      setReceipt(result);
      setChosen([]);
      setNotice(
        `${result.tasks.length} ${t("tareas creadas")}${
          result.skipped.length ? ` · ${result.skipped.length} ${t("omitidas")}` : ""
        }`,
      );
      await loadProposal();
    } catch (failure) {
      setError(failureMessage(failure, locale));
      if (conflict(failure)) await loadProposal();
    } finally {
      setBusy(false);
    }
  }

  const problem = proposal ? acceptanceProblem(proposal) : null;

  return (
    <Panel
      title={t("Propuestas de tareas")}
      actions={
        <>
          {active && (
            <Button variant="outline" size="sm" disabled={busy} onClick={() => void cancel()}>
              <CircleSlash data-icon="inline-start" />
              {t("Cancelar propuesta")}
            </Button>
          )}
          <Button
            size="sm"
            disabled={busy || !ready || !isLatest || active}
            onClick={() => void propose()}
          >
            <Sparkles data-icon="inline-start" />
            {t("Proponer tareas")}
          </Button>
        </>
      }
    >
      <p className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
        <Badge variant={ready ? "secondary" : "destructive"}>
          {ready ? t("análisis disponible") : t("análisis no disponible")}
        </Badge>
        <span>
          {engine
            ? `${engine.kind || t("sin motor")} · ${t(engineModelLabel(engine))}${
                engine.reasoning ? ` · ${engine.reasoning}` : ""
              } · ${engine.timeout}s`
            : t("sin proveedor configurado")}
        </span>
        {reason && <span>· {reason}</span>}
        {onOpenConfig && (
          <Button variant="link" size="xs" onClick={onOpenConfig}>
            {t("Revisar la configuración de análisis")}
          </Button>
        )}
      </p>
      {!isLatest && (
        <Notice tone="warn">
          {t("Esta es una revisión histórica: las propuestas se hacen sobre la revisión actual.")}
        </Notice>
      )}
      {notice && <Notice tone="ok">{notice}</Notice>}
      {error && <Notice tone="error">{error}</Notice>}
      {loadError && <Notice tone="error">{loadError}</Notice>}

      {proposal && (
        <div className="space-y-3">
          <p className="flex flex-wrap items-center gap-2 text-sm">
            <Badge variant={proposal.status === "completed" ? "default" : "secondary"}>
              {t(proposalStatusLabels[proposal.status] ?? proposal.status)}
            </Badge>
            {proposal.automatic && <Badge variant="outline">{t("automática")}</Badge>}
            <span className="text-xs text-muted-foreground">
              {proposal.algorithm} · {t("revisión")} {proposal.revision.slice(0, 10)}
            </span>
            {proposal.progress.total_chunks > 0 && (
              <span className="text-xs text-muted-foreground">
                {t("bloques")} {proposal.progress.completed_chunks}/{proposal.progress.total_chunks}
              </span>
            )}
          </p>
          {proposal.coverage && (
            <p className="text-xs text-muted-foreground">
              {t("cobertura")}: {proposal.coverage.status ?? t("sin dato")} ·{" "}
              {proposal.coverage.event_count ?? 0} {t("eventos")}
              {proposal.coverage.raw_sha256
                ? ` · sha256 ${String(proposal.coverage.raw_sha256).slice(0, 12)}`
                : ""}
            </p>
          )}
          {proposal.inference?.runs?.length > 0 && (
            <div className="space-y-1">
              {proposal.inference.runs.map((run, index) => (
                <p key={`${run.provider}-${index}`} className="text-xs text-muted-foreground">
                  {run.provider} · {run.model}
                  {run.model_provider ? ` · ${run.model_provider}` : ""} · {run.status} ·{" "}
                  {run.seconds.toFixed(1)}s ·{" "}
                  {run.real_inference ? t("inferencia real") : t("sin inferencia real")}
                  {run.turns !== undefined ? ` · ${run.turns} ${t("turnos")}` : ""}
                  {run.tool_calls !== undefined ? ` · ${run.tool_calls} ${t("herramientas")}` : ""}
                  {formatUsage(run.usage) ? ` · ${formatUsage(run.usage)}` : ""}
                  {run.error ? ` · ${run.error}` : ""}
                </p>
              ))}
            </div>
          )}
          {proposal.error && <Notice tone="error">{proposal.error}</Notice>}

          {proposal.suggestions.length > 0 && (
            <div className="space-y-2">
              <p className="flex items-center gap-2 text-sm font-medium">
                {t("Sugerencias")}
                <Badge variant="secondary">{proposal.suggestions.length}</Badge>
              </p>
              {proposal.suggestions.map((suggestion, index) => (
                <label
                  key={`${suggestion.first_event}-${suggestion.last_event}-${index}`}
                  className="flex items-start gap-3 rounded-lg border p-3 text-sm"
                >
                  <input
                    type="checkbox"
                    aria-label={`${t("Aceptar sugerencia")} ${suggestion.title}`}
                    disabled={problem !== null}
                    checked={chosen.includes(index)}
                    onChange={(event) =>
                      setChosen((current) =>
                        event.target.checked
                          ? [...current, index]
                          : current.filter((value) => value !== index),
                      )
                    }
                  />
                  <span className="space-y-1">
                    <span className="block font-medium">{suggestion.title}</span>
                    <span className="block text-xs text-muted-foreground">
                      {t("eventos")} {toDisplayIndex(suggestion.first_event)}–
                      {toDisplayIndex(suggestion.last_event)}
                    </span>
                    {(suggestion.evidence ?? []).map((citation) => (
                      <span
                        key={`${citation.event_index}-${citation.quote.slice(0, 8)}`}
                        className="block break-words text-xs"
                      >
                        <Badge variant="outline">{toDisplayIndex(citation.event_index)}</Badge>{" "}
                        {citation.quote}
                      </span>
                    ))}
                  </span>
                </label>
              ))}
              <div className="flex flex-wrap items-center gap-3">
                <Button
                  size="sm"
                  disabled={busy || chosen.length === 0 || problem !== null}
                  onClick={() => void accept()}
                >
                  {t("Aceptar seleccionadas")}
                </Button>
              </div>
              {problem && <Notice tone="warn">{problem}</Notice>}
            </div>
          )}
          {proposal.skipped.length > 0 && (
            <div className="space-y-1">
              <p className="text-sm font-medium">{t("Omitidas")}</p>
              {proposal.skipped.map((skip, index) => (
                <p key={`${skip.first_event}-${index}`} className="text-xs text-muted-foreground">
                  {t("eventos")} {toDisplayIndex(skip.first_event)}–{toDisplayIndex(skip.last_event)} ·{" "}
                  {t(skippedReasonLabels[skip.reason] ?? skip.reason)}
                  {(skip.task_ids ?? []).map((id) => (
                    <Button key={id} variant="link" size="xs" onClick={() => onOpenTask(id)}>
                      {id.slice(0, 8)}
                      <ChevronRight data-icon="inline-end" />
                    </Button>
                  ))}
                </p>
              ))}
            </div>
          )}
          {loaded && proposal.status === "completed" && proposal.suggestions.length === 0 && (
            <Empty title={t("Sin sugerencias nuevas")} />
          )}
        </div>
      )}

      {receipt && (
        <div className="space-y-2">
          <p className="flex flex-wrap items-center gap-2 text-sm font-medium">
            {t("Aceptadas")}
            <Badge variant="secondary">{receipt.tasks.length}</Badge>
          </p>
          {receipt.tasks.map((task) => (
            <Button
              key={task.id}
              size="sm"
              variant="outline"
              onClick={() => onOpenTask(task.id)}
            >
              {task.title || task.id}
              <ChevronRight data-icon="inline-end" />
            </Button>
          ))}
          {receipt.skipped.length > 0 && (
            <p className="text-xs text-muted-foreground">
              {receipt.skipped.length} {t("omitidas")} ·{" "}
              {receipt.skipped
                .map((skip) => t(skippedReasonLabels[skip.reason] ?? skip.reason))
                .join(" · ")}
            </p>
          )}
          <Button variant="ghost" size="sm" onClick={() => setReceipt(null)}>
            {t("Ocultar resultado")}
            <X data-icon="inline-end" />
          </Button>
        </div>
      )}
    </Panel>
  );
}
