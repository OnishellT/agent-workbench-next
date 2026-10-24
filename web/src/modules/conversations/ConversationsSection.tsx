import { useEffect, useState } from "react";
import type { Language } from "@/app/language";
import { Conversations } from "./Conversations";
import { Example } from "./Example";
import { Sources } from "./Sources";
import { conversationViews, viewOf, type ConversationView } from "./domain";
import { translate } from "./i18n";

export interface ConversationsSectionProps {
  locale: Language;
  /** Shell navigation: cross-section links and this section's own view id. */
  onNavigate?: (section: string, view?: string) => void;
  /** View requested by the shell (?section=conversations&view=…). An unknown or absent
   *  value keeps the tab the operator is on instead of resetting the screen. */
  view?: string;
}

/** Conversaciones capturadas, sus fuentes y el ejemplo guiado: una sola sección del
 *  dominio Foundry. El estado de cada pantalla vive en la URL compartida
 *  (?conversation=, ?revision=, ?event=, filtros), así que recargar o volver desde
 *  Tareas conserva el destino exacto y ninguna revisión se mezcla con otra. */
export function ConversationsSection({ locale, onNavigate, view }: ConversationsSectionProps) {
  const t = (value: unknown) => translate(value, locale);
  const [current, setCurrent] = useState<ConversationView>(() => viewOf(view));

  useEffect(() => {
    if (view) setCurrent(viewOf(view));
  }, [view]);

  function open(next: ConversationView) {
    setCurrent(next);
    // The shell keeps unrelated query parameters, so ?conversation= survives here.
    onNavigate?.("conversations", next);
  }

  return (
    <div className="space-y-5">
      <nav
        className="flex flex-wrap gap-1 border-b"
        aria-label={`${t("Secciones de")} ${t("Conversaciones")}`}
      >
        {conversationViews.map(([id, label]) => (
          <button
            key={id}
            type="button"
            aria-current={current === id ? "page" : undefined}
            className={`-mb-px border-b-2 px-3 py-2 text-sm ${
              current === id
                ? "border-primary font-medium text-foreground"
                : "border-transparent text-muted-foreground hover:text-foreground"
            }`}
            onClick={() => open(id as ConversationView)}
          >
            {t(label)}
          </button>
        ))}
      </nav>

      {current === "conversations" && (
        <Conversations locale={locale} onNavigate={onNavigate} onOpenView={open} />
      )}
      {current === "sources" && <Sources locale={locale} />}
      {current === "example" && <Example locale={locale} onNavigate={onNavigate} />}
    </div>
  );
}

export default ConversationsSection;
