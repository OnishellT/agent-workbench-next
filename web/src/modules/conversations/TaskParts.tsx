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
import { Loading } from "./components";
import {
  instant,
  quoteText,
  taskFilters,
  taskState,
  toDisplayIndex,
} from "./domain";
import { translate } from "./i18n";
import type { TaskDTO, TaskHistoryDTO } from "./types";

export function StateBadge({ locale, task }: { locale: Language; task: TaskDTO }) {
  const t = (value: unknown) => translate(value, locale);
  const state = task.status ?? taskState(task);
  const label = taskFilters.find(([value]) => value === state)?.[1] ?? state;
  return (
    <Badge
      variant={
        state === "incorrect"
          ? "destructive"
          : state === "correct"
            ? "default"
            : "secondary"
      }
    >
      {t(label)}
    </Badge>
  );
}

/** The append-only record of one task, as the installation keeps it: it is read from the
 *  detail route when the task is open, so no list pays for it. */
export function TaskHistory({
  locale,
  history,
}: {
  locale: Language;
  history?: TaskHistoryDTO;
}) {
  const t = (value: unknown) => translate(value, locale);
  const completions = history?.completions ?? [];
  const labels = history?.labels ?? [];
  if (history === undefined)
    return <Loading locale={locale} label="Leyendo el historial…" />;
  if (completions.length === 0 && labels.length === 0) return null;
  return (
    <div className="space-y-3">
      {completions.map((entry) => (
        <div
          key={`completion-${entry.generation}`}
          className="space-y-1 border-b pb-2 last:border-0"
        >
          <p className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
            <Badge variant="outline">{t("Finalización")}</Badge>
            <span>
              {t("Generación")} {entry.generation}
            </span>
            <span>{instant(entry.created, locale)}</span>
          </p>
          <p className="text-sm">
            {entry.completed
              ? `${t("Completada y confirmada")} · ${instant(entry.confirmed_at, locale)}`
              : typeof entry.revoked_generation === "number"
                ? `${t("Reabierta")} · ${t("revocó la generación")} ${entry.revoked_generation}`
                : t("Reabierta")}
          </p>
        </div>
      ))}
      {labels.map((entry, index) => (
        <div
          key={`label-${index}-${entry.generation}`}
          className="space-y-1 border-b pb-2 last:border-0"
        >
          <p className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
            <Badge variant="outline">{t("Valoración")}</Badge>
            <span>
              {t("Generación")} {entry.generation}
            </span>
            <span>{instant(entry.created, locale)}</span>
          </p>
          <p className="text-sm">
            {taskFilters.find(([value]) => value === entry.label)?.[1]
              ? t(taskFilters.find(([value]) => value === entry.label)?.[1])
              : (entry.label ?? "")}
            {entry.feedback ? ` · ${entry.feedback}` : ""}
          </p>
        </div>
      ))}
    </div>
  );
}

export function TaskTable({
  locale,
  rows,
  onOpen,
}: {
  locale: Language;
  rows: TaskDTO[];
  onOpen: (id: string) => void;
}) {
  const t = (value: unknown) => translate(value, locale);
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>{t("Tarea")}</TableHead>
          <TableHead>{t("Conversación")}</TableHead>
          <TableHead>{t("Eventos")}</TableHead>
          <TableHead>{t("Revisión")}</TableHead>
          <TableHead>{t("Estado")}</TableHead>
          <TableHead>{t("Citas")}</TableHead>
          <TableHead />
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((task) => (
          <TableRow key={task.id}>
            <TableCell className="max-w-[380px] whitespace-normal">
              <div className="font-medium">{task.title || task.id}</div>
              {task.feedback && (
                <div className="line-clamp-2 text-xs text-muted-foreground">
                  {quoteText(task.feedback, 160)}
                </div>
              )}
            </TableCell>
            <TableCell className="break-all text-xs text-muted-foreground">
              {task.session_id}
            </TableCell>
            <TableCell className="text-xs text-muted-foreground">
              {toDisplayIndex(task.first_event)}–
              {toDisplayIndex(task.last_event)}
            </TableCell>
            <TableCell className="text-xs text-muted-foreground">
              {(task.revision || "").slice(0, 8)}
            </TableCell>
            <TableCell>
              <StateBadge locale={locale} task={task} />
            </TableCell>
            <TableCell className="text-xs text-muted-foreground">
              {(task.citations ?? []).length}
            </TableCell>
            <TableCell>
              <Button
                size="sm"
                variant="outline"
                onClick={() => onOpen(task.id)}
              >
                {t("Revisar tarea")}
                <ChevronRight data-icon="inline-end" />
              </Button>
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

/** TK-03/TK-04/TK-05: one task with its pinned citations and the two human gates. The
 *  screen only ever shows the task the server returned; a conflict reloads it. */
