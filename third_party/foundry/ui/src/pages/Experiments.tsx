import { Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { date } from "@/api";
import { Empty, Heading, Panel, Status } from "@/components/primitives";
import { JobRows } from "@/components/tables";
import { term } from "@/lib/labels";
import type { Props } from "@/lib/types";

export function Experiments(p: Props) {
  return (
    <>
      <Heading
        title="Experimentos"
        actions={
          <Button onClick={() => p.open({ kind: "case-create" })}>
            <Plus />
            Crear caso
          </Button>
        }
      />
      <div className="grid xl:grid-cols-[1.7fr_1fr] gap-5">
        <div className="space-y-5">
          <Panel
            title="Experimentos"
            caption={`${p.data.experiments.length} comparaciones registradas`}
          >
            {p.data.experiments.length ? (
              <div className="table-wrap">
                <table className="data-table">
                  <thead>
                    <tr>
                      <th>Aprendizaje</th>
                      <th>Estado</th>
                      <th />
                    </tr>
                  </thead>
                  <tbody>
                    {p.data.experiments.map((e) => (
                      <tr key={e.id}>
                        <td>
                          <div className="font-medium mb-1">
                            {p.data.candidates.find(
                              (c) => c.id === e.candidate_id,
                            )?.spec?.name || e.candidate_id}
                          </div>
                          <div className="meta">{date(e.created)}</div>
                        </td>
                        <td>
                          <Status value={e.status} />
                        </td>
                        <td>
                          <Button
                            variant="outline"
                            size="sm"
                            onClick={() =>
                              p.open({ kind: "experiment", row: e })
                            }
                          >
                            Ver resultados
                          </Button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <Empty
                title="Sin experimentos"
                text="Abre un aprendizaje para preparar su evaluación."
                action={
                  <Button
                    variant="outline"
                    onClick={() => p.open({ kind: "page", id: "learning" })}
                  >
                    Ver aprendizajes
                  </Button>
                }
              />
            )}
          </Panel>
          <Panel title="Actividad y trabajos">
            <JobRows data={p.data} run={p.run} open={p.open} />
          </Panel>
        </div>
        <Panel
          title="Casos reproducibles"
          caption={`${p.data.cases.length} snapshots verificados`}
        >
          {p.data.cases.length ? (
            p.data.cases.map((c) => (
              <div
                key={c.id}
                className="p-5 border-b last:border-0 flex justify-between gap-3"
              >
                <div>
                  <h3 className="text-xs font-medium leading-5 mb-2">
                    {c.title}
                  </h3>
                  <div className="btn-wrap">
                    <span className="pill">{term(c.split)}</span>
                    <span className="meta">{c.domain}</span>
                  </div>
                </div>
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => p.open({ kind: "case", row: c })}
                >
                  Ver
                </Button>
              </div>
            ))
          ) : (
            <Empty
              title="Sin casos"
              text="Añade un proyecto inicial y sus verificadores."
            />
          )}
        </Panel>
      </div>
    </>
  );
}
