import { ArrowUpRight, History } from "lucide-react";
import { Button } from "@/components/ui/button";
import { date } from "@/api";
import { Empty, Heading, Panel, Status } from "@/components/primitives";
import { term } from "@/lib/labels";
import { active } from "@/lib/state";
import type { Props } from "@/lib/types";

export function Learning(p: Props) {
  return (
    <>
      <Heading title="Aprendizajes" />
      <div className="grid xl:grid-cols-[1.6fr_1fr] gap-5">
        <Panel title="Propuestas">
          {p.data.candidates.length ? (
            <div>
              {p.data.candidates.map((c) => (
                <div key={c.id} className="p-5 border-b last:border-0">
                  <div className="flex justify-between gap-4">
                    <div className="min-w-0">
                      <div className="btn-wrap mb-3">
                        <Status value={c.status} />
                        <span className="pill">{term(c.spec.kind)}</span>
                        <span className="pill">{c.spec.domain}</span>
                        {active(p.data, c.id) && (
                          <span className="pill good">En el arnés activo</span>
                        )}
                      </div>
                      <h3 className="text-sm font-medium mb-2">
                        {c.spec.name || c.spec.title}
                      </h3>
                      <p className="text-xs muted leading-6 line-clamp-3">
                        {c.spec.description || c.spec.rationale}
                      </p>
                    </div>
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => p.open({ kind: "candidate", row: c })}
                    >
                      Inspeccionar
                      <ArrowUpRight />
                    </Button>
                  </div>
                </div>
              ))}
            </div>
          ) : (
            <Empty
              title="Sin aprendizajes"
              text="Completa y valora una tarea para extraer un aprendizaje."
              action={
                <Button
                  variant="outline"
                  onClick={() => p.open({ kind: "page", id: "conversations" })}
                >
                  Revisar tareas
                </Button>
              }
            />
          )}
        </Panel>
        <div className="space-y-5">
          <Panel title="Versiones del arnés">
            {p.data.versions.map((v) => (
              <div
                key={v.id}
                className="p-5 border-b last:border-0 flex justify-between items-start gap-3"
              >
                <div>
                  <h3 className="text-xs font-medium mb-2">
                    {v.id === "initial" ? "Arnés inicial" : v.id}
                  </h3>
                  <p className="meta">
                    {v.members?.length || 0} capacidades · {date(v.created)}
                  </p>
                  <p className="meta mt-1">{v.reason}</p>
                </div>
                {p.data.active_version === v.id ? (
                  <Status value="active" />
                ) : (
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() =>
                      p.run(
                        "rollback",
                        { version_id: v.id },
                        "Versión restaurada; historial conservado.",
                      )
                    }
                  >
                    <History />
                    Restaurar
                  </Button>
                )}
              </div>
            ))}
          </Panel>
        </div>
      </div>
    </>
  );
}
