import { useEffect, useState } from "react";
import { ArrowDownToLine, CircleHelp, FlaskConical, Plus, RefreshCw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { type Row, api, apiFetch, episode } from "@/api";
import { Choice, Field, Form } from "@/components/forms";
import { Code, Loading, Notice, Status } from "@/components/primitives";
import { term } from "@/lib/labels";
import { active } from "@/lib/state";
import type { Modal, Props } from "@/lib/types";

export function CandidateEditor({ modal, p }: { modal: Modal; p: Props }) {
  const c = p.data.candidates.find((c) => c.id === modal.row?.id) || modal.row!;
  const [readiness, setReadiness] = useState<Row | null>(
      modal.readiness || null,
    ),
    [error, setError] = useState(""),
    [selected, setSelected] = useState<string[]>([]),
    [loading, setLoading] = useState(false);
  const task =
    p.data.episodes.find((e) => e.id === c.episode_id) ||
    (readiness?.episode ? episode(readiness.episode) : undefined);
  async function load() {
    setLoading(true);
    setError("");
    try {
      const r = await api(
        "/api/candidate/readiness?id=" + encodeURIComponent(c.id),
      );
      setReadiness(r);
      setSelected(r.selected_cases.map((x: Row) => x.id));
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setLoading(false);
    }
  }
  useEffect(() => {
    void load();
  }, [c.id, p.data.cases.length, c.status]);
  async function download() {
    try {
      const r = await api("/api/action", {
        action: "export",
        candidate_id: c.id,
      });
      if (r.download || r.url) {
        const response = await apiFetch(r.download || r.url);
        if (!response.ok) throw Error("No se pudo descargar el paquete.");
        const url = URL.createObjectURL(await response.blob());
        const a = document.createElement("a");
        a.href = url;
        a.download = r.filename || "foundry-capability.zip";
        a.click();
        URL.revokeObjectURL(url);
      } else p.open({ kind: "info", row: r });
    } catch (e) {
      setError((e as Error).message);
    }
  }
  const preventive = c.spec.preferred_variant === "preventive";
  return (
    <div className="space-y-5">
      <div className="btn-wrap">
        <Status value={c.status} />
        <span className="pill">{term(c.spec.kind)}</span>
        <span className="pill">{term(c.spec.scope)}</span>
        <span className="pill">{c.spec.domain}</span>
        {c.spec.category && <span className="pill">{c.spec.category}</span>}
        {c.spec.trace_outcome && (
          <span className="pill">traza: {c.spec.trace_outcome}</span>
        )}
        {typeof c.spec.confidence === "number" && (
          <span className="pill">
            confianza {c.spec.confidence} · utilidad {c.spec.utility}
          </span>
        )}
        {active(p.data, c.id) && <Status value="active" />}
      </div>
      <p className="text-sm muted leading-6">
        {c.spec.description || c.spec.rationale}
      </p>
      {Array.isArray(c.spec.risk) && c.spec.risk.length > 0 && (
        <Notice tone="warning">
          El lint de riesgo marcó: {c.spec.risk.join(", ")}. Revisa las
          formulaciones antes de reconocerlo; la evaluación y la adopción
          quedan bloqueadas hasta entonces.
        </Notice>
      )}
      {preventive && (
        <Notice>
          Se evaluará la formulación preventiva porque la tarea fue incorrecta.
        </Notice>
      )}
      <Tabs defaultValue={preventive ? "preventive" : "positive"}>
        <TabsList>
          {(preventive
            ? ["preventive", "positive"]
            : ["positive", "preventive"]
          ).map((v) => (
            <TabsTrigger key={v} value={v}>
              Formulación {v === "positive" ? "positiva" : "preventiva"}
            </TabsTrigger>
          ))}
        </TabsList>
        {["positive", "preventive"].map((v) => (
          <TabsContent key={v} value={v}>
            <Code value={c.spec[v]?.instructions || c.spec[v]} />
            {c.spec[v]?.files?.map((f: Row) => (
              <details key={f.path}>
                <summary>{f.path}</summary>
                <Code value={f.content} />
              </details>
            ))}
          </TabsContent>
        ))}
      </Tabs>
      <div className="step-section">
        <div className="flex items-center justify-between gap-3 mb-3">
          <div>
            <h3 className="text-sm font-semibold">Preparar evaluación</h3>
          </div>
          <Button size="sm" variant="ghost" disabled={loading} onClick={load}>
            <RefreshCw className={loading ? "animate-spin" : ""} />
            Revisar
          </Button>
        </div>
        {error && <Notice tone="error">{error}</Notice>}
        {!readiness && loading ? (
          <Loading />
        ) : (
          readiness && (
            <>
              <Notice tone={readiness.ready ? "success" : "warning"}>
                {readiness.ready
                  ? "Lista para evaluar."
                  : "Preparación pendiente."}
              </Notice>
              {readiness.blockers.map((b: Row, n: number) => (
                <div className="readiness-row" key={n}>
                  <CircleHelp
                    size={17}
                    className="text-warn shrink-0 mt-0.5"
                  />
                  <p className="text-xs leading-6 flex-1">{b.message}</p>
                  {[
                    "complete_task",
                    "label_task",
                    "configure_origin",
                    "derive",
                  ].includes(b.action) &&
                    task && (
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => p.open({ kind: "task", row: task })}
                      >
                        Revisar tarea
                      </Button>
                    )}
                  {b.action === "settings" && (
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => p.open({ kind: "page", id: "settings" })}
                    >
                      Configurar
                    </Button>
                  )}
                  {b.action === "acknowledge_risk" && (
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={p.busy}
                      onClick={async () => {
                        const r = await p.run(
                          "candidate.acknowledge_risk",
                          {
                            id: c.id,
                            acknowledged: true,
                            note: "Hallazgos revisados en la interfaz.",
                          },
                          "Riesgo reconocido; queda registrado en la auditoría.",
                        );
                        if (r) await load();
                      }}
                    >
                      Reconocer riesgo
                    </Button>
                  )}
                  {["create_origin", "create_holdout"].includes(b.action) && (
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() =>
                        p.open({
                          kind: "case-create",
                          task,
                          candidate: c,
                          readiness,
                          split:
                            b.action === "create_origin" ? "origin" : "holdout",
                        })
                      }
                    >
                      Crear caso
                    </Button>
                  )}
                </div>
              ))}
              {readiness.warnings?.map((w: Row, n: number) => (
                <p className="form-help my-3" key={n}>
                  {w.message || String(w)}
                </p>
              ))}
              <div className="grid sm:grid-cols-3 gap-3 my-5">
                {[
                  ["Casos seleccionados", readiness.selected_cases.length],
                  ["Llamadas iniciales", readiness.budget?.calls_first_variant],
                  ["Límite de llamadas", readiness.budget?.max_calls],
                ].map(([l, v]) => (
                  <div className="rounded-lg border p-3" key={l}>
                    <p className="meta">{l}</p>
                    <p className="text-lg mt-1">{v ?? "—"}</p>
                  </div>
                ))}
              </div>
              <h4 className="text-sm font-medium mb-3">Casos disponibles</h4>
              {readiness.available_cases.length ||
              readiness.selected_cases.length ? (
                <div className="space-y-3 mb-4">
                  {Array.from(
                    new Map(
                      [
                        ...readiness.selected_cases,
                        ...readiness.available_cases,
                      ].map((x: Row) => [x.id, x]),
                    ).values(),
                  ).map((x: Row) => (
                    <label
                      key={x.id}
                      className="flex gap-3 text-xs items-start"
                    >
                      <Checkbox
                        checked={selected.includes(x.id)}
                        onCheckedChange={(v) =>
                          setSelected(
                            v
                              ? [...selected, x.id]
                              : selected.filter((id) => id !== x.id),
                          )
                        }
                      />
                      <span>
                        {x.title}
                        <span className="meta block">
                          {x.domain} · {term(x.split)}
                        </span>
                      </span>
                    </label>
                  ))}
                </div>
              ) : (
                <p className="text-xs muted mb-4">
                  Crea un caso de origen y otro de transferencia.
                </p>
              )}
              <div className="btn-wrap mb-4">
                <Button
                  variant="outline"
                  onClick={async () => {
                    const r = await p.run(
                      "candidate.setup",
                      { id: c.id, case_ids: selected },
                      "Casos seleccionados.",
                    );
                    if (r) setReadiness(r);
                  }}
                >
                  Guardar selección
                </Button>
                <Button
                  variant="ghost"
                  onClick={() =>
                    p.open({
                      kind: "case-create",
                      task,
                      candidate: c,
                      readiness,
                      split: "origin",
                    })
                  }
                >
                  <Plus />
                  Caso de origen
                </Button>
                <Button
                  variant="ghost"
                  onClick={() =>
                    p.open({
                      kind: "case-create",
                      task,
                      candidate: c,
                      readiness,
                      split: "holdout",
                    })
                  }
                >
                  <Plus />
                  Caso de transferencia
                </Button>
              </div>
              {!readiness.promotion?.origin_linked && (
                <Notice tone="warning">
                  La incorporación requiere vincular el estado inicial.{" "}
                  {task && (
                    <Button
                      size="sm"
                      variant="link"
                      onClick={() => p.open({ kind: "task", row: task })}
                    >
                      Confirmar origen desde la tarea
                    </Button>
                  )}
                </Notice>
              )}
              <p className="form-help mt-4">
                Esta comparación ejecuta el agente y consume cuota.
              </p>
              <Button
                className="mt-2"
                disabled={!readiness.ready || p.busy}
                onClick={async () => {
                  const r = await p.run("candidate.evaluate", { id: c.id });
                  if (r?.status === "setup_required") setReadiness(r.readiness);
                  else if (r?.job_id) {
                    await load();
                    p.open({ kind: "page", id: "experiments" });
                  }
                }}
              >
                <FlaskConical />
                Ejecutar comparación
              </Button>
            </>
          )
        )}
      </div>
      <details>
        <summary>Alcance, dominio y evidencias</summary>
        <Form
          onSave={async (v) => {
            await p.run(
              "candidate.scope",
              { id: c.id, scope: v.scope, domain: v.domain },
              "Alcance actualizado. La evidencia anterior deberá reevaluarse.",
            );
            await load();
          }}
        >
          <div className="grid sm:grid-cols-2 gap-4">
            <Field
              name="domain"
              label="Dominio"
              initial={c.spec.domain}
              required
            />
            <Choice
              name="scope"
              label="Alcance"
              initial={c.spec.scope}
              options={[
                ["project", "Proyecto"],
                ["domain", "Dominio"],
                ["general", "General"],
              ]}
            />
          </div>
          <Button
            type="submit"
            variant="outline"
            disabled={active(p.data, c.id) || c.status === "evaluating"}
          >
            Guardar alcance
          </Button>
        </Form>
        <Code value={c.spec.evidence || c.spec.source_revision} />
      </details>
      {!active(p.data, c.id) &&
        !["accepted", "evaluating", "rejected"].includes(c.status) && (
          <details>
            <summary>Rechazar aprendizaje</summary>
            <p className="form-help my-3">
              El motivo se guarda como decisión permanente y calibra el próximo
              minado.
            </p>
            <Form
              onSave={async (v) => {
                await p.run(
                  "candidate.reject",
                  { id: c.id, comment: v.comment },
                  "Aprendizaje rechazado; decisión registrada.",
                );
              }}
            >
              <Field
                name="comment"
                label="Motivo del rechazo"
                initial=""
                required
              />
              <Button type="submit" variant="outline">
                Rechazar aprendizaje
              </Button>
            </Form>
          </details>
        )}
      <div className="btn-wrap">
        <Button variant="outline" onClick={download}>
          <ArrowDownToLine />
          Exportar paquete
        </Button>
        {task && (
          <Button
            variant="ghost"
            onClick={() => p.open({ kind: "task", row: task })}
          >
            Tarea de origen
          </Button>
        )}
      </div>
    </div>
  );
}
