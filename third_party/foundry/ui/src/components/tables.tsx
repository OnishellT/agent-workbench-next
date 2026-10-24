import { Button } from "@/components/ui/button";
import { type AppState, type Row, date } from "@/api";
import { Empty, Status } from "@/components/primitives";
import type { Props } from "@/lib/types";

export function JobRows({
  data,
  run,
  open,
}: Pick<Props, "data" | "run"> & { open?: Props["open"] }) {
  return (
    <div>
      {data.jobs.slice(0, 12).map((j) => {
        const progress =
          typeof j.progress === "object" && j.progress ? j.progress : {};
        const detail =
          j.error ||
          (j.status === "succeeded"
            ? "Finalizado · " + date(j.updated)
            : typeof j.progress === "string"
              ? j.progress
              : progress.message ||
                progress.phase ||
                progress.status ||
                (j.status === "blocked"
                  ? "Faltan requisitos antes de evaluar."
                  : ""));
        const candidate = data.candidates.find(
          (c) =>
            c.id ===
            (j.subject ||
              j.target_id ||
              j.target ||
              progress.readiness?.candidate_id),
        );
        return (
          <div
            key={j.id}
            className="flex justify-between gap-3 px-5 py-4 border-b border-border last:border-0"
          >
            <div className="min-w-0">
              <div className="text-xs font-medium mb-1">
                {(
                  {
                    scan: "Capturar conversaciones",
                    propose_tasks: "Proponer tareas de una conversación",
                    derive: "Extraer aprendizaje",
                    evaluate: "Evaluar candidato",
                    case: "Validar caso reproducible",
                    example: "Preparar ejemplo HTTP",
                  } as Row
                )[j.kind] || j.kind}
              </div>
              <p className="meta break-words line-clamp-3">{detail}</p>
            </div>
            <div className="flex flex-col items-end gap-2 shrink-0">
              <Status value={j.status} />
              {["queued", "running"].includes(j.status) ? (
                <Button
                  size="xs"
                  variant="ghost"
                  onClick={() =>
                    run("job.cancel", { id: j.id }, "Cancelación solicitada.")
                  }
                >
                  Cancelar
                </Button>
              ) : j.status === "blocked" ? (
                <Button
                  size="xs"
                  variant="outline"
                  onClick={() =>
                    open?.(
                      candidate
                        ? { kind: "candidate", row: candidate }
                        : { kind: "page", id: "learning" },
                    )
                  }
                >
                  Preparar
                </Button>
              ) : (
                ["failed", "interrupted", "cancelled"].includes(j.status) && (
                  <Button
                    size="xs"
                    variant="ghost"
                    onClick={() =>
                      run(
                        "job.retry",
                        { id: j.id },
                        "Solicitud de reintento procesada.",
                      )
                    }
                  >
                    Reintentar
                  </Button>
                )
              )}
              {j.kind === "propose_tasks" && j.status === "succeeded" && (
                <Button
                  size="xs"
                  variant="outline"
                  onClick={() =>
                    open?.({
                      kind: "session",
                      id: j.subject,
                      revision: j.payload?.expected_revision,
                    })
                  }
                >
                  Revisar tareas
                </Button>
              )}
            </div>
          </div>
        );
      })}
      {!data.jobs.length && (
        <Empty
          title="Sin trabajos pendientes"
          text="Las capturas y evaluaciones aparecerán aquí con su progreso."
        />
      )}
    </div>
  );
}

export function SessionTable({
  rows,
  data,
  open,
  onTasks,
}: {
  rows: Row[];
  data: AppState;
  open: Props["open"];
  onTasks?: (s: Row) => void;
}) {
  return (
    <div className="table-wrap">
      <table className="data-table">
        <thead>
          <tr>
            <th>Conversación</th>
            <th>Última actividad</th>
            <th>Tareas</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {rows.map((s) => (
            <tr key={s.id}>
              <td className="max-w-[460px]">
                <div className="text-[13px] font-medium line-clamp-2 mb-1">
                  {s.title}
                </div>
                <div className="meta truncate" title={s.path}>
                  {data.sources.find((x) => x.id === s.source_id)?.adapter} ·{" "}
                  {String(s.path || "")
                    .split(/[\\/]/)
                    .pop()}
                </div>
                <div className="meta">
                  ID {s.id.slice(-8)} · captura{" "}
                  {date(s.captured_at || s.updated)}
                </div>
              </td>
              <td className="whitespace-nowrap text-xs muted">
                {date(s.activity_at || s.updated)}
              </td>
              <td>
                <span className="pill">
                  {s.task_count ??
                    data.episodes.filter((e) => e.session_id === s.id).length}
                </span>
              </td>
              <td>
                <div className="btn-wrap table-actions">
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => open({ kind: "session", id: s.id })}
                  >
                    Organizar tareas
                  </Button>
                  {onTasks && (
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => onTasks(s)}
                    >
                      Ver tareas
                    </Button>
                  )}
                </div>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
