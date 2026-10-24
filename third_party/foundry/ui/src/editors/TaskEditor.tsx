import { useEffect, useState } from "react";
import { Plus, ShieldCheck, Sparkles } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { type Row, argv, episode } from "@/api";
import { Choice, Field, Form } from "@/components/forms";
import { Notice, TasksStatus } from "@/components/primitives";
import { term } from "@/lib/labels";
import type { Modal, Props } from "@/lib/types";

export function CaseForm({
  modal,
  p,
  close,
}: {
  modal: Modal;
  p: Props;
  close: () => void;
}) {
  const task = modal.task;
  const setup = modal.readiness?.setup || {};
  return (
    <div className="space-y-5">
      <Notice tone="warning">
        Los controles ejecutan comandos locales. Limítalos a {"{workspace}"}.
      </Notice>
      {task && (
        <p className="text-sm">
          Se vinculará a: <strong>{task.title}</strong>
        </p>
      )}
      <Form
        onSave={async (v) => {
          const payload: Row = {
            title: v.title,
            prompt: v.prompt,
            path: v.path,
            verifier_path: v.verifier_path,
            ref: v.ref || undefined,
            domain: v.domain,
            split: v.split,
            verify: argv(v.verify),
            positive_control:
              !v.positive_control.trim() || v.positive_control.trim() === "[]"
                ? []
                : argv(v.positive_control),
            negative_control: argv(v.negative_control),
          };
          if (task || modal.readiness?.episode_id) {
            payload.episode_id = task?.id || modal.readiness?.episode_id;
            payload.expected_revision =
              task?.revision || setup.expected_revision;
          }
          const r = await p.run(
            "case.create",
            payload,
            "Validación del snapshot y controles en cola. Consulta Experimentos.",
          );
          if (r) close();
        }}
      >
        <Field name="title" label="Nombre del caso" required />
        <Field
          name="prompt"
          label="Instrucción de la tarea"
          type="textarea"
          required
        />
        <div className="grid md:grid-cols-2 gap-4">
          <Field
            name="path"
            label="Directorio del proyecto inicial"
            initial={setup.project_path || ""}
            required
          />
          <Field
            name="verifier_path"
            label="Directorio externo del verificador"
            required
          />
          <Field name="ref" label="Referencia Git inicial (opcional)" />
          <Field
            name="domain"
            label="Dominio"
            initial={setup.domain || modal.candidate?.spec?.domain || ""}
            required
          />
        </div>
        <Choice
          name="split"
          label="Función del caso"
          initial={modal.split || "origin"}
          options={[
            ["origin", "Origen: objetivo original"],
            ["holdout", "Transferencia: un caso distinto"],
            ["regression", "Regresión: comportamiento existente"],
          ]}
        />
        <Field
          name="verify"
          label="Verificador · argumentos JSON"
          initial={'["{python}", "{verifier}/verify.py", "{workspace}"]'}
          required
        />
        <Field
          name="positive_control"
          label="Control positivo · argumentos JSON"
          initial=""
          hint="Prepara una solución correcta. Opcional si el estado inicial ya pasa."
        />
        <Field
          name="negative_control"
          label="Control negativo · argumentos JSON"
          hint="Introduce un fallo que el verificador debe rechazar."
          initial={
            '["{python}", "{verifier}/introduce_fault.py", "{workspace}"]'
          }
          required
        />
        <p className="form-help">
          Argumentos JSON, sin shell. Sustituye los scripts de ejemplo por los
          de tu proyecto.
        </p>
        <Button type="submit">
          <ShieldCheck />
          Validar y guardar caso
        </Button>
      </Form>
    </div>
  );
}

export function TaskEditor({ modal, p }: { modal: Modal; p: Props }) {
  const initial = episode(modal.row!),
    task = p.data.episodes.find((e) => e.id === initial.id) || initial;
  const [local, setLocal] = useState<Row>(task);
  const e = { ...task, ...local };
  const [selected, setSelected] = useState<string[]>(e.cases || []);
  useEffect(() => {
    setLocal(task);
  }, [task.completed, task.label, task.origin, task.cases]);
  const linked = p.data.cases.filter((c) => selected.includes(c.id));
  const canExtract = e.completed && ["correct", "incorrect"].includes(e.label);
  return (
    <div className="space-y-5">
      <TasksStatus task={e} />
      <div className="text-xs muted">
        Eventos {e.first_event + 1}–{e.last_event + 1} · revisión{" "}
        {String(e.revision).slice(0, 12)}
      </div>
      <div className="step-section">
        <h3 className="text-sm font-semibold mb-2">1. Finalización</h3>
        <Button
          variant={e.completed ? "outline" : "default"}
          onClick={async () => {
            const r = await p.run(
              "episode.complete",
              { id: e.id, completed: !e.completed },
              e.completed
                ? "Tarea reabierta; valoración pendiente."
                : "Finalización confirmada. Ahora puedes valorar el resultado.",
            );
            if (r) setLocal(episode(r));
          }}
        >
          {e.completed ? "Reabrir tarea" : "Marcar tarea completada"}
        </Button>
        {e.completed && (
          <p className="form-help mt-3">
            Reabrir retira la valoración e invalida la evidencia pendiente.
          </p>
        )}
      </div>
      <div className="step-section">
        <h3 className="text-sm font-semibold mb-2">2. Resultado</h3>
        {!e.completed && (
          <Notice tone="warning">
            Confirma primero que la tarea está completada.
          </Notice>
        )}
        {e.completed && (
          <Form
            key={e.id + "-" + e.label}
            onSave={async (v) => {
              const r = await p.run(
                "episode.label",
                { id: e.id, label: v.label, feedback: v.feedback },
                "Valoración guardada.",
              );
              if (r) setLocal({ ...e, label: v.label, feedback: v.feedback });
            }}
          >
            <fieldset disabled={!e.completed} className="field-group">
              <Choice
                label="Valoración"
                name="label"
                initial={e.label}
                options={[
                  ["pending", "Pendiente de valoración"],
                  ["correct", "Correcta"],
                  ["incorrect", "Incorrecta"],
                ]}
              />
              <Field
                name="feedback"
                label="Qué funcionó o qué falló"
                initial={e.feedback || ""}
                type="textarea"
                hint="Incluye el resultado esperado si hubo un fallo."
              />
              {p.data.config.auto_derive && (
                <p className="form-help">
                  Guardar una valoración correcta o incorrecta inicia la
                  extracción y consume cuota.
                </p>
              )}
              <Button type="submit" variant="outline">
                Guardar valoración
              </Button>
            </fieldset>
          </Form>
        )}
      </div>
      <details className="disclosure" open={canExtract ? true : undefined}>
        <summary>Casos de evaluación · {selected.length}</summary>
        <div className="space-y-4 mt-4">
          <div>
            <div className="space-y-3 mb-4">
              {p.data.cases.map((c) => (
                <label className="flex items-start gap-3 text-xs" key={c.id}>
                  <Checkbox
                    checked={selected.includes(c.id)}
                    onCheckedChange={(v) =>
                      setSelected(
                        v
                          ? [...selected, c.id]
                          : selected.filter((id) => id !== c.id),
                      )
                    }
                  />
                  <span>
                    {c.title}
                    <span className="meta block">
                      {c.domain} · {term(c.split)}
                    </span>
                  </span>
                </label>
              ))}
            </div>
            <div className="btn-wrap">
              <Button
                variant="outline"
                onClick={async () => {
                  const r = await p.run(
                    "episode.cases",
                    { id: e.id, case_ids: selected },
                    "Casos vinculados.",
                  );
                  if (r) setLocal({ ...e, cases: selected });
                }}
              >
                Guardar selección
              </Button>
              <Button
                variant="ghost"
                onClick={() => p.open({ kind: "case-create", task: e })}
              >
                <Plus />
                Crear caso para esta tarea
              </Button>
            </div>
          </div>
          <details
            className="disclosure"
            open={
              linked.some((c) => c.split === "origin") && !e.origin
                ? true
                : undefined
            }
          >
            <summary>
              Estado inicial {e.origin ? "· vinculado" : "· sin confirmar"}
            </summary>
            <div className="field-group mt-4">
              {e.origin ? (
                <Notice tone="success">
                  Origen vinculado ·{" "}
                  {e.origin.provenance === "system_recorded"
                    ? "Registrado antes de ejecutar la tarea"
                    : "Confirmación humana del estado inicial"}
                  . {e.origin.note}
                </Notice>
              ) : (
                <Notice tone="warning">
                  Confirma el estado inicial para poder adoptar una mejora
                  evaluada.
                </Notice>
              )}
              <Form
                onSave={async (v, fd) => {
                  if (!fd.has("confirmed"))
                    throw Error(
                      "Debes confirmar expresamente el estado inicial.",
                    );
                  const r = await p.run(
                    "episode.origin",
                    {
                      id: e.id,
                      case_id: v.case_id,
                      confirmed_initial_state: true,
                      note: v.note,
                    },
                    "Origen vinculado con tu confirmación.",
                  );
                  if (r) setLocal({ ...e, origin: r.origin || r });
                }}
              >
                <Choice
                  name="case_id"
                  label="Caso de origen vinculado"
                  options={linked
                    .filter((c) => c.split === "origin")
                    .map((c) => [c.id, c.title])}
                />
                <Field
                  name="note"
                  label="Cómo sabes que es el estado inicial"
                  type="textarea"
                  required
                  hint="Por ejemplo, commit previo, copia conservada o registro de ejecución."
                />
                <label className="text-xs flex gap-3 items-start">
                  <Checkbox name="confirmed" />
                  Confirmo que este snapshot corresponde al estado inicial de
                  esta tarea. Es mi declaración, no una prueba automática.
                </label>
                <Button
                  type="submit"
                  variant="outline"
                  disabled={!linked.some((c) => c.split === "origin")}
                >
                  Vincular origen confirmado
                </Button>
              </Form>
            </div>
          </details>
        </div>
      </details>
      {canExtract && (
        <p className="form-help">
          Extraer aprendizaje usa el modelo configurado y consume cuota.
        </p>
      )}
      <div className="btn-wrap">
        {canExtract && (
          <Button
            disabled={p.busy}
            onClick={() =>
              p.run(
                "derive",
                { id: e.id },
                "Extracción de aprendizaje en cola.",
              )
            }
          >
            <Sparkles />
            Extraer aprendizaje {e.label === "incorrect" ? "preventivo" : ""}
          </Button>
        )}
        <Button
          variant="outline"
          onClick={() =>
            p.open({ kind: "session", id: e.session_id, revision: e.revision })
          }
        >
          Ver evidencia original
        </Button>
      </div>
    </div>
  );
}
