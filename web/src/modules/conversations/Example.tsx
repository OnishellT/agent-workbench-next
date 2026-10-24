import { useCallback, useState } from "react";
import { ChevronRight, FlaskConical, RefreshCw } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { Language } from "@/app/language";
import { Failure, Loading, Notice, Panel, Unsupported } from "./components";
import { exampleArtifacts, failureMessage, fetchExampleState, notServed, runExample, useResource } from "./client";
import { translate } from "./i18n";
import type { ExampleRunDTO } from "./types";

/** Step marker of the guided example: the order of the list carries the instruction. */
function Step({ n }: { n: number }) {
  return (
    <span
      aria-hidden
      className="inline-flex size-6 shrink-0 items-center justify-center rounded-full border text-xs font-medium"
    >
      {n}
    </span>
  );
}

type Nav = (section: string, view?: string) => void;

/** CV-12: the guided example is prepared and executed from here against the real
 *  backend. It never completes or values the task it creates: both gates stay human. */
export function Example({ locale, onNavigate }: { locale: Language; onNavigate?: Nav }) {
  const t = (value: unknown) => translate(value, locale);
  const load = useCallback((signal: AbortSignal) => fetchExampleState(signal), []);
  const state = useResource(load);
  const [receipt, setReceipt] = useState<ExampleRunDTO | null>(null);
  const [served, setServed] = useState<{ conversation: boolean; task: boolean }>({
    conversation: false,
    task: false,
  });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function prepare() {
    setBusy(true);
    setError("");
    try {
      const result = await runExample();
      setReceipt(result);
      setServed(await exampleArtifacts(result));
    } catch (failure) {
      setError(failureMessage(failure, locale));
    } finally {
      setBusy(false);
    }
  }

  function openURL(patch: Record<string, string>) {
    const url = new URL(location.href);
    for (const [key, value] of Object.entries(patch)) url.searchParams.set(key, value);
    history.replaceState(null, "", url);
  }

  if (state.error !== undefined && !state.data) {
    if (notServed(state.error))
      return (
        <Unsupported
          locale={locale}
          what={t("el ejemplo guiado")}
          error={state.error}
          onRetry={state.reload}
        />
      );
    return <Failure locale={locale} error={state.error} onRetry={state.reload} />;
  }
  if (!state.data) return <Loading locale={locale} label="Comprobando el ejemplo…" />;

  return (
    <div className="space-y-5">
      <Panel
        title={t("Ejemplo guiado · persistencia HTTP")}
        actions={
          <Button variant="outline" size="sm" onClick={state.reload}>
            <RefreshCw data-icon="inline-start" />
            {t("Volver a comprobar")}
          </Button>
        }
      >
        {!state.data.available && (
          <Notice tone="warn">
            {t("El ejemplo no está disponible en esta instalación:")}{" "}
            {state.data.reason || t("el recurso del ejemplo no está publicado.")}
          </Notice>
        )}
        <ol className="space-y-4">
          <li className="flex flex-wrap items-center gap-3">
            <Step n={1} />
            <Button disabled={busy || !state.data.available} onClick={() => void prepare()}>
              <FlaskConical data-icon="inline-start" />
              {t("Preparar y ejecutar el ejemplo")}
            </Button>
          </li>
          {error && (
            <li>
              <Notice tone="error">{error}</Notice>
            </li>
          )}
          {receipt && (
            <li className="space-y-3">
              <div className="flex flex-wrap items-center gap-3">
                <Step n={2} />
                <Notice tone={receipt.status === "succeeded" ? "ok" : "error"}>
                  <Badge variant="outline">{receipt.status}</Badge>
                  {receipt.error}
                </Notice>
                {receipt.session_id && served.conversation && (
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => {
                      openURL({ conversation: receipt.session_id ?? "", revision: "", event: "" });
                      onNavigate?.("conversations");
                    }}
                  >
                    {t("Abrir la conversación del ejemplo")}
                    <ChevronRight data-icon="inline-end" />
                  </Button>
                )}
                {receipt.task_id && served.task && (
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => {
                      openURL({ task: receipt.task_id ?? "" });
                      onNavigate?.("tasks");
                    }}
                  >
                    {t("Abrir la tarea pendiente")}
                    <ChevronRight data-icon="inline-end" />
                  </Button>
                )}
              </div>
              {(receipt.session_id || receipt.task_id) &&
                !served.conversation &&
                !served.task && (
                  <Notice tone="warn">
                    {t(
                      "La ejecución dejó sus identificadores en este almacén, pero esta entrega no sirve las rutas de conversación y tarea: no se puede abrir aquí una vista que no existe.",
                    )}
                  </Notice>
                )}
            </li>
          )}
          {receipt && (receipt.checks ?? []).length > 0 && (
            <li className="space-y-2">
              <Step n={3} />
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t("Control")}</TableHead>
                    <TableHead>{t("Estado")}</TableHead>
                    <TableHead>{t("Esperado")}</TableHead>
                    <TableHead>{t("Observado")}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {(receipt.checks ?? []).map((check) => (
                    <TableRow key={check.name}>
                      <TableCell className="whitespace-normal">{check.name}</TableCell>
                      <TableCell>
                        <Badge variant={check.status === "passed" ? "secondary" : "destructive"}>
                          {check.status}
                        </Badge>
                      </TableCell>
                      <TableCell className="whitespace-normal text-xs text-muted-foreground">
                        {check.expected || t("sin dato")}
                      </TableCell>
                      <TableCell className="whitespace-normal text-xs text-muted-foreground">
                        {check.observed || t("sin dato")}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </li>
          )}
        </ol>
      </Panel>
    </div>
  );
}
