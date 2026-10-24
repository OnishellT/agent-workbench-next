import { ChevronRight } from "lucide-react";
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
import {
  coverageFlags,
  coverageStatusLabels,
  inertNote,
  instant,
  toDisplayIndex,
} from "./domain";
import { translate } from "./i18n";
import type {
  ConversationRowDTO,
  EventDTO,
  TaskDTO,
} from "./types";

function CoverageBadges({ locale, row }: { locale: Language; row: ConversationRowDTO }) {
  const t = (value: unknown) => translate(value, locale);
  const flags = coverageFlags(row.coverage);
  return (
    <span className="flex flex-wrap items-center gap-1">
      <Badge variant={flags.partial ? "destructive" : "secondary"}>
        {flags.partial ? t("captura parcial") : t(coverageStatusLabels[flags.status] ?? flags.status)}
      </Badge>
      {flags.events !== null && (
        <span className="text-xs text-muted-foreground">
          {flags.events} {t("eventos")}
        </span>
      )}
      {flags.issues > 0 && (
        <span className="text-xs text-amber-600 dark:text-amber-500">
          {flags.issues} {t("incidencias")}
        </span>
      )}
    </span>
  );
}

export function ConversationTable({
  locale,
  rows,
  onOpen,
}: {
  locale: Language;
  rows: ConversationRowDTO[];
  onOpen: (row: ConversationRowDTO) => void;
}) {
  const t = (value: unknown) => translate(value, locale);
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>{t("Conversación")}</TableHead>
          <TableHead>{t("Motor")}</TableHead>
          <TableHead>{t("Host")}</TableHead>
          <TableHead>{t("Actividad")}</TableHead>
          <TableHead>{t("Estado")}</TableHead>
          <TableHead>{t("Tareas")}</TableHead>
          <TableHead />
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((row) => (
          <TableRow key={row.id}>
            <TableCell className="max-w-[420px] whitespace-normal">
              <div className="font-medium">{row.title || row.id}</div>
              <div className="truncate text-xs text-muted-foreground" dir="rtl">
                <bdi>{row.path || row.id}</bdi>
              </div>
              {row.project && <div className="truncate text-xs text-muted-foreground">{row.project}</div>}
            </TableCell>
            <TableCell>
              <Badge variant="outline">{row.adapter || t("sin adaptador")}</Badge>
            </TableCell>
            <TableCell className="text-xs text-muted-foreground">
              {row.host_id && row.host_id !== "local"
                ? row.host_name || row.host_id
                : t("este servidor")}
            </TableCell>
            <TableCell className="text-xs text-muted-foreground">
              {instant(row.activity_at ?? row.updated, locale)}
            </TableCell>
            <TableCell>
              <CoverageBadges locale={locale} row={row} />
            </TableCell>
            <TableCell className="text-xs text-muted-foreground">
              {typeof row.task_count === "number" ? row.task_count : t("sin dato")}
            </TableCell>
            <TableCell>
              <Button size="sm" variant="outline" onClick={() => onOpen(row)}>
                {t("Abrir")}
                <ChevronRight data-icon="inline-end" />
              </Button>
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

export function TranscriptEvent({
  locale,
  row,
  inRange,
  focused,
}: {
  locale: Language;
  row: EventDTO;
  inRange: boolean;
  focused: boolean;
}) {
  const t = (value: unknown) => translate(value, locale);
  const text = row.text ?? "";
  const shown = text.slice(0, 700);
  const attachments = Array.isArray(row.attachments) ? row.attachments.length : 0;
  return (
    <article
      id={`event-${row.event_index}`}
      aria-current={focused ? "true" : undefined}
      className={`space-y-2 rounded-lg border p-3 text-sm ${
        focused
          ? "border-primary ring-3 ring-ring/40"
          : inRange
            ? "border-primary/50 bg-primary/5"
            : "border-border"
      }`}
    >
      <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
        <Badge variant="outline">{toDisplayIndex(row.event_index)}</Badge>
        <strong className="text-foreground">{row.role || row.kind || t("Evento")}</strong>
        {row.tool_name && <Badge variant="secondary">{row.tool_name}</Badge>}
        {row.channel && <span>{row.channel}</span>}
        <span>{instant(row.timestamp, locale)}</span>
        {attachments > 0 && (
          <span>
            {attachments} {t("adjuntos")}
          </span>
        )}
        {focused && <Badge>{t("cita fijada")}</Badge>}
      </div>
      {/* Transcript text stays text: markup or commands from a captured session are
          displayed, never executed or interpreted as HTML. */}
      <p className="whitespace-pre-wrap break-words">{shown || t("Evento de herramienta sin texto.")}</p>
      {text.length > 700 && (
        <details>
          <summary className="cursor-pointer text-xs text-muted-foreground">
            {t("Ver el mensaje")}
          </summary>
          <p className="mt-2 whitespace-pre-wrap break-words text-xs">{text}</p>
        </details>
      )}
      {inertNote(row) && (
        <p className="text-xs text-muted-foreground">{t(inertNote(row))}</p>
      )}
    </article>
  );
}

export function RevisionTasks({
  locale,
  tasks,
  onOpenTask,
}: {
  locale: Language;
  tasks: TaskDTO[];
  onOpenTask: (id: string) => void;
}) {
  if (!tasks.length) return null;
  return (
    <div className="space-y-2">
      <h3 className="text-sm font-semibold">{translate("Tareas de esta revisión", locale)}</h3>
      <div className="flex flex-wrap gap-2">
        {tasks.map((task) => (
          <Button key={task.id} size="sm" variant="outline" onClick={() => onOpenTask(task.id)}>
            {task.title || task.id}
            <ChevronRight data-icon="inline-end" />
          </Button>
        ))}
      </div>
    </div>
  );
}
