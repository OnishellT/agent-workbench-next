import { useState } from "react";
import { Database } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { type Row, argv } from "@/api";
import { Choice, Field, Form } from "@/components/forms";
import { Code, Heading, Panel } from "@/components/primitives";
import type { Props } from "@/lib/types";

function AgentSettings({ name, config }: { name: string; config: Row }) {
  const [kind, setKind] = useState(
    config?.kind || (name === "provider" ? "pi" : "codex"),
  );
  const [command, setCommand] = useState(
    JSON.stringify(config?.command || [kind === "pi" ? "pi" : "codex"]),
  );
  const [model, setModel] = useState(
    config?.model || (kind === "pi" ? "gpt-6.1-sol" : ""),
  );
  const [provider, setProvider] = useState(
    config?.provider || (kind === "pi" ? "openai-codex" : ""),
  );
  return (
    <div className="step-section">
      <h3 className="text-sm font-semibold mb-2">
        {name === "provider" ? "Agente de análisis" : "Agente de evaluación"}
      </h3>
      <div className="grid md:grid-cols-2 gap-4">
        <Field name={name + "_kind"} label="Agente">
          <select
            id={"field-" + name + "_kind"}
            className="native-select"
            name={name + "_kind"}
            value={kind}
            onChange={(event) => {
              const next = event.target.value;
              setKind(next);
              if (next !== "command") {
                setCommand(JSON.stringify([next]));
                if (next === "pi") {
                  if (!model) setModel("gpt-6.1-sol");
                  if (!provider) setProvider("openai-codex");
                }
              }
            }}
          >
            <option value="pi">Pi</option>
            <option value="codex">Codex CLI</option>
            <option value="command">Comando externo</option>
          </select>
        </Field>
        <Field name={name + "_model"} label="Modelo">
          <Input
            id={"field-" + name + "_model"}
            name={name + "_model"}
            value={model}
            onChange={(event) => setModel(event.target.value)}
            placeholder="gpt-6.1-sol"
          />
        </Field>
        <Choice
          name={name + "_reasoning"}
          label="Razonamiento (Pi)"
          initial={config?.reasoning || "medium"}
          disabled={kind !== "pi"}
          options={[
            ["off", "Desactivado"],
            ["minimal", "Mínimo"],
            ["low", "Bajo"],
            ["medium", "Medio"],
            ["high", "Alto"],
            ["xhigh", "Muy alto"],
            ["max", "Máximo"],
          ]}
        />
        <Field
          name={name + "_provider"}
          label="Proveedor de Pi"
          hint={kind === "pi" ? "Autentica tu cuenta en Pi." : undefined}
        >
          <Input
            id={"field-" + name + "_provider"}
            name={name + "_provider"}
            value={provider}
            onChange={(event) => setProvider(event.target.value)}
            disabled={kind !== "pi"}
          />
        </Field>
        <Field
          name={name + "_timeout"}
          label="Tiempo máximo por llamada (segundos)"
          type="number"
          initial={config?.timeout || 180}
        />
      </div>
      <details
        className="mt-3"
        open={kind === "command" ? true : undefined}
        onInvalidCapture={(event) => {
          event.currentTarget.open = true;
        }}
      >
        <summary>Comando de ejecución</summary>
        <Field
          name={name + "_command"}
          label="Argumentos JSON"
          hint="Sin shell ni claves. Autentica el agente por separado."
        >
          <Input
            name={name + "_command"}
            id={"field-" + name + "_command"}
            value={command}
            onChange={(event) => setCommand(event.target.value)}
            required
          />
        </Field>
      </details>
    </div>
  );
}

export function Settings(p: Props) {
  const c = p.data.config;
  const numbers = [
    ["poll_seconds", "Intervalo de captura (segundos)"],
    [
      "task_idle_seconds",
      "Espera sin actividad para proponer tareas (segundos)",
    ],
    ["max_input_chars", "Caracteres por bloque de análisis"],
    ["max_source_bytes", "Tamaño máximo de fuente (bytes)"],
    ["max_files_per_scan", "Archivos por captura"],
    ["max_scan_seconds", "Tiempo máximo de captura (segundos)"],
    ["repetitions", "Repeticiones por caso"],
    ["max_calls", "Máximo de llamadas por experimento"],
    ["experiment_seconds", "Tiempo máximo del experimento (segundos)"],
    ["min_quality_gain", "Mejora mínima de calidad"],
    ["max_time_ratio", "Relación de tiempo máxima"],
    ["min_efficiency_gain", "Mejora mínima de eficiencia"],
  ];
  return (
    <>
      <Heading title="Configuración" />
      <div className="grid xl:grid-cols-[1.6fr_1fr] gap-5">
        <Panel title="Preferencias">
          <div className="panel-content">
            <Form
              onSave={async (v, fd) => {
                const cfg: Row = { auto_promote: false };
                for (const k of [
                  "capture",
                  "auto_tasks",
                  "auto_derive",
                  "auto_evaluate",
                ])
                  if (k !== "capture" || !p.onOpenCaptureSettings) cfg[k] = fd.has(k);
                for (const [k] of numbers) cfg[k] = Number(v[k]);
                for (const k of ["provider", "replay"])
                  cfg[k] = {
                    ...c[k],
                    kind: v[k + "_kind"],
                    command: argv(v[k + "_command"]),
                    model: v[k + "_model"],
                    ...(v[k + "_kind"] === "pi"
                      ? {
                          reasoning: v[k + "_reasoning"],
                          provider:
                            v[k + "_provider"] ||
                            c[k]?.provider ||
                            "openai-codex",
                        }
                      : {}),
                    timeout: Number(v[k + "_timeout"]),
                  };
                await p.run(
                  "settings",
                  { config: cfg },
                  "Configuración guardada.",
                );
              }}
            >
              <div className="grid gap-5">
                {p.onOpenCaptureSettings && <div className="flex flex-wrap items-center justify-between gap-3">
                  <span className="text-sm">{c.capture ? "Captura activa" : "Captura pausada"}</span>
                  <Button type="button" variant="outline" size="sm" onClick={p.onOpenCaptureSettings}>Gestionar captura en Workbench</Button>
                </div>}
                {[
                  [
                    "capture",
                    "Captura automática",
                    "Lee periódicamente las fuentes habilitadas.",
                  ],
                  [
                    "auto_tasks",
                    "Proponer tareas automáticamente",
                    "Conversaciones nuevas o actualizadas · consume cuota.",
                  ],
                  [
                    "auto_derive",
                    "Extraer aprendizajes tras tu valoración",
                    "Tareas completadas y valoradas · consume cuota.",
                  ],
                  [
                    "auto_evaluate",
                    "Evaluar propuestas automáticamente",
                    "Con casos preparados · consume cuota.",
                  ],
                ].filter(([k]) => k !== "capture" || !p.onOpenCaptureSettings).map(([k, t, h]) => (
                  <label
                    key={k}
                    className="flex items-start justify-between gap-5"
                  >
                    <span>
                      <strong className="text-sm font-medium">{t}</strong>
                      <span className="form-help block">{h}</span>
                    </span>
                    <Switch name={k} defaultChecked={!!c[k]} />
                  </label>
                ))}
              </div>
              {["provider", "replay"].map((k) => (
                <AgentSettings key={k} name={k} config={c[k]} />
              ))}
              <details className="disclosure">
                <summary>Límites de captura y evaluación</summary>
                <div className="grid md:grid-cols-2 gap-4">
                  {numbers.map(([k, l]) => (
                    <Field
                      key={k}
                      name={k}
                      label={l}
                      type="number"
                      initial={c[k] ?? (k === "task_idle_seconds" ? 120 : 0)}
                      hint={
                        k === "task_idle_seconds"
                          ? "Entre 15 y 86400 segundos."
                          : k === "max_source_bytes"
                            ? "Máximo admitido: 512 MB (512000000 bytes)."
                            : k === "max_input_chars"
                              ? "La evidencia completa se reparte en bloques."
                              : undefined
                      }
                    />
                  ))}
                </div>
              </details>
              <Button type="submit">Guardar configuración</Button>
            </Form>
          </div>
        </Panel>
        <div className="space-y-5">
          <Panel title="Estado del entorno">
            <div className="panel-content">
              <details>
                <summary>Ver diagnóstico</summary>
                <Code value={p.data.diagnostics} />
              </details>
            </div>
          </Panel>
          <Panel title="Respaldo">
            <div className="panel-content space-y-4">
              <p className="form-help">
                Sólo SQLite. Para un respaldo completo, copia también snapshots
                y artefactos.
              </p>
              <Button
                variant="outline"
                onClick={async () => {
                  const r = await p.run("backup", {}, "Copia creada.");
                  if (r) p.open({ kind: "info", row: r });
                }}
              >
                <Database />
                Respaldar base de datos
              </Button>
            </div>
          </Panel>
        </div>
      </div>
    </>
  );
}
