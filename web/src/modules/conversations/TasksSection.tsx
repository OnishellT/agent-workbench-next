import { useCallback, useEffect, useRef, useState } from "react";
import {
  AlertTriangle,
  FlaskConical,
  RefreshCw,
  Search,
  X,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  NativeSelect,
  NativeSelectOption,
} from "@/components/ui/native-select";
import { ApiError } from "@/app/api";
import type { Language } from "@/app/language";
import {
  Empty,
  Failure,
  Loading,
  Notice,
  Panel,
  Unsupported,
} from "./components";
import { StateBadge, TaskHistory, TaskTable } from "./TaskParts";
import { ProposalConfig } from "./ProposalConfig";
import {
  conflict,
  failureMessage,
  fetchCases,
  fetchTask,
  fetchTaskHistory,
  fetchTasks,
  setCompletion,
  setLabel,
  setTaskCases,
  useResource,
} from "./client";
import {
  coverageFlags,
  coverageStatusLabels,
  instant,
  mergePage,
  taskFilters,
  taskLabels,
  tasksViews,
  toDisplayIndex,
} from "./domain";
import { translate } from "./i18n";
import type { TaskDTO } from "./types";

type Nav = (section: string, view?: string) => void;

type TasksView = (typeof tasksViews)[number][0];

function tasksViewOf(view: string | undefined): TasksView {
  return tasksViews.some(([id]) => id === view) ? (view as TasksView) : "tasks";
}

function TaskEditor({
  locale,
  id,
  onClose,
  onOpenEvidence,
  onOpenFoundry,
}: {
  locale: Language;
  id: string;
  onClose: () => void;
  onOpenEvidence: (task: TaskDTO, event?: number) => void;
  onOpenFoundry: () => void;
}) {
  const t = (value: unknown) => translate(value, locale);
  const load = useCallback(
    (signal: AbortSignal) => fetchTask(id, signal),
    [id],
  );
  const loaded = useResource(load);
  const loadHistory = useCallback(
    (signal: AbortSignal) => fetchTaskHistory(id, signal),
    [id],
  );
  const history = useResource(loadHistory);
  const [task, setTask] = useState<TaskDTO | null>(null);
  const [label, setLabelValue] = useState("pending");
  const [feedback, setFeedback] = useState("");
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");
  const [chosen, setChosen] = useState<string[]>([]);
  const loadCases = useCallback(
    (signal: AbortSignal) => fetchCases(signal),
    [],
  );
  const caseOptions = useResource(loadCases);

  useEffect(() => {
    if (!loaded.data) return;
    setTask(loaded.data);
    setLabelValue(loaded.data.label ?? "pending");
    setFeedback(loaded.data.feedback ?? "");
    setChosen((loaded.data.cases ?? []).map((row) => row.id));
  }, [loaded.data]);

  /** La selección se guarda como acción explícita: marcar no vincula nada todavía. */
  async function saveCases() {
    if (!task) return;
    setBusy(true);
    setError("");
    try {
      await setTaskCases(task.id, chosen, task.cases_generation ?? 0);
      setNotice(t("Selección guardada."));
      loaded.reload();
    } catch (failure) {
      setError(
        failureMessage(failure, locale),
      );
      if (conflict(failure)) loaded.reload();
    } finally {
      setBusy(false);
    }
  }

  async function complete(completed: boolean) {
    if (!task) return;
    setBusy(true);
    setError("");
    try {
      const receipt = await setCompletion(
        task.id,
        completed,
        task.completion_generation,
      );
      setTask(receipt.task);
      history.reload();
      setLabelValue(receipt.task.label ?? "pending");
      setFeedback(receipt.task.feedback ?? "");
      setNotice(
        receipt.reused
          ? t("El estado ya era ese; no se añadió nada.")
          : completed
            ? t("Finalización confirmada. Ahora puedes valorar el resultado.")
            : t(
                "Tarea reabierta; la valoración vuelve a pendiente y la evidencia derivada se revoca.",
              ),
      );
    } catch (failure) {
      setError(
        failureMessage(failure, locale),
      );
      if (conflict(failure)) {
        loaded.reload();
        history.reload();
      }
    } finally {
      setBusy(false);
    }
  }

  async function saveLabel() {
    if (!task) return;
    setBusy(true);
    setError("");
    try {
      const receipt = await setLabel(
        task.id,
        label,
        feedback,
        task.completion_generation,
      );
      setTask(receipt.task);
      history.reload();
      setLabelValue(receipt.task.label ?? "pending");
      setFeedback(receipt.task.feedback ?? "");
      setNotice(
        receipt.reused
          ? t("La valoración ya era esa; no se añadió nada.")
          : t("Valoración guardada."),
      );
    } catch (failure) {
      setError(
        failureMessage(failure, locale),
      );
      if (conflict(failure)) {
        loaded.reload();
        history.reload();
      }
    } finally {
      setBusy(false);
    }
  }

  if (loaded.error !== undefined && !loaded.data) {
    // This route answers a specific `task_not_found` for an absent row, so a 404 here is
    // that absence; only the product's own 501 is a gap of this release.
    if (loaded.error instanceof ApiError && loaded.error.status === 501)
      return (
        <Unsupported
          locale={locale}
          what={t("una tarea")}
          error={loaded.error}
          onRetry={loaded.reload}
        />
      );
    return (
      <Failure locale={locale} error={loaded.error} onRetry={loaded.reload} />
    );
  }
  if (!task) return <Loading locale={locale} label="Leyendo la tarea…" />;

  const flags = coverageFlags(task.coverage);
  const labelChanged = label !== (task.label ?? "pending");

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="space-y-1">
          <h2 className="text-lg font-semibold">{task.title || task.id}</h2>
          <p className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
            <StateBadge locale={locale} task={task} />
            <span>
              {t("Eventos")} {toDisplayIndex(task.first_event)}–
              {toDisplayIndex(task.last_event)}
            </span>
            <span>
              · {t("Revisión")} {(task.revision || "").slice(0, 12)}
            </span>
            <span>
              · {t("Conversación")} {task.session_id}
            </span>
            {task.source_id && (
              <span>
                · {t("fuente")} {task.source_id}
              </span>
            )}
          </p>
          <p className="text-xs text-muted-foreground">
            {t("Creada")}: {instant(task.created, locale)} ·{" "}
            {t("Último cambio")}: {instant(task.updated, locale)}
            {task.completed && task.completion_confirmed_at
              ? ` · ${t("Finalización confirmada")}: ${instant(task.completion_confirmed_at, locale)}`
              : ""}
          </p>
        </div>
        <Button variant="ghost" size="sm" onClick={onClose}>
          {t("Volver al histórico")}
          <X data-icon="inline-end" />
        </Button>
      </div>

      {notice && <Notice tone="ok">{notice}</Notice>}
      {error && <Notice tone="error">{error}</Notice>}
      {flags.partial && (
        <Notice tone="warn">
          <AlertTriangle className="size-4" aria-hidden />
          {t(
            "Captura parcial: parte de la conversación no se pudo leer. No trates este texto como prueba completa.",
          )}{" "}
          <Badge variant="outline">
            {t(coverageStatusLabels[flags.status] ?? flags.status)}
          </Badge>
        </Notice>
      )}

      <Panel
        title={t("Citas fijadas")}
        actions={
          <Button
            variant="outline"
            size="sm"
            onClick={() => onOpenEvidence(task)}
          >
            {t("Ver evidencia original")}
          </Button>
        }
      >
        {(task.citations ?? []).length === 0 ? (
          <Empty title={t("Sin citas")} />
        ) : (
          (task.citations ?? []).map((citation) => (
            <div
              key={`${citation.event_index}-${citation.quote.slice(0, 12)}`}
              className="space-y-1 border-b pb-2 last:border-0"
            >
              <div className="flex flex-wrap items-center gap-2 text-xs">
                <Badge variant="outline">
                  {toDisplayIndex(citation.event_index)}
                </Badge>
                <Button
                  size="xs"
                  variant="outline"
                  onClick={() => onOpenEvidence(task, citation.event_index)}
                >
                  {t("Seguir la cita")}
                </Button>
              </div>
              <p className="break-words text-sm">{citation.quote}</p>
            </div>
          ))
        )}
      </Panel>

      <Panel
        title={t("Caso de evaluación reproducible")}
      >
        <p className="text-sm text-muted-foreground">
          {t("Los casos reproducibles y los experimentos se preparan en Foundry.")}
        </p>
        <Button variant="outline" onClick={() => onOpenFoundry()}>
          <FlaskConical data-icon="inline-start" />
          {t("Preparar el caso en Foundry")}
        </Button>
        {/* La selección la publica el detalle: recargar no la inventa ni mezcla tareas. */}
        <p className="text-xs text-muted-foreground">
          {t("Casos de evaluación")} · {(task.cases ?? []).length}
        </p>
        {caseOptions.error !== undefined && !caseOptions.data && (
          <Failure
            locale={locale}
            error={caseOptions.error}
            onRetry={caseOptions.reload}
          />
        )}
        {!caseOptions.data && caseOptions.error === undefined && (
          <Loading locale={locale} label="Leyendo los casos…" />
        )}
        {caseOptions.data &&
          ((caseOptions.data.cases ?? []).length === 0 ? (
            null
          ) : (
            <div className="space-y-3">
              {(caseOptions.data.cases ?? []).map((row) => (
                <label className="flex items-start gap-3 text-xs" key={row.id}>
                  <input
                    type="checkbox"
                    className="mt-0.5"
                    checked={chosen.includes(row.id)}
                    disabled={busy}
                    onChange={() =>
                      setChosen((current) =>
                        current.includes(row.id)
                          ? current.filter((item) => item !== row.id)
                          : [...current, row.id],
                      )
                    }
                  />
                  <span>
                    {row.title || row.id}
                    <span className="block text-muted-foreground">
                      {row.domain ?? t("sin dominio")} ·{" "}
                      {row.split ?? t("sin split")} ·{" "}
                      <span className="font-mono">{row.id}</span>
                    </span>
                  </span>
                </label>
              ))}
              <Button
                variant="outline"
                disabled={
                  busy ||
                  chosen.join("|") ===
                    (task.cases ?? []).map((row) => row.id).join("|")
                }
                onClick={() => void saveCases()}
              >
                {t("Guardar selección")}
              </Button>
            </div>
          ))}
      </Panel>

      <Panel title={t("1. Finalización")}>
        <div className="flex flex-wrap items-center gap-3">
          <Button
            variant={task.completed ? "outline" : "default"}
            disabled={busy}
            onClick={() => void complete(!task.completed)}
          >
            {task.completed ? t("Reabrir tarea") : t("Marcar tarea completada")}
          </Button>
          <span className="text-xs text-muted-foreground">
            {t("Generación")}:{" "}
            {typeof task.completion_generation === "number"
              ? task.completion_generation
              : t("sin dato")}
          </span>
        </div>
        {task.completed && (
          <p className="text-xs text-muted-foreground">
            {t(
              "Reabrir retira la valoración, devuelve la tarea a pendiente e invalida la evidencia derivada.",
            )}
          </p>
        )}
      </Panel>

      <Panel title={t("2. Resultado")}>
        {!task.completed && (
          <Notice tone="warn">
            {t("Confirma primero que la tarea está completada.")}
          </Notice>
        )}
        <div className="space-y-1.5">
          <Label htmlFor="task-label">{t("Valoración")}</Label>
          <NativeSelect
            id="task-label"
            value={label}
            disabled={!task.completed}
            onChange={(event) => setLabelValue(event.target.value)}
          >
            {taskLabels.map(([value, text]) => (
              <NativeSelectOption key={value} value={value}>
                {t(text)}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="task-feedback">{t("Qué funcionó o qué falló")}</Label>
          <textarea
            id="task-feedback"
            className="min-h-24 w-full rounded-lg border border-input bg-transparent px-2.5 py-1.5 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-50 dark:bg-input/30"
            value={feedback}
            onChange={(event) => setFeedback(event.target.value)}
          />
        </div>
        {task.completed && labelChanged && (
          <Notice tone="warn">
            {t(
              "Cambiar la etiqueta invalida la evidencia y los candidatos vinculados a la valoración anterior.",
            )}
          </Notice>
        )}
        <Button
          disabled={busy || !task.completed}
          onClick={() => void saveLabel()}
        >
          {t("Guardar valoración")}
        </Button>
      </Panel>

      <Panel title={t("Historial de esta tarea")}>
        {history.error !== undefined ? (
          <Failure
            locale={locale}
            error={history.error}
            onRetry={history.reload}
          />
        ) : (
          <TaskHistory locale={locale} history={history.data} />
        )}
      </Panel>
    </div>
  );
}

/** Histórico de tareas, configuración del análisis y sus propuestas. `view` lo fija el
 *  shell (?section=tasks&view=…) y, sin él, la propia barra de la sección. */
export function TasksSection({
  locale,
  onNavigate,
  view,
}: {
  locale: Language;
  onNavigate?: Nav;
  view?: string;
}) {
  const t = (value: unknown) => translate(value, locale);
  const [current, setCurrent] = useState<TasksView>(() => tasksViewOf(view));
  const [status, setStatus] = useState("all");
  const [q, setQ] = useState("");
  const [open, setOpen] = useState("");
  const [rows, setRows] = useState<TaskDTO[]>([]);
  const [next, setNext] = useState<string | undefined>();
  const [state, setState] = useState<{ loading: boolean; error?: unknown }>({
    loading: true,
  });
  const sequence = useRef(0);
  const moreAbort = useRef<AbortController | null>(null);

  useEffect(() => {
    if (view) setCurrent(tasksViewOf(view));
  }, [view]);

  useEffect(() => {
    const sync = () =>
      setOpen(new URLSearchParams(location.search).get("task") ?? "");
    sync();
    window.addEventListener("popstate", sync);
    return () => window.removeEventListener("popstate", sync);
  }, []);

  function openView(next: TasksView) {
    setCurrent(next);
    onNavigate?.("tasks", next);
  }

  const load = useCallback(
    (signal?: AbortSignal) => {
      const seq = ++sequence.current;
      moreAbort.current?.abort();
      setState({ loading: true });
      return fetchTasks({ status, q }, undefined, signal).then(
        (body) => {
          if (seq !== sequence.current) return;
          setRows(body.tasks ?? []);
          setNext(body.next_cursor);
          setState({ loading: false });
        },
        (error: unknown) => {
          if (seq !== sequence.current || signal?.aborted) return;
          setState({ loading: false, error });
        },
      );
    },
    [status, q],
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
      const body = await fetchTasks({ status, q }, next, more.signal);
      if (seq !== sequence.current) return;
      setRows((previous) => mergePage(previous, body.tasks, (task) => task.id));
      setNext(body.next_cursor);
      setState({ loading: false });
    } catch (error) {
      if (seq === sequence.current && !more.signal.aborted) setState({ loading: false, error });
    }
  }

  function show(id: string) {
    const url = new URL(location.href);
    if (id) url.searchParams.set("task", id);
    else url.searchParams.delete("task");
    history.replaceState(null, "", url);
    if (id) setCurrent("tasks");
    setOpen(id);
  }

  /** Evaluation cases and experiments are prepared in Foundry (its Experimentos page). */
  function openFoundry() {
    onNavigate?.("foundry", "experiments");
  }

  /** A citation is followed to its own conversation and immutable revision; the shell
   *  keeps these parameters while it switches section. */
  function openEvidence(task: TaskDTO, event?: number) {
    const url = new URL(location.href);
    url.searchParams.set("conversation", task.session_id);
    url.searchParams.set("revision", task.revision);
    if (event === undefined) url.searchParams.delete("event");
    else url.searchParams.set("event", String(event));
    history.replaceState(null, "", url);
    onNavigate?.("conversations");
  }

  if (open)
    return (
      <TaskEditor
        key={open}
        locale={locale}
        id={open}
        onClose={() => show("")}
        onOpenEvidence={openEvidence}
        onOpenFoundry={openFoundry}
      />
    );

  return (
    <div className="space-y-5">
      <nav
        className="flex flex-wrap gap-1 border-b"
        aria-label={`${t("Secciones de")} ${t("Tareas")}`}
      >
        {tasksViews.map(([id, label]) => (
          <button
            key={id}
            type="button"
            aria-current={current === id ? "page" : undefined}
            className={`-mb-px border-b-2 px-3 py-2 text-sm ${
              current === id
                ? "border-primary font-medium text-foreground"
                : "border-transparent text-muted-foreground hover:text-foreground"
            }`}
            onClick={() => openView(id as TasksView)}
          >
            {t(label)}
          </button>
        ))}
      </nav>
      {current === "proposals" ? (
        <ProposalConfig locale={locale} />
      ) : (
        <Panel
          title={t("Histórico de tareas")}
          actions={
            <Button variant="outline" size="sm" onClick={() => void load()}>
              <RefreshCw data-icon="inline-start" />
              {t("Recargar")}
            </Button>
          }
        >
          <div className="flex flex-wrap items-center gap-2">
            {taskFilters.map(([value, label]) => (
              <Button
                key={value}
                size="sm"
                variant={status === value ? "secondary" : "ghost"}
                aria-pressed={status === value}
                onClick={() => setStatus(value)}
              >
                {t(label)}
              </Button>
            ))}
            <div className="relative min-w-56 flex-1">
              <Search
                size={15}
                aria-hidden
                className="absolute top-1/2 left-3 -translate-y-1/2 text-muted-foreground"
              />
              <Input
                className="pl-9"
                aria-label={t("Buscar tarea")}
                value={q}
                onChange={(event) => setQ(event.target.value)}
              />
            </div>
          </div>
          {state.error !== undefined && (
            <Failure
              locale={locale}
              error={state.error}
              onRetry={() => void load()}
            />
          )}
          {state.loading && (
            <Loading locale={locale} label="Leyendo tareas…" />
          )}
          {!state.loading && state.error === undefined && rows.length === 0 && (
            <Empty
              title={t("Sin tareas")}
              action={
                <Button
                  variant="outline"
                  onClick={() => onNavigate?.("conversations")}
                >
                  {t("Ver conversaciones")}
                </Button>
              }
            />
          )}
          {rows.length > 0 && (
            <div aria-busy={state.loading}>
              <TaskTable locale={locale} rows={rows} onOpen={show} />
            </div>
          )}
          <div className="flex justify-end">
            <Button
              variant="outline"
              size="sm"
              disabled={!next || state.loading}
              onClick={() => void loadMore()}
            >
              {t("Cargar más tareas")}
            </Button>
          </div>
        </Panel>
      )}
    </div>
  );
}

export default TasksSection;
