import { lazy, Suspense, useEffect, useMemo, useRef, useState } from "react";
import {
  AlertCircle,
  ArrowRight,
  ChevronRight,
  Layers3,
  LogOut,
  Menu,
  Moon,
  RefreshCw,
  Search,
  Sun,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetTitle,
} from "@/components/ui/sheet";

import { api } from "./api";
import { useBootstrap, useRoute } from "./hooks";
import type { CommandEntry } from "./commands";
import Connections from "./Connections";
import Palette from "./Palette";
import Settings from "./Settings";
import SectionBoundary from "./SectionBoundary";
import { translateShell } from "./i18n";
import { sectionURL, sections } from "./navigation";
import { applyLanguage, initialLanguage, type Language } from "./language";
import { applyTheme, initialTheme, type Theme } from "./theme";

const HostsSection = lazy(() =>
  import("@/modules/hosts/HostsSection").then((module) => ({ default: module.HostsSection })),
);
const ModelsSection = lazy(() => import("@/modules/models/ModelsSection"));
const ConversationsSection = lazy(() => import("@/modules/conversations/ConversationsSection"));
const TasksSection = lazy(() => import("@/modules/conversations/TasksSection"));
const FoundrySection = lazy(() => import("@/modules/foundry/FoundrySection"));
const SimSection = lazy(() =>
  import("@/modules/sim/SimSection").then((module) => ({ default: module.SimSection })),
);

/** Suspense fallback that also reports the loading state to the content region. */
function SectionPending({
  label,
  onBusy,
}: {
  label: string;
  onBusy: (busy: boolean) => void;
}) {
  useEffect(() => {
    onBusy(true);
    return () => onBusy(false);
  }, [onBusy]);
  return <p role="status">{label}</p>;
}

export default function App() {
  const [current, setCurrent] = useRoute();
  const [menu, setMenu] = useState(false);
  const [palette, setPalette] = useState(false);
  const [mobile, setMobile] = useState(
    () => window.matchMedia("(max-width: 900px)").matches,
  );
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState(false);
  const [sectionBusy, setSectionBusy] = useState(false);
  const [language, setLanguage] = useState<Language>(initialLanguage);
  const [theme, setTheme] = useState<Theme>(initialTheme);
  const paletteTrigger = useRef<HTMLButtonElement>(null);
  const paletteReturn = useRef<HTMLElement | null>(null);
  const paletteOpen = useRef(false);
  paletteOpen.current = palette;
  const { boot, setBoot, error, setError, auth, refresh, saveSettings } =
    useBootstrap(paletteOpen);
  const t = (value: unknown) => translateShell(value, language);

  useEffect(() => {
    const viewport = window.matchMedia("(max-width: 900px)");
    const resize = () => {
      setMobile(viewport.matches);
      if (!viewport.matches) setMenu(false);
    };
    viewport.addEventListener("change", resize);
    return () => viewport.removeEventListener("change", resize);
  }, []);

  // UI-02: Ctrl/Cmd+K opens the palette from anywhere; Escape and the overlay
  // belong to the Dialog, which returns focus to the button that opened it.
  useEffect(() => {
    const listener = (event: KeyboardEvent) => {
      if (!(event.metaKey || event.ctrlKey) || event.key.toLowerCase() !== "k") return;
      event.preventDefault();
      if (paletteOpen.current) setPalette(false);
      else openPalette();
    };
    window.addEventListener("keydown", listener);
    return () => window.removeEventListener("keydown", listener);
  }, []);

  useEffect(() => applyTheme(theme), [theme]);
  useEffect(() => applyLanguage(language), [language]);
  useEffect(() => {
    document.title = current.section
      ? `${t(current.section.label)} · Agent Workbench`
      : "Agent Workbench";
    setMenu(false);
  }, [current, language]);

  // Opening from anywhere remembers where focus was, so Escape returns it there
  // (falling back to the palette button when focus sat on the document body).
  function openPalette() {
    const active = document.activeElement;
    paletteReturn.current =
      active instanceof HTMLElement && active !== document.body
        ? active
        : paletteTrigger.current;
    setMenu(false);
    setPalette(true);
  }

  function navigate(section: string, view?: string, host?: string) {
    const target = sections.find((s) => s.id === section);
    const next = {
      section: target ?? null,
      view: view ?? target?.views[0]?.[0] ?? "",
    };
    const url = sectionURL(section, next.view, host);
    setMenu(false);
    // Reselecting the current route must not add a history entry.
    if (
      current.section?.id === next.section?.id &&
      current.view === next.view &&
      url.href === location.href
    )
      return;
    history.pushState(null, "", url);
    setCurrent(next);
    window.scrollTo(0, 0);
  }

  const commands = useMemo(() => {
    const entries: CommandEntry[] = [];
    for (const section of sections) {
      entries.push({
        id: `section:${section.id}`,
        group: section.group,
        label: section.label,
        keywords: `${section.group} ${section.id}`,
      });
      for (const [id, label] of section.views)
        entries.push({
          id: `view:${section.id}/${id}`,
          group: section.label,
          label,
          keywords: `${section.label} ${id}`,
        });
    }
    entries.push({
      id: "action:theme",
      group: "Preferencias rápidas",
      label: "Cambiar de tema",
      keywords: "tema theme claro oscuro dark light color",
    });
    entries.push({
      id: "action:language",
      group: "Preferencias rápidas",
      label: language === "es" ? "Usar inglés" : "Usar español",
      keywords: "idioma language spanish english",
    });
    return entries;
  }, [language, theme]);

  function runCommand(id: string) {
    if (id.startsWith("section:")) {
      navigate(id.slice("section:".length));
      return;
    }
    if (id.startsWith("view:")) {
      const [section, view] = id.slice("view:".length).split("/");
      navigate(section, view);
      return;
    }
    if (id === "action:theme") setTheme(theme === "dark" ? "light" : "dark");
    if (id === "action:language") setLanguage(language === "es" ? "en" : "es");
  }

  // Bootstrap reports module mount state ("mounted" | "not_implemented"), not processes.
  const modules: Record<string, string> = boot?.modules ?? {};
  const unmounted = Object.entries(modules)
    .filter(([, state]) => state !== "mounted")
    .map(([name]) => name);
  const shortcut = /Mac|iPhone|iPad/.test(navigator.platform) ? "⌘ K" : "Ctrl K";

  const navigation = (
    <>
      <button
        className="flex items-center gap-3 rounded-lg px-3 py-2 text-left"
        onClick={() => navigate(sections[0].id)}
      >
        <span className="flex size-9 items-center justify-center rounded-xl bg-primary text-primary-foreground">
          <Layers3 size={20} />
        </span>
        <span className="font-semibold tracking-tight">Workbench</span>
      </button>
      <button
        ref={paletteTrigger}
        className="flex items-center gap-3 rounded-lg border px-3 py-2 text-left text-sm text-muted-foreground hover:text-foreground"
        onClick={openPalette}
      >
        <Search size={15} />
        <span className="flex-1">{t("Ir a…")}</span>
        <kbd className="rounded border px-1.5 py-0.5 text-[11px]">{shortcut}</kbd>
      </button>
      <nav className="min-h-0 flex-1 space-y-1 overflow-y-auto" aria-label={t("Navegación principal")}>
        {[...new Set(sections.map((s) => s.group))].map((group) => (
          <div key={group} className="mb-5 space-y-1">
            <p className="mb-2 px-3 text-xs font-semibold text-muted-foreground">
              {t(group)}
            </p>
            {sections
              .filter((s) => s.group === group)
              .map(({ id, label, icon: Icon }) => (
                <Button
                  key={id}
                  variant={current.section?.id === id ? "secondary" : "ghost"}
                  className="h-10 w-full justify-start gap-3"
                  aria-current={current.section?.id === id ? "page" : undefined}
                  onClick={() => navigate(id)}
                >
                  <Icon className="size-4" />
                  {t(label)}
                </Button>
              ))}
          </div>
        ))}
      </nav>
      <div className="space-y-3 border-t pt-4">
        {unmounted.length > 0 && (
          <p
            role="status"
            aria-label={`${t("Módulos sin montar")}: ${unmounted.join(", ")}`}
            className="flex items-center gap-2 px-3 text-xs text-destructive"
          >
            <AlertCircle size={14} className="shrink-0" />
            <span className="min-w-0 break-words">{unmounted.join(", ")}</span>
          </p>
        )}
        <div className="flex items-center gap-2 px-3">
          <NativeSelect
            aria-label={t("Idioma")}
            value={language}
            onChange={(e) => setLanguage(e.target.value as Language)}
          >
            <NativeSelectOption value="es">Español</NativeSelectOption>
            <NativeSelectOption value="en">English</NativeSelectOption>
          </NativeSelect>
          <Button
            variant="ghost"
            size="icon"
            aria-label={theme === "dark" ? t("Usar tema claro") : t("Usar tema oscuro")}
            aria-pressed={theme === "dark"}
            onClick={() => setTheme(theme === "dark" ? "light" : "dark")}
          >
            {theme === "dark" ? <Sun /> : <Moon />}
          </Button>
        </div>
        <Button
          variant="ghost"
          className="w-full justify-start gap-3"
          onClick={async () => {
            setBusy(true);
            try {
              await api("/api/logout", { method: "POST" });
            } catch (e) {
              setError((e as Error).message);
            } finally {
              setBusy(false);
              setBoot(null);
              await refresh();
            }
          }}
          disabled={busy}
        >
          <LogOut className="size-4" />
          {t("Salir")}
        </Button>
      </div>
    </>
  );

  if (auth)
    return (
      <div className="flex min-h-dvh items-center justify-center p-4">
        <div className="w-full max-w-sm space-y-5 rounded-xl border bg-card p-6">
          <span className="flex size-11 items-center justify-center rounded-xl bg-primary text-primary-foreground">
            <Layers3 size={24} />
          </span>
          <h1 className="text-xl font-semibold tracking-tight">Agent Workbench</h1>
          <form
            className="space-y-3"
            onSubmit={async (e) => {
              e.preventDefault();
              setError("");
              setBusy(true);
              try {
                await api("/api/auth", { data: { token } });
                setToken("");
                await refresh();
              } catch (e) {
                setError((e as Error).message);
              } finally {
                setBusy(false);
              }
            }}
          >
            <label className="block text-sm font-medium" htmlFor="access-token">
              {t("Token de acceso")}
            </label>
            <Input
              id="access-token"
              type="password"
              autoComplete="current-password"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              required
            />
            <Button disabled={busy} type="submit" className="w-full">
              {busy ? t("Conectando…") : t("Entrar")}
              <ArrowRight size={15} />
            </Button>
          </form>
          {error && (
            <p role="alert" className="text-sm text-destructive">
              {t(error)}
            </p>
          )}
        </div>
      </div>
    );

  // Sections only render once bootstrap answered: no section may query the API
  // before the instance reported a session (or asked for the token).
  if (!boot)
    return (
      <div className="flex min-h-dvh items-center justify-center p-4">
        <div
          role="status"
          aria-live="polite"
          className="w-full max-w-sm space-y-4 rounded-xl border bg-card p-6 text-center"
        >
          <span className="mx-auto flex size-11 items-center justify-center rounded-xl bg-primary text-primary-foreground">
            <Layers3 size={24} />
          </span>
          {error ? (
            <>
              <p role="alert" className="text-sm text-destructive">
                {t(error)}
              </p>
              <Button onClick={() => void refresh()}>{t("Reintentar")}</Button>
            </>
          ) : (
            <p className="text-sm text-muted-foreground">
              {t("Conectando con esta instalación…")}
            </p>
          )}
        </div>
      </div>
    );

  return (
    <div className="flex min-h-dvh bg-background">
      <a
        href="#wb-content"
        className="sr-only focus:not-sr-only focus:fixed focus:top-3 focus:left-3 focus:z-50 focus:rounded-lg focus:bg-primary focus:px-4 focus:py-2 focus:text-primary-foreground"
      >
        {t("Salta al contenido")}
      </a>
      {mobile ? (
        <Sheet open={menu} onOpenChange={setMenu}>
          <SheetContent
            side="left"
            className="w-[min(300px,calc(100vw-40px))] overflow-y-auto p-4"
            closeLabel={t("Cerrar")}
          >
            <SheetTitle className="sr-only">{t("Navegación principal")}</SheetTitle>
            <SheetDescription className="sr-only">
              {t("Secciones y preferencias de Workbench")}
            </SheetDescription>
            {navigation}
          </SheetContent>
        </Sheet>
      ) : (
        <aside
          className="fixed inset-y-0 left-0 z-30 flex w-60 flex-col gap-4 overflow-y-auto border-r bg-sidebar px-4 py-5"
          aria-label={t("Navegación principal")}
        >
          {navigation}
        </aside>
      )}
      <div className={mobile ? "flex w-full flex-col" : "ml-60 flex w-full flex-col"}>
        <header className="sticky top-0 z-20 flex h-16 items-center gap-3 border-b bg-background/95 px-4 backdrop-blur sm:px-7">
          <Button
            variant="ghost"
            size="icon"
            className={mobile ? "" : "hidden"}
            aria-label={t("Abrir navegación")}
            aria-expanded={menu}
            onClick={() => setMenu(true)}
          >
            <Menu size={18} />
          </Button>
          <div className="flex min-w-0 items-center gap-2 text-sm">
            <span className="text-muted-foreground">
              {current.section ? t(current.section.group) : "Agent Workbench"}
            </span>
            <ChevronRight size={13} className="shrink-0 text-muted-foreground" />
            <strong className="truncate">
              {current.section ? t(current.section.label) : t("Sección no encontrada")}
            </strong>
          </div>
          <Button
            variant="ghost"
            size="icon"
            className="ml-auto"
            aria-label={t("Actualizar estado de la instalación")}
            onClick={() => void refresh()}
          >
            <RefreshCw size={15} />
          </Button>
        </header>
        {error && (
          <div
            role="alert"
            className="flex flex-wrap items-center gap-3 border-b bg-destructive/5 px-4 py-3 text-sm text-destructive sm:px-7"
          >
            <AlertCircle size={16} />
            {t(error)}
            <Button variant="ghost" onClick={() => void refresh()}>
              {t("Reintentar")}
            </Button>
          </div>
        )}
        <main
          id="wb-content"
          tabIndex={-1}
          data-section={current.section?.id ?? ""}
          aria-busy={sectionBusy}
          className="mx-auto w-full max-w-[1600px] flex-1 space-y-6 p-4 sm:p-7"
        >
          {current.section ? (
            <>
              <h1 className="text-2xl font-semibold tracking-tight">
                {t(current.section.label)}
              </h1>
              <SectionBoundary
                // Foundry's frame follows the tab by message and keeps its own state, so its
                // view does not remount it like it does for the other sections.
                key={current.section.id === "foundry" ? "foundry" : `${current.section.id}/${current.view}`}
                message={t("No se pudo abrir esta sección. Puedes usar el resto de la aplicación.")}
                retryLabel={t("Reintentar")}
              >
                <Suspense
                  fallback={
                    <SectionPending label={t("Cargando sección…")} onBusy={setSectionBusy} />
                  }
                >

                  {current.section.id === "models" && (
                    <ModelsSection locale={language} view={current.view} onNavigate={navigate} />
                  )}

                  {current.section.id === "conversations" && (
                    <ConversationsSection locale={language} view={current.view} onNavigate={navigate} />
                  )}
                  {current.section.id === "tasks" && (
                    <TasksSection locale={language} view={current.view} onNavigate={navigate} />
                  )}
                  {current.section.id === "foundry" && (
                    <FoundrySection locale={language} view={current.view} onNavigate={navigate} />
                  )}
                  {current.section.id === "sim" && (
                    <SimSection locale={language} url={boot.sim_url} />
                  )}
                  {current.section.id === "connections" && <>
                    <HostsSection locale={language} onNavigate={navigate} sshAgent={boot?.ssh_agent !== false} />
                    <Connections language={language} />
                  </>}
                  {current.section.id === "settings" && (
                    <Settings
                      boot={boot}
                      save={saveSettings}
                      onOpenSection={navigate}
                      language={language}
                    />
                  )}
                </Suspense>
              </SectionBoundary>
            </>
          ) : (
            <div className="space-y-4 rounded-xl border bg-card p-6">
              <h1 className="text-xl font-semibold tracking-tight">
                {t("Sección no encontrada")}
              </h1>
              <div className="flex flex-wrap gap-2">
                {sections.map(({ id, label }) => (
                  <Button key={id} variant="outline" onClick={() => navigate(id)}>
                    {t(label)}
                  </Button>
                ))}
              </div>
            </div>
          )}
        </main>
        <footer className="flex flex-wrap items-center gap-x-3 gap-y-1 border-t px-4 py-4 text-xs text-muted-foreground sm:px-7">
          <span data-release-version={boot?.version ?? ""}>
            Agent Workbench{boot?.version ? ` ${boot.version}` : ""}
          </span>
          <a className="underline underline-offset-2" href="/NOTICES.txt">
            {t("Avisos, licencias y fuente correspondiente")}
          </a>
        </footer>
      </div>
      <Palette
        open={palette}
        onClose={() => setPalette(false)}
        entries={commands}
        onRun={runCommand}
        language={language}
        returnFocus={paletteReturn}
      />
    </div>
  );
}
