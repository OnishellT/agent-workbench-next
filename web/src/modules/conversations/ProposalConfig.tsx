import { useCallback, useEffect, useState } from "react";
import { Save, RefreshCw, ShieldAlert } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import type { Language } from "@/app/language";
import { Failure, Loading, Notice, Panel, Unsupported } from "./components";
import { failureMessage, fetchTaskConfig, notServed, saveTaskConfig, useResource } from "./client";
import {
  configRanges,
  configNumberProblem,
  engineModelLabel,
  formatCommand,
  parseCommand,
  taskConfigPatch,
  type ConfigNumberField,
} from "./domain";
import { translate } from "./i18n";
import type {
  ProviderDiagnosticsDTO,
  TaskConfigDTO,
  TaskConfigDiagnosticsDTO,
  TaskProviderConfigDTO,
} from "./types";

/** One engine of the configuration with what the installation verified about it. */
function EngineRow({
  locale,
  role,
  engine,
  diagnostic,
}: {
  locale: Language;
  role: string;
  engine: TaskProviderConfigDTO;
  diagnostic?: ProviderDiagnosticsDTO;
}) {
  const t = (value: unknown) => translate(value, locale);
  return (
    <div className="space-y-1 border-b pb-3 last:border-0">
      <p className="flex flex-wrap items-center gap-2 text-sm">
        <strong>{role}</strong>
        <Badge variant={engine.real_inference ? "secondary" : "outline"}>
          {engine.real_inference ? t("inferencia real") : t("sin inferencia real")}
        </Badge>
        <span className="text-xs text-muted-foreground">
          {engine.kind || t("sin motor")} · {t(engineModelLabel(engine))}
          {engine.provider ? ` · ${engine.provider}` : ""}
          {engine.reasoning ? ` · ${engine.reasoning}` : ""} · {engine.timeout}s
        </span>
      </p>
      <p className="break-all text-xs text-muted-foreground">
        {formatCommand(engine.command) || t("sin comando")}
      </p>
      {diagnostic && (
        <p className="flex flex-wrap items-center gap-2 text-xs">
          <Badge variant={diagnostic.available ? "secondary" : "destructive"}>
            {diagnostic.available ? t("disponible") : t("no disponible")}
          </Badge>
          <span className="text-muted-foreground">
            {diagnostic.reason || t("sin motivo publicado")}
          </span>
        </p>
      )}
    </div>
  );
}

/** Analysis configuration and its real diagnostics: the only place automatic proposals
 *  are consented to. Everything the operator can change here is a contract field with
 *  its published range; an engine the installation cannot run is stated with the reason
 *  the installation gives, never hidden behind a silent disabled control. */
export function ProposalConfig({ locale }: { locale: Language }) {
  const t = (value: unknown) => translate(value, locale);
  const load = useCallback((signal: AbortSignal) => fetchTaskConfig(signal), []);
  const loaded = useResource(load);
  const [draft, setDraft] = useState<TaskConfigDTO | null>(null);
  const [diagnostics, setDiagnostics] = useState<TaskConfigDiagnosticsDTO | null>(null);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");
  const [raw, setRaw] = useState<Record<string, string>>({});

  useEffect(() => {
    if (!loaded.data?.config) return;
    setDraft(loaded.data.config);
    setDiagnostics(loaded.data.diagnostics ?? null);
    setRaw({
      task_idle_seconds: String(loaded.data.config.task_idle_seconds),
      max_input_chars: String(loaded.data.config.max_input_chars),
      max_calls: String(loaded.data.config.max_calls),
      experiment_seconds: String(loaded.data.config.experiment_seconds),
    });
  }, [loaded.data]);

  if (loaded.error !== undefined && !loaded.data) {
    if (notServed(loaded.error))
      return (
        <Unsupported
          locale={locale}
          what={t("la configuración de análisis de tareas")}
          error={loaded.error}
          onRetry={loaded.reload}
        />
      );
    return <Failure locale={locale} error={loaded.error} onRetry={loaded.reload} />;
  }
  if (!draft) return <Loading locale={locale} label="Leyendo la configuración de análisis…" />;

  const problems = (Object.keys(configRanges) as ConfigNumberField[])
    .map((field) => configNumberProblem(field, raw[field] ?? ""))
    .filter((problem): problem is string => problem !== null);

  function update(patch: Partial<TaskConfigDTO>) {
    setDraft((current) => (current ? { ...current, ...patch } : current));
  }

  function updateProvider(
    role: "provider" | "replay",
    patch: Partial<TaskProviderConfigDTO>,
  ) {
    setDraft((current) => (current ? { ...current, [role]: { ...current[role], ...patch } } : current));
  }

  async function save() {
    if (!draft || problems.length) return;
    const patch = taskConfigPatch(loaded.data?.config ?? draft, draft);
    if (Object.keys(patch).length === 0) {
      setNotice(t("No hay cambios que guardar."));
      setError("");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const receipt = await saveTaskConfig(patch);
      if (!receipt.config)
        throw new Error(t("La instalación no confirmó la nueva configuración."));
      setDraft(receipt.config);
      setDiagnostics(receipt.diagnostics ?? null);
      setRaw({
        task_idle_seconds: String(receipt.config.task_idle_seconds),
        max_input_chars: String(receipt.config.max_input_chars),
        max_calls: String(receipt.config.max_calls),
        experiment_seconds: String(receipt.config.experiment_seconds),
      });
      setNotice(t("Configuración confirmada por la instalación."));
    } catch (failure) {
      setNotice("");
      setError(failureMessage(failure, locale));
    } finally {
      setBusy(false);
    }
  }

  function numberField(field: ConfigNumberField, label: string) {
    return (
      <div className="space-y-1.5" key={field}>
        <Label htmlFor={`cfg-${field}`}>{label}</Label>
        <Input
          id={`cfg-${field}`}
          type="number"
          min={configRanges[field][0]}
          max={configRanges[field][1]}
          value={raw[field] ?? ""}
          onChange={(event) => {
            setRaw((current) => ({ ...current, [field]: event.target.value }));
            update({ [field]: Number(event.target.value) } as Partial<TaskConfigDTO>);
          }}
        />
      </div>
    );
  }

  return (
    <div className="space-y-5">
      <Panel
        title={t("Análisis de tareas")}
        actions={
          <>
            <Button variant="outline" size="sm" onClick={() => loaded.reload()}>
              <RefreshCw data-icon="inline-start" />
              {t("Recargar")}
            </Button>
            <Button size="sm" disabled={busy || problems.length > 0} onClick={() => void save()}>
              <Save data-icon="inline-start" />
              {t("Guardar cambios")}
            </Button>
          </>
        }
      >
        {notice && <Notice tone="ok">{notice}</Notice>}
        {error && <Notice tone="error">{error}</Notice>}
        {problems.map((problem) => (
          <Notice tone="error" key={problem}>
            {problem}
          </Notice>
        ))}
        <div className="space-y-2">
          <p className="flex items-center gap-2 text-sm font-medium">
            <ShieldAlert size={14} aria-hidden />
            {t("Diagnóstico de los motores de análisis")}
          </p>
          {diagnostics ? (
            <>
              <EngineRow
                locale={locale}
                role={t("proveedor")}
                engine={draft.provider}
                diagnostic={diagnostics.provider}
              />
              <EngineRow
                locale={locale}
                role={t("reproducción")}
                engine={draft.replay}
                diagnostic={diagnostics.replay}
              />
              {diagnostics.ready ? (
                <Badge variant="secondary" className="w-fit">
                  {t("análisis disponible")}
                </Badge>
              ) : (
                <Notice tone="warn">
                  {`${t("Análisis no disponible:")} ${
                    diagnostics.reason || t("la instalación no publicó el motivo.")
                  }`}
                </Notice>
              )}
            </>
          ) : (
            <p className="text-xs text-muted-foreground">
              {t("La instalación no publicó diagnósticos para esta configuración.")}
            </p>
          )}
        </div>

        <div className="grid gap-3 sm:grid-cols-2">
          <div className="space-y-1.5">
            <Label htmlFor="cfg-kind">{t("Motor del proveedor")}</Label>
            <Input
              id="cfg-kind"
              value={draft.provider.kind}
              onChange={(event) => {
                // OMP is always this installation's own engine and keeps its own
                // thinking until one is set here, so no field is inherited into it.
                const kind = event.target.value;
                updateProvider(
                  "provider",
                  kind === "omp" && draft.provider.kind !== "omp"
                    ? { kind, command: ["omp"], reasoning: "" }
                    : { kind },
                );
              }}
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="cfg-command">{t("Comando")}</Label>
            <Input
              id="cfg-command"
              value={formatCommand(draft.provider.command)}
              onChange={(event) =>
                updateProvider("provider", { command: parseCommand(event.target.value) })
              }
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="cfg-model">{t("Modelo")}</Label>
            <Input
              id="cfg-model"
              value={draft.provider.model}
              onChange={(event) => updateProvider("provider", { model: event.target.value })}
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="cfg-provider">{t("Proveedor del modelo")}</Label>
            <Input
              id="cfg-provider"
              value={draft.provider.provider ?? ""}
              onChange={(event) => updateProvider("provider", { provider: event.target.value })}
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="cfg-reasoning">{t("Razonamiento")}</Label>
            <Input
              id="cfg-reasoning"
              value={draft.provider.reasoning ?? ""}
              onChange={(event) => updateProvider("provider", { reasoning: event.target.value })}
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="cfg-timeout">{t("Tiempo límite por llamada (s)")}</Label>
            <Input
              id="cfg-timeout"
              type="number"
              min={1}
              max={86400}
              value={String(draft.provider.timeout)}
              onChange={(event) =>
                updateProvider("provider", { timeout: Number(event.target.value) })
              }
            />
          </div>
        </div>

        <label className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={draft.provider.real_inference}
            onChange={(event) =>
              updateProvider("provider", { real_inference: event.target.checked })
            }
          />
          {t("Permitir inferencia real del proveedor")}
        </label>

        <div className="grid gap-3 sm:grid-cols-3">
          {numberField("max_input_chars", t("Máximo de caracteres por análisis"))}
          {numberField("max_calls", t("Máximo de llamadas"))}
          {numberField("experiment_seconds", t("Presupuesto de segundos"))}
        </div>

        <div className="grid items-end gap-3 sm:grid-cols-[1fr_1fr]">
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={draft.auto_tasks}
              onChange={(event) => update({ auto_tasks: event.target.checked })}
            />
            {t("Proponer tareas automáticamente")}
          </label>
          {numberField("task_idle_seconds", t("Inactividad antes del análisis automático (s)"))}
        </div>
      </Panel>
    </div>
  );
}
