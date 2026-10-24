import type { ReactNode } from "react";
import { AlertCircle, Loader2, PlugZap } from "lucide-react";
import { ApiError } from "@/app/api";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardAction,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogTitle,
} from "@/components/ui/dialog";
import type { Language } from "@/app/language";
import { failureMessage } from "./client";
import { translate } from "./i18n";

interface Screen {
  locale: Language;
  onNavigate?: (section: string, view?: string) => void;
}

/**
 * Card of one logical block, with the controls of that block next to its title.
 * There is deliberately no caption: a block that needs a sentence to be understood
 * needs a better layout, not more text.
 */
export function Panel({
  title,
  actions,
  children,
}: {
  title: string;
  actions?: ReactNode;
  children: ReactNode;
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-sm font-semibold">{title}</CardTitle>
        {actions && <CardAction className="flex flex-wrap gap-2">{actions}</CardAction>}
      </CardHeader>
      <CardContent className="space-y-3">{children}</CardContent>
    </Card>
  );
}

/** Inline banner for a rejected operation: the server message is shown as it came. */
export function Notice({
  tone,
  children,
}: {
  tone: "error" | "ok" | "warn";
  children: ReactNode;
}) {
  const style =
    tone === "error"
      ? "border-destructive/30 bg-destructive/5 text-destructive"
      : tone === "warn"
        ? "border-amber-500/40 bg-amber-500/5"
        : "border-border bg-muted/40";
  return (
    <div
      role={tone === "error" ? "alert" : "status"}
      className={`flex flex-wrap items-center gap-2 rounded-lg border p-3 text-sm ${style}`}
    >
      {children}
    </div>
  );
}

export function Loading({ locale, label }: { locale: Language; label: string }) {
  return (
    <p role="status" className="flex items-center gap-2 p-4 text-sm text-muted-foreground">
      <Loader2 className="size-4 animate-spin" aria-hidden />
      {translate(label, locale)}
    </p>
  );
}

/** Empty state: what is missing and, when there is one, the next real action. */
export function Empty({
  title,
  action,
}: {
  title: string;
  action?: ReactNode;
}) {
  return (
    <div className="space-y-3 rounded-xl border border-dashed p-6 text-center">
      <p className="font-medium">{title}</p>
      {action && <div className="flex flex-wrap justify-center gap-2">{action}</div>}
    </div>
  );
}

export function Failure({
  locale,
  error,
  onRetry,
}: Screen & { error: unknown; onRetry: () => void }) {
  const t = (value: unknown) => translate(value, locale);
  return (
    <div role="alert" className="space-y-3 rounded-xl border bg-card p-5 text-sm">
      <p className="flex items-center gap-2 font-medium text-destructive">
        <AlertCircle className="size-4" aria-hidden />
        {t("No se pudo leer esta vista")}
      </p>
      <p className="text-muted-foreground">
        {failureMessage(error, locale)}
      </p>
      <Button variant="outline" size="sm" onClick={onRetry}>
        {t("Reintentar")}
      </Button>
    </div>
  );
}

/** A route this release does not serve: the screen states the gap with the real status
 *  instead of an empty list that would read as "nothing captured". */
export function Unsupported({
  locale,
  what,
  error,
  onRetry,
}: Screen & { what: string; error: unknown; onRetry: () => void }) {
  const t = (value: unknown) => translate(value, locale);
  const detail =
    error instanceof ApiError
      ? `${error.status} · ${error.message}`
      : t("sin respuesta del servicio");
  return (
    <div
      role="status"
      className="space-y-2 rounded-xl border bg-card p-5 text-sm text-muted-foreground"
    >
      <p className="flex items-center gap-2 font-medium text-foreground">
        <PlugZap className="size-4" aria-hidden />
        {t("Esta entrega no expone")} {t(what)}
      </p>
      <p className="font-mono text-xs">{detail}</p>
      <Button variant="outline" size="sm" onClick={onRetry}>
        {t("Reintentar")}
      </Button>
    </div>
  );
}

/** Small form dialog of the section: the form itself lives in the caller, so a failed
 *  write stays open with its own message and the values the operator typed. */
export function FormDialog({
  open,
  locale,
  title,
  error,
  busy,
  submitLabel,
  onSubmit,
  onClose,
  children,
}: {
  open: boolean;
  locale: Language;
  title: string;
  error?: string;
  busy?: boolean;
  submitLabel: string;
  onSubmit: () => void;
  onClose: () => void;
  children: ReactNode;
}) {
  const t = (value: unknown) => translate(value, locale);
  return (
    <Dialog open={open} onOpenChange={(next) => (!next ? onClose() : undefined)}>
      <DialogContent closeLabel={t("Cerrar")} aria-describedby={undefined}>
        <DialogTitle className="text-base font-semibold">{t(title)}</DialogTitle>
        {error && <Notice tone="error">{error}</Notice>}
        <div className="space-y-3">{children}</div>
        <div className="flex justify-end gap-2">
          <Button variant="outline" onClick={onClose} disabled={busy}>
            {t("Cancelar")}
          </Button>
          <Button onClick={onSubmit} disabled={busy}>
            {t(submitLabel)}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
