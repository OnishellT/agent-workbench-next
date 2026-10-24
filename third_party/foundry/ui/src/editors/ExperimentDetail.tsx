import { CheckCheck } from "lucide-react";
import { Button } from "@/components/ui/button";
import { type Row, date } from "@/api";
import { Code, Notice, Panel, Status } from "@/components/primitives";
import { active } from "@/lib/state";
import type { Modal, Props } from "@/lib/types";

export function ExperimentDetail({ modal, p }: { modal: Modal; p: Props }) {
  const e =
      p.data.experiments.find((e) => e.id === modal.row?.id) || modal.row!,
    r = e.report || {},
    c = p.data.candidates.find((c) => c.id === e.candidate_id);
  const eligible =
    e.status === "passed" &&
    r.selected_variant &&
    r.replay_provenance?.promotion_eligible;
  const metric = (v: any) =>
    typeof v === "number" ? v.toFixed(3) : "No disponible";
  const duration =
    typeof r.started === "number" && typeof r.finished === "number"
      ? Math.max(0, r.finished - r.started)
      : null;
  const runs: Row[] = (r.attempts || []).flatMap(
    (attempt: Row) => attempt.runs || [],
  );
  const synthetic = runs.some((run: Row) => run.real_inference === false);
  return (
    <div className="space-y-5">
      <div>
        <h3 className="text-base font-semibold">
          {c?.spec?.name || e.candidate_id}
        </h3>
        {c?.spec?.description && (
          <p className="text-sm muted leading-6 mt-2">{c.spec.description}</p>
        )}
        <p className="meta mt-3">
          {r.calls ?? 0} llamadas · Inicio: {date(r.started)} · Fin:{" "}
          {date(r.finished)}
          {duration !== null
            ? ` · Duración: ${duration < 60 ? duration.toFixed(1) + " s" : (duration / 60).toFixed(1) + " min"}`
            : ""}
        </p>
      </div>
      {synthetic && (
        <Notice tone="warning">
          Ejecución sintética: estos resultados no demuestran una mejora del
          modelo.
        </Notice>
      )}
      <div className="btn-wrap">
        <Status value={e.status} />
        {r.selected_variant && (
          <span className="pill">
            Formulación{" "}
            {r.selected_variant === "positive" ? "positiva" : "preventiva"}
          </span>
        )}
        {active(p.data, e.candidate_id) && <Status value="active" />}
      </div>
      <Notice
        tone={r.replay_provenance?.promotion_eligible ? "success" : "warning"}
      >
        {r.replay_provenance?.historical_fidelity === "system_recorded"
          ? "Estado inicial registrado por el sistema."
          : r.replay_provenance?.historical_fidelity === "human_attested"
            ? "Estado inicial confirmado por una persona, sin verificación automática."
            : "Caso reconstruido: sin evidencia del estado inicial histórico."}{" "}
        {r.replay_provenance?.reason}
      </Notice>
      {r.attempts?.map((a: Row, i: number) => (
        <Panel
          key={i}
          title={
            a.variant === "positive"
              ? "Formulación positiva"
              : "Formulación preventiva"
          }
          caption={`${a.summary?.paired_runs ?? 0} pares comparados`}
        >
          <div className="panel-content">
            <div className="grid grid-cols-2 sm:grid-cols-3 gap-3">
              {[
                ["Calidad sin propuesta", a.summary?.baseline_quality],
                ["Calidad con propuesta", a.summary?.candidate_quality],
                ["Cambio de calidad", a.summary?.quality_gain],
                ["Relación de tiempo", a.summary?.time_ratio],
                ["Relación de tokens", a.summary?.token_ratio],
                ["Relación de turnos", a.summary?.turn_ratio],
              ].map(([l, v]) => (
                <div className="rounded-lg border p-3" key={l}>
                  <p className="meta">{l}</p>
                  <p className="text-lg mt-2">{metric(v)}</p>
                </div>
              ))}
            </div>
            <p className="form-help mt-3">
              Relación candidato / baseline: menos de 1 indica menor consumo.
            </p>
            {a.summary?.reasons?.map((reason: string, n: number) => (
              <p className="notice warning mt-3" key={n}>
                {reason}
              </p>
            ))}
            <details>
              <summary>Mediciones y controles</summary>
              <Code value={a} />
            </details>
          </div>
        </Panel>
      ))}
      <div className="step-section">
        <h3 className="text-sm font-semibold mb-3">3. Adoptar mejora</h3>
        <p className="text-xs muted mb-4">
          Tu aprobación activa esta mejora. Puedes restaurar una versión
          anterior.
        </p>
        <Button
          disabled={!eligible || active(p.data, e.candidate_id) || p.busy}
          onClick={() =>
            p.run(
              "candidate.promote",
              { id: e.candidate_id, experiment_id: e.id },
              "Mejora incorporada por tu aprobación.",
            )
          }
        >
          <CheckCheck />
          {active(p.data, e.candidate_id)
            ? "Ya está en el arnés activo"
            : "Incorporar mejora al arnés"}
        </Button>
        {!eligible && (
          <p className="form-help mt-3">
            Adopción bloqueada. Revisa los motivos y el estado inicial.
          </p>
        )}
      </div>
      {c && (
        <Button
          variant="outline"
          onClick={() => p.open({ kind: "candidate", row: c })}
        >
          Revisar aprendizaje
        </Button>
      )}
      <details>
        <summary>Informe completo</summary>
        <Code value={r} />
      </details>
    </div>
  );
}
