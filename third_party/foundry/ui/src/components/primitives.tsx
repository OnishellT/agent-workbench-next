import { type ReactNode } from "react";
import { Layers3 } from "lucide-react";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { type Row, pretty } from "@/api";
import { statusNames } from "@/lib/labels";

export function Status({ value }: { value: string }) {
  return (
    <Badge
      variant="outline"
      className={
        "pill " +
        ([
          "passed",
          "succeeded",
          "accepted",
          "active",
          "correct",
          "promoted",
          "completed",
        ].includes(value)
          ? "good"
          : ["failed", "incorrect", "discarded"].includes(value)
            ? "bad"
            : ["pending", "inconclusive", "stale", "interrupted"].includes(
                  value,
                )
              ? "warn"
              : ["running", "evaluating", "queued"].includes(value)
                ? "purple"
                : "")
      }
    >
      {statusNames[value] || value}
    </Badge>
  );
}

export function TasksStatus({ task }: { task: Row }) {
  return (
    <div className="btn-wrap">
      <span className={"pill " + (task.completed ? "good" : "warn")}>
        {task.completed ? "Completada" : "Sin completar"}
      </span>
      {task.completed && <Status value={task.label} />}
      {task.proposal && !task.completed && (
        <span className="pill purple">Propuesta</span>
      )}
    </div>
  );
}

export function Panel({
  title,
  caption,
  action,
  children,
  className = "",
}: {
  title?: string;
  caption?: string;
  action?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <Card className={"panel gap-0 py-0 " + className}>
      {title && (
        <div className="panel-heading">
          <div>
            <h2 className="text-sm font-semibold">{title}</h2>
            {caption && <p className="meta mt-1">{caption}</p>}
          </div>
          {action}
        </div>
      )}
      {children}
    </Card>
  );
}

export function Heading({ title, actions }: { title: string; actions?: ReactNode }) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-4 mb-6">
      <h1 className="page-title">{title}</h1>
      {actions && <div className="btn-wrap">{actions}</div>}
    </div>
  );
}

export function Empty({
  title,
  text,
  action,
}: {
  title: string;
  text: string;
  action?: ReactNode;
}) {
  return (
    <div className="empty">
      <div className="empty-icon">
        <Layers3 size={20} />
      </div>
      <h3 className="text-sm font-medium mb-2">{title}</h3>
      <p className="muted text-xs leading-6 mb-5">{text}</p>
      {action}
    </div>
  );
}

export function Notice({
  children,
  tone = "",
}: {
  children: ReactNode;
  tone?: string;
}) {
  return <div className={"notice " + tone}>{children}</div>;
}

export function Code({ value }: { value: any }) {
  return (
    <pre className="code-block">
      {typeof value === "string" ? value : pretty(value)}
    </pre>
  );
}

export function Loading() {
  return (
    <div className="space-y-4 p-4">
      <Skeleton className="h-6 w-1/2" />
      <Skeleton className="h-24 w-full" />
      <Skeleton className="h-24 w-full" />
    </div>
  );
}
