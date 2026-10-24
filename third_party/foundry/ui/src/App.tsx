import { useEffect, useRef, useState } from "react";
import { ChevronRight, FlaskConical, FolderOpen, Layers3, LayoutDashboard, MessageSquare, RefreshCw, Settings2, Sparkles, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { type AppState, type Row, api, episode, setToken } from "@/api";
import { Field, Form } from "@/components/forms";
import { Heading, Loading, Notice, Panel } from "@/components/primitives";
import { ModalBody } from "@/editors/ModalBody";
import type { Modal, Props } from "@/lib/types";
import { Conversations } from "@/pages/Conversations";
import { Dashboard } from "@/pages/Dashboard";
import { Experiments } from "@/pages/Experiments";
import { Learning } from "@/pages/Learning";
import { Settings } from "@/pages/Settings";
import { Sources } from "@/pages/Sources";

const nav = [
  ["dashboard", "Vista general", LayoutDashboard],
  ["sources", "Fuentes", FolderOpen],
  ["conversations", "Conversaciones", MessageSquare],
  ["learning", "Aprendizajes", Sparkles],
  ["experiments", "Experimentos", FlaskConical],
  ["settings", "Configuración", Settings2],
] as const;

export default function App({ embedded = false, initialView = "dashboard", onNavigate, onOpenCaptureSettings }: { embedded?: boolean; initialView?: string; onNavigate?: (view: string) => void; onOpenCaptureSettings?: () => void } = {}) {
  const [data, setData] = useState<AppState | null>(null),
    [page, setPageState] = useState(initialView),
    [modal, setModal] = useState<Modal | null>(null),
    [notice, setNotice] = useState<{
      text: string;
      error?: boolean;
      warning?: boolean;
    } | null>(null),
    [error, setError] = useState(""),
    [auth, setAuth] = useState(false),
    [busy, setBusy] = useState(false);
  const fetching = useRef<Promise<void> | null>(null);
  const refreshQueued = useRef(false);
  const mounted = useRef(true);
  const setPage = (value: string) => { setPageState(value); onNavigate?.(value); };
  useEffect(() => { setPageState(initialView); setModal(null); }, [initialView]);
  async function refresh() {
    if (fetching.current) {
      // A mutation can finish after the current snapshot started. Wait for a
      // subsequent snapshot before confirming that its state has refreshed.
      refreshQueued.current = true;
      return fetching.current;
    }
    const load = async () => {
      do {
        refreshQueued.current = false;
        try {
          const r = await api("/api/state?compact=1");
          if (!mounted.current) return;
          r.episodes = r.episodes.map(episode);
          setData(r);
          setError("");
          setAuth(false);
        } catch (e) {
          if (!mounted.current) return;
          const msg = (e as Error).message;
          setError(msg);
          if (
            (e as Error & { status?: number }).status === 401 ||
            /401|token|autori/i.test(msg)
          )
            setAuth(true);
        }
      } while (refreshQueued.current && mounted.current);
    };
    fetching.current = load();
    try {
      await fetching.current;
    } finally {
      fetching.current = null;
    }
  }
  useEffect(() => {
    mounted.current = true;
    void refresh();
    const tick = () => {
      if (!document.hidden && !fetching.current) void refresh();
    };
    const t = setInterval(tick, 3000);
    document.addEventListener("visibilitychange", tick);
    return () => {
      mounted.current = false;
      clearInterval(t);
      document.removeEventListener("visibilitychange", tick);
    };
  }, []);
  function open(m: Modal) {
    if (m.kind === "page") {
      setModal(null);
      setPage(m.id!);
    } else setModal(m);
  }
  async function run(action: string, payload: Row = {}, message?: string) {
    setBusy(true);
    setNotice(null);
    try {
      const r = await api("/api/action", { action, ...payload });
      await refresh();
      if (r.status === "setup_required" && r.readiness) {
        const candidate = data?.candidates.find(
          (c) => c.id === r.readiness.candidate_id,
        );
        setNotice({
          text: "Completa la preparación antes de evaluar.",
          warning: true,
        });
        if (candidate)
          setModal({
            kind: "candidate",
            row: candidate,
            readiness: r.readiness,
          });
      } else if (action === "episode.label" && r.job_id) {
        setNotice({
          text: "Valoración guardada · extracción en cola.",
        });
      } else if (message) setNotice({ text: message });
      return r;
    } catch (e) {
      setNotice({ text: (e as Error).message, error: true });
      return null;
    } finally {
      setBusy(false);
    }
  }
  const p: Props = { data: data!, run, open, refresh, busy, onOpenCaptureSettings };
  const title =
    modal?.kind === "session"
      ? "Tareas de la conversación"
      : modal?.kind === "task"
        ? modal.row?.title
        : modal?.kind === "candidate"
          ? modal.row?.spec?.name
          : modal?.kind === "experiment"
            ? "Resultados del experimento"
            : modal?.kind === "source"
              ? "Conectar una fuente"
              : modal?.kind === "case-create"
                ? "Preparar un caso reproducible"
                : modal?.kind === "case"
                  ? modal.row?.title
                  : "Resultado";
  return (
    <div className={embedded ? "app-shell foundry-embedded" : "app-shell"}>
      {!embedded && <aside className="sidebar">
        <div className="brand">
          <span className="brand-icon">
            <Layers3 size={19} />
          </span>
          <div>
            <div className="text-sm font-semibold tracking-tight">
              Conversation Foundry
            </div>
          </div>
        </div>
        <nav>
          {nav.map(([id, label, Icon]) => (
            <button
              key={id}
              className={"nav-button " + (page === id ? "selected" : "")}
              onClick={() => {
                setPage(id);
                setModal(null);
              }}
              aria-current={page === id ? "page" : undefined}
            >
              <Icon size={17} />
              {label}
            </button>
          ))}
        </nav>
        <div className="sidebar-footer mt-auto p-5">
          <div className="flex items-center justify-between mt-5 text-xs muted">
            <span className="flex gap-2 items-center">
              <span className="pending-dot" />
              Entorno local
            </span>
            <span>v1.0</span>
          </div>
        </div>
      </aside>}
      <div className="main-shell">
        <header className="topbar">
          {!embedded && <div className="flex items-center gap-2 text-xs muted">
            <span>Foundry</span>
            <ChevronRight size={13} />
            <span className="text-foreground">
              {nav.find((n) => n[0] === page)?.[1]}
            </span>
          </div>}
          <div className="btn-wrap">
            <span className="hidden sm:inline text-xs muted">
              {data?.config.capture ? "Captura activa" : "Captura pausada"}
            </span>
            <Button
              variant="ghost"
              size="sm"
              onClick={() => refresh()}
              aria-label="Actualizar datos"
            >
              <RefreshCw size={14} />
              <span className="hidden sm:inline">Actualizar</span>
            </Button>
          </div>
        </header>
        <main className="page-body">
          {auth ? (
            <div className="max-w-lg mx-auto mt-12">
              <Panel title="Conecta tu sesión local">
                <div className="panel-content">
                  <Notice>
                    Ejecuta conversation-foundry open o introduce tu token.
                  </Notice>
                  <Form
                    onSave={async (v) => {
                      setToken(v.token);
                      setNotice(null);
                      setError("");
                      await refresh();
                    }}
                  >
                    <Field
                      name="token"
                      label="Token de acceso local"
                      type="password"
                      required
                    />
                    <Button type="submit">Conectar</Button>
                  </Form>
                  {error && (
                    <p className="text-xs text-destructive mt-4">{error}</p>
                  )}
                </div>
              </Panel>
            </div>
          ) : !data ? (
            <>
              <Heading title="Conectando…" />
              {error ? (
                <Notice tone="error">
                  {error}
                  <Button className="ml-3" variant="outline" onClick={refresh}>
                    Reintentar
                  </Button>
                </Notice>
              ) : (
                <Loading />
              )}
            </>
          ) : (
            <>
              {error && <Notice tone="error">{error}</Notice>}
              {page === "dashboard" ? (
                <Dashboard {...p} />
              ) : page === "sources" ? (
                <Sources {...p} />
              ) : page === "conversations" ? (
                <Conversations {...p} />
              ) : page === "learning" ? (
                <Learning {...p} />
              ) : page === "experiments" ? (
                <Experiments {...p} />
              ) : (
                <Settings {...p} />
              )}
            </>
          )}
        </main>
      </div>
      <Dialog
        open={!!modal}
        onOpenChange={(v) => {
          if (!v) setModal(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{title}</DialogTitle>
          </DialogHeader>
          {notice?.error && (
            <div role="alert" className="notice error flex gap-4 items-start">
              <span className="flex-1">{notice.text}</span>
              <button aria-label="Cerrar aviso" onClick={() => setNotice(null)}><X size={16} /></button>
            </div>
          )}
          <div
            className="modal-body"
            key={modal?.kind + ":" + (modal?.id || modal?.row?.id || "")}
          >
            {modal && data && (
              <ModalBody modal={modal} p={p} close={() => setModal(null)} />
            )}
          </div>
        </DialogContent>
      </Dialog>
      {notice && !(modal && notice.error) && (
        <div
          role={notice.error ? "alert" : "status"}
          className={
            "toast notice " +
            (notice.error ? "error" : notice.warning ? "warning" : "success")
          }
        >
          <div className="flex gap-4 items-start">
            <span className="flex-1">{notice.text}</span>
            <button aria-label="Cerrar aviso" onClick={() => setNotice(null)}>
              <X size={16} />
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
