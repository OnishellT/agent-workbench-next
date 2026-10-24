import { ArrowUpRight, CheckCheck, FlaskConical, GitBranch, MessageSquare, Plus, Sparkles } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Empty, Heading, Panel } from "@/components/primitives";
import { JobRows, SessionTable } from "@/components/tables";
import type { Props } from "@/lib/types";

export function Dashboard(p: Props) {
  const { data, open, run } = p;
  const accepted =
    data.versions.find((v) => v.id === data.active_version)?.members?.length ||
    0;
  return (
    <>
      <Heading
        title="Resumen"
        actions={
          <Button onClick={() => open({ kind: "source" })}>
            <Plus />
            Conectar fuente
          </Button>
        }
      />
      <div className="grid grid-cols-2 xl:grid-cols-4 gap-4 mb-6">
        {[
          [
            MessageSquare,
            "Conversaciones",
            data.total_sessions || data.sessions.length,
          ],
          [
            CheckCheck,
            "Tareas completadas",
            data.episodes.filter((e) => e.completed).length,
          ],
          [Sparkles, "Aprendizajes", data.candidates.length],
          [GitBranch, "Mejoras activas", accepted],
        ].map(([Icon, label, note]: any) => (
          <div className="stat-card" key={label}>
            <div className="flex justify-between items-center muted text-xs">
              {label}
              <Icon size={16} className="text-primary" />
            </div>
            <div className="stat-number">{note}</div>
          </div>
        ))}
      </div>
      <div className="grid xl:grid-cols-[1.6fr_1fr] gap-5">
        <div className="space-y-5">
          <Panel
            title="Conversaciones recientes"
            action={
              <Button
                variant="ghost"
                size="sm"
                onClick={() => open({ kind: "page", id: "conversations" })}
              >
                Ver todas
                <ArrowUpRight />
              </Button>
            }
          >
            {data.sessions.length ? (
              <SessionTable
                rows={data.sessions.slice(0, 5)}
                data={data}
                open={open}
              />
            ) : (
              <Empty
                title="Sin conversaciones"
                text="Conecta una carpeta de sesiones para empezar."
                action={
                  <Button onClick={() => open({ kind: "source" })}>
                    Conectar fuente
                  </Button>
                }
              />
            )}
          </Panel>
          <Panel>
            <div className="panel-content flex items-center gap-5">
              <div className="hidden sm:grid size-12 place-items-center rounded-xl bg-primary/10 text-primary">
                <FlaskConical />
              </div>
              <div className="flex-1">
                <h3 className="text-sm font-semibold mb-1">Ejemplo HTTP</h3>
              </div>
              <Button
                variant="outline"
                onClick={() =>
                  run("example", {}, "Preparación del ejemplo en cola.")
                }
              >
                Preparar ejemplo
              </Button>
            </div>
          </Panel>
        </div>
        <Panel title="Actividad">
          <JobRows data={data} run={run} open={open} />
        </Panel>
      </div>
    </>
  );
}
