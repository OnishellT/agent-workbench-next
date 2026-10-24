import { useEffect, useState } from "react";
import { ChevronRight, Plus, Sparkles } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Checkbox } from "@/components/ui/checkbox";
import { type Row, api, date, episode } from "@/api";
import { Paginator } from "@/components/forms";
import { Code, Loading, Notice, Status } from "@/components/primitives";
import { JobRows } from "@/components/tables";
import type { Modal, Props } from "@/lib/types";

export function SessionEditor({ modal, p }: { modal: Modal; p: Props }) {
  const [s, setS] = useState<Row | null>(null),
    [error, setError] = useState(""),
    [revision, setRevision] = useState(modal.revision || ""),
    [start, setStart] = useState(0),
    [end, setEnd] = useState(0),
    [title, setTitle] = useState(""),
    [page, setPage] = useState(0),
    [human, setHuman] = useState(true),
    [saved, setSaved] = useState("");
  const proposalJobs = p.data.jobs.filter(
    (j) =>
      j.kind === "propose_tasks" &&
      j.subject === modal.id &&
      (!s?.revision ||
        !j.payload?.expected_revision ||
        j.payload.expected_revision === s.revision),
  );
  const proposalBusy = proposalJobs.some((j) =>
    ["queued", "running"].includes(j.status),
  );
  const completedProposal = proposalJobs
    .filter((j) => j.status === "succeeded")
    .map((j) => j.id)
    .join(",");
  useEffect(() => {
    if (!s?.revision || !completedProposal) return;
    let current = true;
    api("/api/episodes?session_id=" + encodeURIComponent(modal.id!))
      .then((result) => {
        if (current)
          setS((previous) =>
            previous
              ? {
                  ...previous,
                  episodes: result.episodes
                    .filter((e: Row) => e.revision === previous.revision)
                    .map(episode),
                }
              : previous,
          );
      })
      .catch((e) => setError(e.message));
    return () => {
      current = false;
    };
  }, [completedProposal, s?.revision, modal.id]);
  useEffect(() => {
    let ok = true;
    setS(null);
    api(
      "/api/session?id=" +
        encodeURIComponent(modal.id!) +
        (revision ? "&revision=" + encodeURIComponent(revision) : ""),
    )
      .then((r) => {
        if (ok) {
          setS(r);
          setStart(0);
          setEnd(Math.max(0, r.events.length - 1));
          setPage(0);
        }
      })
      .catch((e) => setError(e.message));
    return () => {
      ok = false;
    };
  }, [modal.id, revision]);
  if (error) return <Notice tone="error">{error}</Notice>;
  if (!s) return <Loading />;
  const liveSession = p.data.sessions.find(
    (row) => row.id === s.id && row.revision === s.revision,
  );
  const receipt = liveSession?.task_proposal || s.task_proposal;
  const rows = s.events
    .map((e: Row, i: number) => ({ ...e, index: i }))
    .filter(
      (e: Row) => !human || ["user", "assistant", "human"].includes(e.role),
    );
  return (
    <div className="space-y-5">
      <div className="step-section">
        <h3 className="text-sm font-semibold mb-2">Proponer tareas</h3>
        <div className="btn-wrap">
          <Button
            disabled={proposalBusy || p.busy}
            onClick={() =>
              p.run(
                "session.propose_tasks",
                { id: s.id, expected_revision: s.revision },
                "Propuesta de tareas en cola.",
              )
            }
          >
            <Sparkles />
            {proposalBusy
              ? "Analizando conversación…"
              : "Proponer tareas con " +
                (p.data.config.provider?.kind === "pi"
                  ? "Pi"
                  : p.data.config.provider?.kind === "codex"
                    ? "Codex"
                    : "el agente")}
          </Button>
          <span className="meta">
            {p.data.config.provider?.kind === "command"
              ? "Adaptador command: " +
                (p.data.config.provider?.command || []).join(" ")
              : (p.data.config.provider?.model || "Modelo configurado") +
                " · " +
                (p.data.config.provider?.reasoning || "medium")}{" "}
            · consume cuota
          </span>
        </div>
        {p.data.config.auto_tasks && (
          <p className="form-help mt-3">
            Automático tras {p.data.config.task_idle_seconds ?? 120} s sin
            actividad.
          </p>
        )}
        {receipt && (
          <div className="rounded-lg border p-3 mt-4">
            <div className="btn-wrap">
              <span className="text-xs font-medium">Última propuesta</span>
              <Status value={receipt.status} />
              <span className="meta">
                {receipt.created_count ?? (Array.isArray(receipt.created) ? receipt.created.length : 0)}{" "}
                tareas nuevas ·{" "}
                {receipt.skipped_count ?? (Array.isArray(receipt.skipped) ? receipt.skipped.length : 0)}{" "}
                objetivos ya representados
              </span>
            </div>
            {receipt.progress?.total_chunks > 0 && (
              <p className="meta mt-2">
                Bloques analizados: {receipt.progress.completed_chunks} de{" "}
                {receipt.progress.total_chunks}
              </p>
            )}
            {receipt.error && (
              <p className="text-xs text-destructive mt-2">{receipt.error}</p>
            )}
            <p className="meta mt-2">
              {date(
                receipt.finished_at || receipt.updated_at || receipt.started_at,
              )}
            </p>
          </div>
        )}
        {!!proposalJobs.length && (
          <div className="mt-4">
            <JobRows
              data={{ ...p.data, jobs: proposalJobs.slice(0, 2) }}
              run={p.run}
              open={p.open}
            />
          </div>
        )}
      </div>
      <div className="flex flex-wrap gap-3 items-center">
        <label className="text-xs muted" htmlFor="revision">
          Revisión
        </label>
        <select
          id="revision"
          className="native-select flex-1 min-w-48"
          value={s.revision}
          onChange={(e) => setRevision(e.target.value)}
        >
          {s.revisions?.map((r: Row) => (
            <option key={r.id} value={r.id}>
              {date(r.created)} · {r.episode_count} tareas · {r.id.slice(0, 8)}
            </option>
          ))}
        </select>
        <Status value={s.completion || "unknown"} />
      </div>
      {!s.is_latest && (
        <Notice tone="warning">
          Revisión histórica. Selecciona otra fecha para ver una captura más
          reciente.
        </Notice>
      )}
      <div>
        <h3 className="text-sm font-semibold mb-3">Tareas de esta revisión</h3>
        {!s.episodes?.length && (
          <p className="text-sm muted">
            Pide una propuesta al agente o crea una tarea manualmente.
          </p>
        )}
      </div>
      {!!s.episodes?.length && (
        <div className="flex flex-wrap gap-2">
          {s.episodes.map((e: Row) => (
            <Button
              key={e.id}
              variant="outline"
              size="sm"
              onClick={() => p.open({ kind: "task", row: episode(e) })}
            >
              {e.title}
              <ChevronRight />
            </Button>
          ))}
        </div>
      )}
      {s.historical_episode_count > 0 && (
        <p className="meta">
          {s.historical_episode_count} tareas en otras revisiones.
        </p>
      )}
      <details className="disclosure">
        <summary>Mensajes y selección manual</summary>
        <div className="space-y-4 mt-4">
          <div className="sticky-selection">
            <div className="grid sm:grid-cols-[1fr_100px_100px] gap-3 mb-3">
              <label className="text-xs">
                Nombre de la tarea
                <Input
                  className="mt-2"
                  value={title}
                  onChange={(e) => setTitle(e.target.value)}
                  placeholder="Por ejemplo, corregir la persistencia"
                />
              </label>
              <label className="text-xs">
                Inicio
                <Input
                  className="mt-2"
                  type="number"
                  min={1}
                  max={s.events.length}
                  value={start + 1}
                  onChange={(e) => setStart(Number(e.target.value) - 1)}
                />
              </label>
              <label className="text-xs">
                Fin
                <Input
                  className="mt-2"
                  type="number"
                  min={1}
                  max={s.events.length}
                  value={end + 1}
                  onChange={(e) => setEnd(Number(e.target.value) - 1)}
                />
              </label>
            </div>
            <div className="btn-wrap">
              <Button
                disabled={
                  !title.trim() ||
                  start < 0 ||
                  end < start ||
                  end >= s.events.length ||
                  p.busy
                }
                onClick={async () => {
                  const r = await p.run(
                    "episode.create",
                    {
                      session_id: s.id,
                      first_event: start,
                      last_event: end,
                      title: title.trim(),
                      expected_revision: s.revision,
                    },
                    "Tarea guardada.",
                  );
                  if (r) {
                    setSaved(
                      r.reused
                        ? "La tarea ya existía; se conservó su valoración."
                        : "Tarea guardada.",
                    );
                    setTitle("");
                    setS({
                      ...s,
                      episodes: [
                        ...(s.episodes || []).filter((e: Row) => e.id !== r.id),
                        episode(r),
                      ],
                    });
                  }
                }}
              >
                <Plus />
                Guardar tarea
              </Button>
              <Button
                variant="outline"
                onClick={() => {
                  setStart(0);
                  setEnd(s.events.length - 1);
                }}
              >
                Toda la conversación
              </Button>
              <span className="meta">
                {Math.max(0, end - start + 1)} eventos seleccionados
              </span>
            </div>
            {saved && (
              <p className="text-xs text-ok mt-3" role="status">
                {saved}
              </p>
            )}
          </div>
          <label className="flex items-center gap-2 text-xs">
            <Checkbox
              checked={human}
              onCheckedChange={(v) => {
                setHuman(!!v);
                setPage(0);
              }}
            />
            Sólo mensajes humanos y del agente
          </label>
          <div className="space-y-3">
            {rows.slice(page * 25, (page + 1) * 25).map((e: Row) => (
              <article
                key={e.index}
                className={
                  "event-box " +
                  (e.index >= start && e.index <= end ? "chosen" : "")
                }
              >
                <div className="flex justify-between items-center gap-3 mb-3">
                  <div className="btn-wrap">
                    <span className="pill">{e.index + 1}</span>
                    <strong className="text-xs">
                      {e.role || e.type || "Evento"}
                    </strong>
                    <span className="meta">{date(e.timestamp)}</span>
                  </div>
                  <div className="btn-wrap">
                    <Button
                      variant="outline"
                      size="xs"
                      onClick={() => {
                        setStart(e.index);
                        if (end < e.index) setEnd(e.index);
                      }}
                    >
                      Inicio
                    </Button>
                    <Button
                      variant="outline"
                      size="xs"
                      onClick={() => {
                        setEnd(e.index);
                        if (start > e.index) setStart(e.index);
                      }}
                    >
                      Fin
                    </Button>
                  </div>
                </div>
                <div className="text-xs whitespace-pre-wrap break-words leading-6">
                  {String(
                    e.text || e.content || "Evento de herramienta sin texto.",
                  ).slice(0, 700)}
                </div>
                {String(e.text || "").length > 700 && (
                  <details>
                    <summary>Ver el mensaje</summary>
                    <Code value={e.text} />
                  </details>
                )}
                {e.event_text_truncated && (
                  <p className="meta mt-2">
                    Vista abreviada · evidencia completa guardada.
                  </p>
                )}
              </article>
            ))}
          </div>
          <Paginator
            page={page}
            setPage={setPage}
            total={rows.length}
            size={25}
          />
        </div>
      </details>
    </div>
  );
}
