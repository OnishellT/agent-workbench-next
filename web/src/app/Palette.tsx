import { useEffect, useMemo, useRef, useState } from "react";
import { CornerDownLeft, Search } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { matchCommands, type CommandEntry } from "./commands";
import { translateShell } from "./i18n";
import type { Language } from "./language";

/** Ctrl/Cmd+K navigation palette (UI-02). The house Dialog owns the focus trap and
 *  returns focus to the trigger on close; this component owns search and the
 *  keyboard cursor, which never leaves the input (aria-activedescendant). */
export default function Palette({
  open,
  onClose,
  entries,
  onRun,
  language,
  returnFocus,
}: {
  open: boolean;
  onClose: () => void;
  entries: CommandEntry[];
  onRun: (id: string) => void;
  language: Language;
  /** Element that opened the palette: the Dialog returns focus to it on close. */
  returnFocus: React.RefObject<HTMLElement | null>;
}) {
  const t = (value: unknown) => translateShell(value, language);
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const results = useMemo(() => matchCommands(entries, query), [entries, query]);
  const list = useRef<HTMLUListElement>(null);
  const search = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!open) return;
    setQuery("");
    setActive(0);
  }, [open]);
  useEffect(() => setActive(0), [query]);
  useEffect(() => {
    list.current?.children[active]?.scrollIntoView({ block: "nearest" });
  }, [active, results]);

  function run(index: number) {
    const entry = results[index];
    if (!entry) return;
    onRun(entry.id);
    onClose();
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(value: boolean) => {
        if (!value) onClose();
      }}
    >
      <DialogContent
        showCloseButton={false}
        initialFocus={search}
        finalFocus={() => returnFocus.current}
        className="top-[12%] max-h-[80dvh] translate-y-0 gap-3 overflow-hidden p-0 sm:max-w-xl"
        closeLabel={t("Cerrar")}
      >
        <DialogTitle className="sr-only">
          {t("Ir a una sección o vista")}
        </DialogTitle>
        <DialogDescription className="sr-only">
          {t("Buscar una sección, una vista o un ajuste…")}
        </DialogDescription>
        <div className="flex items-center gap-2 border-b px-4 py-3">
          <Search className="size-4 shrink-0 text-muted-foreground" />
          <Input
            ref={search}
            aria-label={t("Buscar sección o ajuste")}
            role="combobox"
            aria-expanded="true"
            aria-controls="wb-palette-results"
            aria-activedescendant={
              results[active] ? `wb-cmd-${results[active].id}` : undefined
            }
            className="h-8 border-0 bg-transparent px-0 focus-visible:ring-0 dark:bg-transparent"
            placeholder={t("Buscar una sección, una vista o un ajuste…")}
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "ArrowDown") {
                event.preventDefault();
                setActive((index) =>
                  results.length ? (index + 1) % results.length : 0,
                );
              } else if (event.key === "ArrowUp") {
                event.preventDefault();
                setActive((index) =>
                  results.length ? (index - 1 + results.length) % results.length : 0,
                );
              } else if (event.key === "Enter") {
                event.preventDefault();
                run(active);
              }
            }}
          />
          <kbd className="hidden shrink-0 rounded border px-1.5 py-0.5 text-[11px] text-muted-foreground sm:block">
            Esc
          </kbd>
        </div>
        {results.length ? (
          <ul
            ref={list}
            id="wb-palette-results"
            role="listbox"
            aria-label={t("Ir a una sección o vista")}
            className="max-h-[60dvh] overflow-y-auto pb-2"
          >
            {results.map((entry, index) => (
              <li
                key={entry.id}
                id={`wb-cmd-${entry.id}`}
                role="option"
                aria-selected={index === active}
                className={`mx-2 flex cursor-pointer items-center gap-3 rounded-lg px-3 py-2.5 ${
                  index === active ? "bg-accent text-accent-foreground" : ""
                }`}
                onMouseMove={() => setActive(index)}
                onClick={() => run(index)}
              >
                <span className="min-w-0 flex-1">
                  <span className="block truncate font-medium">
                    {t(entry.label)}
                  </span>
                  <span className="mt-0.5 block truncate text-xs text-muted-foreground">
                    {t(entry.group)}
                  </span>
                </span>
                {index === active && (
                  <CornerDownLeft className="size-3.5 shrink-0 text-muted-foreground" />
                )}
              </li>
            ))}
          </ul>
        ) : (
          <p role="status" className="px-4 py-6 text-center text-sm text-muted-foreground">
            {t("No hay coincidencias con ese nombre.")}
          </p>
        )}
      </DialogContent>
    </Dialog>
  );
}
