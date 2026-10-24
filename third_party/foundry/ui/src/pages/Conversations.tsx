import { useState } from "react";
import { CheckCheck, ChevronRight, MessageSquare, RefreshCw, Search, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { type Row, api, episode } from "@/api";
import { Paginator } from "@/components/forms";
import { Empty, Heading, Notice, Panel, TasksStatus } from "@/components/primitives";
import { SessionTable } from "@/components/tables";
import type { Props } from "@/lib/types";

export function Conversations(p: Props) {
  const [query, setQuery] = useState(""),
    [period, setPeriod] = useState("all"),
    [source, setSource] = useState(""),
    [page, setPage] = useState(0),
    [taskPage, setTaskPage] = useState(0),
    [filter, setFilter] = useState("all"),
    [selected, setSelected] = useState<Row | null>(null),
    [tasks, setTasks] = useState<Row[]>([]),
    [tab, setTab] = useState("sessions"),
    [error, setError] = useState("");
  const { data, open } = p;
  const today = new Date();
  const boundary =
    new Date(today.getFullYear(), today.getMonth(), today.getDate()).getTime() /
    1000;
  const rows = data.sessions.filter((s) => {
    const t = s.activity_at || s.updated;
    return (
      (!query ||
        (s.title + " " + s.path + " " + s.id)
          .toLowerCase()
          .includes(query.toLowerCase())) &&
      (!source || source === s.source_id) &&
      (period === "all" ||
        (period === "today" && t >= boundary) ||
        (period === "yesterday" && t >= boundary - 86400 && t < boundary) ||
        (period === "week" && t >= boundary - 6 * 86400))
    );
  });
  const allTasks = selected
    ? tasks.map((t) => data.episodes.find((e) => e.id === t.id) || t)
    : data.episodes;
  const shownTasks = allTasks.filter(
    (e) =>
      filter === "all" ||
      (filter === "incomplete" && !e.completed) ||
      (filter === "pending" && e.completed && e.label === "pending") ||
      e.label === filter,
  );
  const cp = Math.min(page, Math.max(0, Math.ceil(rows.length / 25) - 1)),
    tp = Math.min(taskPage, Math.max(0, Math.ceil(shownTasks.length / 20) - 1));
  async function loadTasks(s: Row) {
    setError("");
    try {
      const result = await api(
        "/api/episodes?session_id=" + encodeURIComponent(s.id),
      );
      setTasks(result.episodes.map(episode));
      setSelected(s);
      setTab("tasks");
      setTaskPage(0);
    } catch (e) {
      setError((e as Error).message);
    }
  }
  return (
    <>
      <Heading
        title="Conversaciones y tareas"
        actions={
          <Button
            variant="outline"
            disabled={!p.data.config.capture}
            onClick={() => p.run("scan", {}, "Captura solicitada.")}
          >
            <RefreshCw />
            Capturar ahora
          </Button>
        }
      />
      {error && <Notice tone="error">{error}</Notice>}
      <Tabs value={tab} onValueChange={(v) => setTab(String(v))}>
        <TabsList className="mb-5">
          <TabsTrigger value="sessions">
            <MessageSquare size={14} />
            Conversaciones{" "}
            <span className="ml-1 opacity-50">
              {data.total_sessions || data.sessions.length}
            </span>
          </TabsTrigger>
          <TabsTrigger value="tasks">
            <CheckCheck size={14} />
            Tareas <span className="ml-1 opacity-50">{allTasks.length}</span>
          </TabsTrigger>
        </TabsList>
        <TabsContent value="sessions">
          <div className="flex flex-wrap gap-3 mb-4">
            <div className="relative min-w-[220px] flex-1">
              <Search
                size={15}
                className="absolute top-2.5 left-3 text-muted-foreground"
              />
              <Input
                className="pl-9"
                aria-label="Buscar conversación"
                placeholder="Buscar por título, archivo o ID…"
                value={query}
                onChange={(e) => {
                  setQuery(e.target.value);
                  setPage(0);
                }}
              />
            </div>
            <select
              className="native-select !w-auto"
              aria-label="Fecha de actividad"
              value={period}
              onChange={(e) => {
                setPeriod(e.target.value);
                setPage(0);
              }}
            >
              <option value="all">Cualquier fecha</option>
              <option value="today">Hoy</option>
              <option value="yesterday">Ayer</option>
              <option value="week">Últimos 7 días</option>
            </select>
            <select
              className="native-select !w-auto max-w-[260px]"
              aria-label="Fuente"
              value={source}
              onChange={(e) => {
                setSource(e.target.value);
                setPage(0);
              }}
            >
              <option value="">Todas las fuentes</option>
              {data.sources.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.adapter} · {s.path.split("/").slice(-2).join("/")}
                </option>
              ))}
            </select>
          </div>
          <div className="flex flex-wrap items-center justify-between gap-3 mb-4">
            <p className="meta">
              Actividad original ·{" "}
              {Intl.DateTimeFormat().resolvedOptions().timeZone}
            </p>
            <div className="btn-wrap">
              <Button
                variant={period === "today" ? "secondary" : "ghost"}
                size="xs"
                onClick={() => {
                  setPeriod("today");
                  setPage(0);
                }}
              >
                Hoy
              </Button>
              <Button
                variant={period === "yesterday" ? "secondary" : "ghost"}
                size="xs"
                onClick={() => {
                  setPeriod("yesterday");
                  setPage(0);
                }}
              >
                Ayer
              </Button>
              <Button
                variant="ghost"
                size="xs"
                onClick={() => {
                  setQuery("");
                  setPeriod("all");
                  setSource("");
                  setPage(0);
                }}
              >
                Quitar filtros
              </Button>
            </div>
          </div>
          {(data.total_sessions || 0) > data.sessions.length && (
            <div className="mb-4">
              <Notice tone="warning">
                Mostrando las {data.sessions.length} conversaciones más
                recientes de {data.total_sessions}. Los filtros se aplican a
                esta selección.
              </Notice>
            </div>
          )}
          <Panel>
            {rows.length ? (
              <>
                <SessionTable
                  rows={rows.slice(cp * 25, (cp + 1) * 25)}
                  data={data}
                  open={open}
                  onTasks={loadTasks}
                />
                <Paginator
                  page={cp}
                  setPage={setPage}
                  total={rows.length}
                  size={25}
                />
              </>
            ) : (
              <Empty
                title={
                  data.sessions.length
                    ? "Sin coincidencias"
                    : "Sin conversaciones"
                }
                text={
                  data.sessions.length
                    ? "Prueba otros filtros."
                    : "Conecta una fuente y captura sus sesiones."
                }
                action={
                  data.sessions.length ? (
                    <Button
                      variant="outline"
                      onClick={() => {
                        setQuery("");
                        setPeriod("all");
                        setSource("");
                      }}
                    >
                      Quitar filtros
                    </Button>
                  ) : (
                    <Button
                      onClick={() => open({ kind: "page", id: "sources" })}
                    >
                      Ver fuentes
                    </Button>
                  )
                }
              />
            )}
          </Panel>
        </TabsContent>
        <TabsContent value="tasks">
          {selected && (
            <div className="flex items-center justify-between gap-3 mb-4">
              <p className="text-xs muted max-w-[650px] line-clamp-2">
                {selected.title}
              </p>
              <Button
                variant="ghost"
                size="sm"
                onClick={() => setSelected(null)}
              >
                Todas las conversaciones
                <X size={13} />
              </Button>
            </div>
          )}
          <div className="btn-wrap mb-4">
            {[
              ["all", "Todas"],
              ["incomplete", "Sin completar"],
              ["pending", "Sin valorar"],
              ["correct", "Correctas"],
              ["incorrect", "Incorrectas"],
            ].map(([value, label]) => (
              <Button
                key={value}
                variant={filter === value ? "secondary" : "ghost"}
                size="sm"
                onClick={() => {
                  setFilter(value);
                  setTaskPage(0);
                }}
              >
                {label}
              </Button>
            ))}
          </div>
          <Panel>
            {shownTasks.length ? (
              <>
                <div className="table-wrap">
                  <table className="data-table">
                    <thead>
                      <tr>
                        <th>Tarea</th>
                        <th>Estado</th>
                        <th>Casos</th>
                        <th />
                      </tr>
                    </thead>
                    <tbody>
                      {shownTasks.slice(tp * 20, (tp + 1) * 20).map((e) => (
                        <tr key={e.id}>
                          <td className="max-w-[440px]">
                            <div className="font-medium line-clamp-2 mb-1">
                              {e.title}
                            </div>
                            <span className="meta">
                              Eventos {e.first_event}–{e.last_event} ·{" "}
                              {e.revision.slice(-8)}
                            </span>
                          </td>
                          <td>
                            <TasksStatus task={e} />
                          </td>
                          <td>{(e.cases || []).length}</td>
                          <td>
                            <Button
                              size="sm"
                              variant="outline"
                              onClick={() =>
                                open({ kind: "task", row: episode(e) })
                              }
                            >
                              Revisar tarea
                              <ChevronRight />
                            </Button>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
                <Paginator
                  page={tp}
                  setPage={setTaskPage}
                  total={shownTasks.length}
                />
              </>
            ) : (
              <Empty
                title="Sin tareas"
                text="Abre una conversación para proponer o crear tareas."
                action={
                  <Button variant="outline" onClick={() => setTab("sessions")}>
                    Ver conversaciones
                  </Button>
                }
              />
            )}
          </Panel>
        </TabsContent>
      </Tabs>
    </>
  );
}
