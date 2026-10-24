import { useCallback, useEffect, useRef, useState } from "react";
import {
  AlertTriangle,
  ChevronRight,
  Plus,
  RefreshCw,
  Search,
  X,
} from "lucide-react";
import { ApiError } from "@/app/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select";
import type { Language } from "@/app/language";
import { Empty, Failure, Loading, Notice, Panel, Unsupported } from "./components";
import { Proposals } from "./Proposals";
import {
  conflict,
  failureMessage,
  createTask,
  fetchConversation,
  fetchConversations,
  fetchRevision,
  fetchSources,
  useResource,
} from "./client";
import {
  adapters,
  coverageFlags,
  coverageStatusLabels,
  emptyFilters,
  emptyFocus,
  filtersURL,
  focusURL,
  formatBytes,
  fromDisplayIndex,
  mergePage,
  rangeProblem,
  readFilters,
  readFocus,
  revisionOptions,
  toDisplayIndex,
  wholeRange,
  type ConversationFilters,
  type ConversationView,
} from "./domain";
import { translate } from "./i18n";
import type {
  ConversationRowDTO,
  EventDTO,
  RevisionPageDTO,
  TaskCreateDTO,
  TaskDTO,
} from "./types";
import { ConversationTable, TranscriptEvent, RevisionTasks } from "./ConversationParts";

type Nav = (section: string, view?: string) => void;

/** Bounded follow of one citation: 10 pages of 100 events is the limit this screen
 *  walks on its own; past that the operator keeps loading explicitly. */
const MAX_AUTO_PAGES = 10;

function Detail({
  locale,
  id,
  revision,
  focusEvent,
  onClose,
  onOpenTask,
  onRevision,
  onOpenConfig,
}: {
  locale: Language;
  id: string;
  revision: string;
  focusEvent: string;
  onClose: () => void;
  onOpenTask: (id: string) => void;
  onRevision: (revision: string) => void;
  onOpenConfig: () => void;
}) {
  const t = (value: unknown) => translate(value, locale);
  const loadConversation = useCallback(
    (signal: AbortSignal) => fetchConversation(id, signal),
    [id],
  );
  const conversation = useResource(loadConversation);
  const [selected, setSelected] = useState(revision);
  const [full, setFull] = useState(false);
  const [header, setHeader] = useState<RevisionPageDTO["conversation"]>();
  const [tasks, setTasks] = useState<TaskDTO[]>([]);
  const [rows, setRows] = useState<EventDTO[]>([]);
  const [next, setNext] = useState<string | undefined>();
  const [page, setPage] = useState<{ loading: boolean; error?: unknown }>({ loading: true });
  const [title, setTitle] = useState("");
  const [range, setRange] = useState<{ first: number; last: number } | null>(null);
  const [saving, setSaving] = useState(false);
  const [writeError, setWriteError] = useState("");
  const [receipt, setReceipt] = useState<TaskCreateDTO | null>(null);
  const sequence = useRef(0);
  const autoPages = useRef(0);
  const restoreUntil = useRef(-1);
  const restoreArmed = useRef(false);

  // `GET /api/conversations/{id}` answers {conversation, revisions}: the header and the
  // revision list come from that envelope, never from a flat projection.
  const summary = conversation.data?.conversation;
  const revisions = conversation.data?.revisions ?? [];

  // A deep link without a revision shows the conversation's current one.
  useEffect(() => {
    if (!selected && summary?.revision) setSelected(summary.revision);
  }, [summary, selected]);

  // The summary's count belongs to the conversation's current revision: it is only a
  // fallback while that revision's own page is arriving.
  const eventCount =
    header?.event_count ??
    (selected && selected === summary?.revision ? summary?.coverage?.event_count : undefined);

  const loadFirst = useCallback(
    (signal?: AbortSignal) => {
      if (!selected) return Promise.resolve();
      const seq = ++sequence.current;
      setPage({ loading: true });
      return fetchRevision(id, selected, undefined, !full, signal).then(
        (body) => {
          if (seq !== sequence.current) return;
          setHeader(body.conversation);
          setTasks(body.tasks ?? []);
          setRows(body.events ?? []);
          setNext(body.next_cursor);
          setPage({ loading: false });
          if (restoreUntil.current >= 0) restoreArmed.current = true;
        },
        (error: unknown) => {
          if (seq !== sequence.current || signal?.aborted) return;
          setPage({ loading: false, error });
        },
      );
    },
    [id, selected, full],
  );

  useEffect(() => {
    const controller = new AbortController();
    autoPages.current = 0;
    setRows([]);
    setNext(undefined);
    setReceipt(null);
    void loadFirst(controller.signal);
    return () => controller.abort();
  }, [loadFirst]);

  const loadMore = useCallback(
    async (signal?: AbortSignal) => {
      if (!next) return;
      const seq = sequence.current;
      setPage((value) => ({ ...value, loading: true }));
      try {
        const body = await fetchRevision(id, selected, next, !full, signal);
        if (seq !== sequence.current) return;
        setRows((previous) => mergePage(previous, body.events, (row) => String(row.event_index)));
        setNext(body.next_cursor);
        setPage({ loading: false });
      } catch (error) {
        if (seq === sequence.current && !signal?.aborted) setPage({ loading: false, error });
      }
    },
    [id, selected, next, full],
  );

  const wanted = Number(focusEvent);

  useEffect(() => {
    if (!focusEvent) return;
    const target = document.getElementById(`event-${focusEvent}`);
    target?.scrollIntoView({ block: "center" });
  }, [focusEvent, rows]);

  // One bounded auto-load path for the followed citation and the kept window.
  useEffect(() => {
    if (page.loading) return;
    const cited = Number.isSafeInteger(wanted) && focusEvent ? wanted : -1;
    const kept = restoreArmed.current ? restoreUntil.current : -1;
    const target = Math.max(cited, kept);
    if (target < 0) return;
    const last = rows.length ? (rows[rows.length - 1]?.event_index ?? -1) : -1;
    if (last >= target || !next || autoPages.current >= MAX_AUTO_PAGES) {
      restoreUntil.current = -1;
      restoreArmed.current = false;
      return;
    }
    autoPages.current += 1;
    void loadMore();
  }, [rows, next, loadMore, page.loading, focusEvent, wanted]);

  // The whole conversation is the selection the human can start from; a range only
  // becomes sendable when it exists inside this revision.
  const selection = range ?? wholeRange(eventCount);
  const problem = rangeProblem(selection, eventCount);
  const flags = coverageFlags(header?.coverage);

  async function save() {
    if (problem) {
      setWriteError(problem);
      return;
    }
    setSaving(true);
    setWriteError("");
    try {
      const body = await createTask({
        session_id: id,
        revision: selected,
        first_event: selection.first,
        last_event: selection.last,
        title: title.trim() || undefined,
      });
      setReceipt(body);
      setTitle("");
    } catch (error) {
      setWriteError(failureMessage(error, locale));
      if (conflict(error)) {
        // The revision moved or the range no longer exists: reload instead of keeping
        // a selection the installation already rejected.
        void loadFirst();
      }
    } finally {
      setSaving(false);
    }
  }

  if (conversation.error && !conversation.data) {
    // This route answers a specific `conversation_not_found` for an absent row, so a 404
    // here is that absence; only the product's own 501 is a gap of this release.
    if (conversation.error instanceof ApiError && conversation.error.status === 501)
      return (
        <Unsupported
          locale={locale}
          what={t("el detalle de una conversación")}
          error={conversation.error}
          onRetry={conversation.reload}
        />
      );
    return <Failure locale={locale} error={conversation.error} onRetry={conversation.reload} />;
  }
  if (!conversation.data) return <Loading locale={locale} label="Leyendo la conversación…" />;

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="space-y-1">
          <h2 className="text-lg font-semibold">{summary?.title || summary?.id || id}</h2>
          <p className="break-all text-xs text-muted-foreground">
            {summary?.path || summary?.source_id || ""}
          </p>
          <p className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
            <Badge variant="outline">{summary?.adapter || t("sin adaptador")}</Badge>
            <span>
              {summary?.host_id && summary.host_id !== "local"
                ? summary.host_name || summary.host_id
                : t("este servidor")}
            </span>
            {summary?.source_id && <span>· {t("fuente")} {summary.source_id}</span>}
            {typeof eventCount === "number" && (
              <span>
                · {eventCount} {t("eventos")}
              </span>
            )}
            {flags.bytes !== null && <span>· {formatBytes(flags.bytes, locale)}</span>}
            {header?.coverage?.normalizer_version && (
              <span>· {t("normalizador")} {header.coverage.normalizer_version}</span>
            )}
            {header?.coverage?.redaction_version && (
              <span>· {t("redacción")} {header.coverage.redaction_version}</span>
            )}
            {header?.coverage?.raw_sha256 && (
              <span>· sha256 {String(header.coverage.raw_sha256).slice(0, 12)}</span>
            )}
          </p>
        </div>
        <Button variant="ghost" size="sm" onClick={onClose}>
          {t("Volver a la lista")}
          <X data-icon="inline-end" />
        </Button>
      </div>

      {flags.blocking > 0 && (
        <Notice tone="error">
          <AlertTriangle className="size-4" aria-hidden />
          {t("Esta revisión tiene incidencias de captura que bloquean la evidencia")} ·{" "}
          {flags.blocking} {t("incidencias")}
        </Notice>
      )}
      {flags.partial && flags.blocking === 0 && (
        <Notice tone="warn">
          <AlertTriangle className="size-4" aria-hidden />
          {t(
            "Captura parcial: parte de esta conversación no se pudo leer. No la trates como prueba completa.",
          )}{" "}
          <Badge variant="outline">{t(coverageStatusLabels[flags.status] ?? flags.status)}</Badge>
        </Notice>
      )}
      {header?.coverage?.append_pending && (
        <Notice tone="warn">
          {t("La captura de esta revisión todavía estaba añadiendo eventos al leerla: la evidencia puede crecer.")}
        </Notice>
      )}
      {header?.is_latest === false && (
        <Notice tone="warn">
          {t("Revisión histórica: muestra la captura de esa fecha y no la más reciente.")}
        </Notice>
      )}

      <Panel
        title={t("Revisión y evidencia")}
        actions={
          <>
            <NativeSelect
              aria-label={t("Revisión")}
              value={selected}
              onChange={(event) => {
                setSelected(event.target.value);
                onRevision(event.target.value);
              }}
            >
              {revisionOptions(revisions, selected, locale).map((option) => (
                <NativeSelectOption key={option.value} value={option.value}>
                  {option.label}
                </NativeSelectOption>
              ))}
            </NativeSelect>
            <Button variant="outline" size="sm" onClick={() => void loadFirst()}>
              <RefreshCw data-icon="inline-start" />
              {t("Recargar revisión")}
            </Button>
          </>
        }
      >
        {receipt && (
          <Notice tone="ok">
            <span>
              {receipt.reused
                ? t("La tarea ya existía; se conservó su valoración.")
                : t("Tarea guardada.")}{" "}
              {receipt.title_preserved && t("Se conservó el título de la tarea existente.")}{" "}
              {receipt.snapshot_is_latest === false &&
                t("La revisión ya no es la más reciente; la cita sigue en la revisión elegida.")}
            </span>
            <Button size="sm" variant="outline" onClick={() => onOpenTask(receipt.task.id)}>
              {t("Abrir tarea")}
              <ChevronRight data-icon="inline-end" />
            </Button>
          </Notice>
        )}
        <RevisionTasks locale={locale} tasks={tasks} onOpenTask={onOpenTask} />
      </Panel>

      <Proposals
        locale={locale}
        sessionId={id}
        revision={selected}
        isLatest={header?.is_latest !== false}
        onOpenTask={onOpenTask}
        onOpenConfig={onOpenConfig}
      />

      <Panel title={t("Nueva tarea por rango")}>
        <div className="grid gap-3 sm:grid-cols-[1fr_100px_100px]">
          <div className="space-y-1.5">
            <Label htmlFor="task-title">{t("Nombre de la tarea")}</Label>
            <Input
              id="task-title"
              value={title}
              onChange={(event) => setTitle(event.target.value)}
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="task-start">{t("Inicio")}</Label>
            <Input
              id="task-start"
              type="number"
              min={1}
              max={eventCount ?? undefined}
              value={toDisplayIndex(selection.first)}
              onChange={(event) =>
                setRange({ first: fromDisplayIndex(Number(event.target.value)), last: selection.last })
              }
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="task-end">{t("Fin")}</Label>
            <Input
              id="task-end"
              type="number"
              min={1}
              max={eventCount ?? undefined}
              value={toDisplayIndex(selection.last)}
              onChange={(event) =>
                setRange({ first: selection.first, last: fromDisplayIndex(Number(event.target.value)) })
              }
            />
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-3">
          <Button disabled={saving || problem !== null} onClick={() => void save()}>
            <Plus data-icon="inline-start" />
            {t("Guardar tarea")}
          </Button>
          <Button variant="outline" onClick={() => setRange(wholeRange(eventCount))}>
            {t("Toda la conversación")}
          </Button>
        </div>
        {(problem || writeError) && <Notice tone="error">{writeError || problem}</Notice>}
      </Panel>

      <Panel
        title={t("Mensajes y selección manual")}
        actions={
          <label className="flex items-center gap-2 text-xs">
            <input
              type="checkbox"
              checked={full}
              onChange={(event) => {
                restoreUntil.current = rows.length
                  ? (rows[rows.length - 1]?.event_index ?? -1)
                  : -1;
                restoreArmed.current = false;
                setFull(event.target.checked);
              }}
            />
            {t("Texto completo")}
          </label>
        }
      >
        {page.error !== undefined && <Failure locale={locale} error={page.error} onRetry={() => void loadFirst()} />}
        {rows.length === 0 && page.loading && <Loading locale={locale} label="Leyendo los eventos…" />}
        {rows.length === 0 && !page.loading && page.error === undefined && (
          <Empty title={t("Sin eventos")} />
        )}
        <div className="space-y-3" aria-busy={page.loading}>
          {rows.map((row) => (
            <div key={row.event_index} className="space-y-2">
              <TranscriptEvent
                locale={locale}
                row={row}
                inRange={row.event_index >= selection.first && row.event_index <= selection.last}
                focused={String(row.event_index) === focusEvent}
              />
              <div className="flex flex-wrap gap-2">
                <Button
                  size="xs"
                  variant="outline"
                  aria-label={`${t("Inicio")} · ${t("evento")} ${toDisplayIndex(row.event_index)}`}
                  onClick={() =>
                    setRange({
                      first: row.event_index,
                      last: Math.max(row.event_index, selection.last),
                    })
                  }
                >
                  {t("Inicio")}
                </Button>
                <Button
                  size="xs"
                  variant="outline"
                  aria-label={`${t("Fin")} · ${t("evento")} ${toDisplayIndex(row.event_index)}`}
                  onClick={() =>
                    setRange({
                      first: Math.min(row.event_index, selection.first),
                      last: row.event_index,
                    })
                  }
                >
                  {t("Fin")}
                </Button>
              </div>
            </div>
          ))}
        </div>
        <div className="flex justify-end">
          <Button variant="outline" size="sm" disabled={!next || page.loading} onClick={() => void loadMore()}>
            {t("Cargar más eventos")}
          </Button>
        </div>
      </Panel>
    </div>
  );
}

/** The conversation header of one revision comes from that revision's own page: the
 *  list summary is never reused for another revision's tasks or coverage. */
export function Conversations({
  locale,
  onNavigate,
  onOpenView,
}: {
  locale: Language;
  onNavigate?: Nav;
  onOpenView: (next: ConversationView) => void;
}) {
  const t = (value: unknown) => translate(value, locale);
  const [filters, setFilters] = useState<ConversationFilters>(() => readFilters(location.href));
  const [focus, setFocus] = useState(() => emptyFocus);
  const [rows, setRows] = useState<ConversationRowDTO[]>([]);
  const [next, setNext] = useState<string | undefined>();
  const [state, setState] = useState<{ loading: boolean; error?: unknown }>({ loading: true });
  const sequence = useRef(0);
  const moreAbort = useRef<AbortController | null>(null);
  const loadSources = useCallback((signal: AbortSignal) => fetchSources(signal), []);
  const sources = useResource(loadSources);
  const sourceRows = sources.data ?? [];

  useEffect(() => {
    const sync = () => {
      setFilters(readFilters(location.href));
      setFocus(readFocus(location.href));
    };
    sync();
    window.addEventListener("popstate", sync);
    return () => window.removeEventListener("popstate", sync);
  }, []);

  const load = useCallback(
    (signal?: AbortSignal) => {
      const seq = ++sequence.current;
      setState({ loading: true });
      return fetchConversations(filters, undefined, signal).then(
        (body) => {
          if (seq !== sequence.current) return;
          setRows(body.sessions ?? []);
          setNext(body.next_cursor);
          setState({ loading: false });
        },
        (error: unknown) => {
          if (seq !== sequence.current || signal?.aborted) return;
          setState({ loading: false, error });
        },
      );
    },
    [filters],
  );

  useEffect(() => {
    const controller = new AbortController();
    void load(controller.signal);
    return () => {
      controller.abort();
      moreAbort.current?.abort();
    };
  }, [load]);

  async function loadMore() {
    if (!next) return;
    const seq = sequence.current;
    moreAbort.current?.abort();
    const more = new AbortController();
    moreAbort.current = more;
    setState((value) => ({ ...value, loading: true }));
    try {
      const body = await fetchConversations(filters, next, more.signal);
      if (seq !== sequence.current) return;
      setRows((previous) => mergePage(previous, body.sessions, (row) => row.id));
      setNext(body.next_cursor);
      setState({ loading: false });
    } catch (error) {
      if (seq === sequence.current && !more.signal.aborted) setState({ loading: false, error });
    }
  }

  /** Opening and closing write the shared query string, so a reload keeps the
   *  destination and the filters the operator had. */
  function open(nextFocus: typeof focus) {
    history.replaceState(null, "", focusURL(filtersURL(location.href, filters), nextFocus));
    setFocus(nextFocus);
  }

  function change(patch: Partial<ConversationFilters>) {
    const merged = { ...filters, ...patch };
    setFilters(merged);
    history.replaceState(null, "", focusURL(filtersURL(location.href, merged), emptyFocus));
  }

  function openTask(taskId: string) {
    const url = new URL(location.href);
    url.searchParams.set("task", taskId);
    history.replaceState(null, "", url);
    onNavigate?.("tasks");
  }

  if (focus.conversation)
    return (
      <Detail
        key={focus.conversation}
        locale={locale}
        id={focus.conversation}
        revision={focus.revision}
        focusEvent={focus.event}
        onClose={() => open(emptyFocus)}
        onOpenTask={openTask}
        onRevision={(next) => open({ ...focus, revision: next, event: "" })}
        onOpenConfig={() => onNavigate?.("tasks", "proposals")}
      />
    );

  const filtered = filters.q || filters.source || filters.adapter || filters.status;

  return (
    <div className="space-y-5">
      <Panel
        title={t("Conversaciones capturadas")}
        actions={
          <>
            <Button variant="outline" size="sm" onClick={() => void load()}>
              <RefreshCw data-icon="inline-start" />
              {t("Recargar")}
            </Button>
            <Button variant="outline" size="sm" onClick={() => onOpenView("sources")}>
              {t("Ver fuentes")}
            </Button>
          </>
        }
      >
        <div className="grid gap-3 sm:grid-cols-[2fr_1fr_1fr_1fr]">
          <div className="relative">
            <Search
              size={15}
              aria-hidden
              className="absolute top-1/2 left-3 -translate-y-1/2 text-muted-foreground"
            />
            <Input
              className="pl-9"
              aria-label={t("Buscar conversación")}
              value={filters.q}
              onChange={(event) => change({ q: event.target.value })}
            />
          </div>
          <NativeSelect
            aria-label={t("Fuente")}
            value={filters.source}
            onChange={(event) => change({ source: event.target.value })}
          >
            <NativeSelectOption value="">{t("Todas las fuentes")}</NativeSelectOption>
            {sourceRows.map((source) => (
              <NativeSelectOption key={source.id} value={source.id}>
                {source.adapter} · {source.path}
              </NativeSelectOption>
            ))}
            {filters.source && !sourceRows.some((source) => source.id === filters.source) && (
              <NativeSelectOption value={filters.source}>{filters.source}</NativeSelectOption>
            )}
          </NativeSelect>
          <NativeSelect
            aria-label={t("Motor")}
            value={filters.adapter}
            onChange={(event) => change({ adapter: event.target.value })}
          >
            <NativeSelectOption value="">{t("Todos los motores")}</NativeSelectOption>
            {adapters.map((adapter) => (
              <NativeSelectOption key={adapter} value={adapter}>
                {adapter}
              </NativeSelectOption>
            ))}
          </NativeSelect>
          <NativeSelect
            aria-label={t("Estado")}
            value={filters.status}
            onChange={(event) => change({ status: event.target.value })}
          >
            <NativeSelectOption value="">{t("Cualquier estado")}</NativeSelectOption>
            <NativeSelectOption value="complete">{t(coverageStatusLabels.complete)}</NativeSelectOption>
            <NativeSelectOption value="partial">{t(coverageStatusLabels.partial)}</NativeSelectOption>
            <NativeSelectOption value="error">{t(coverageStatusLabels.error)}</NativeSelectOption>
          </NativeSelect>
        </div>
        {state.error !== undefined && (
          <Failure locale={locale} error={state.error} onRetry={() => void load()} />
        )}
        {/* Keep reloading visible while the previous rows remain. */}
        {state.loading && <Loading locale={locale} label="Leyendo conversaciones…" />}
        {!state.loading && state.error === undefined && rows.length === 0 && (
          <Empty
            title={filtered ? t("Sin coincidencias") : t("Sin conversaciones")}
            action={
              filtered ? (
                <Button variant="outline" onClick={() => change(emptyFilters)}>
                  {t("Quitar filtros")}
                </Button>
              ) : (
                <Button onClick={() => onOpenView("sources")}>{t("Ver fuentes")}</Button>
              )
            }
          />
        )}
        {rows.length > 0 && (
          <div aria-busy={state.loading}>
          <ConversationTable
            locale={locale}
            rows={rows}
            onOpen={(row) =>
              open({ conversation: row.id, revision: row.revision ?? "", event: "" })
            }
          />
          </div>
        )}
        <div className="flex justify-end">
          <Button
            variant="outline"
            size="sm"
            disabled={!next || state.loading}
            onClick={() => void loadMore()}
          >
            {t("Cargar más conversaciones")}
          </Button>
        </div>
      </Panel>
    </div>
  );
}
