import { FolderOpen, Plus, RefreshCw, Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { type Row, date } from "@/api";
import { Empty, Heading, Notice, Panel, Status } from "@/components/primitives";
import type { Props } from "@/lib/types";

export function Sources(p: Props) {
  const c = p.data.capture_status || {};
  return (
    <>
      <Heading
        title="Fuentes conectadas"
        actions={
          <>
            <Button variant="outline" onClick={() => p.run("source.discover", {}, "Fuentes detectadas en este equipo.")}>
              <Search /> Detectar fuentes
            </Button>
            <Button
              variant="outline"
              disabled={!p.data.config.capture}
              onClick={() => p.run("scan", {}, "Captura solicitada.")}
            >
              <RefreshCw />
              Escanear ahora
            </Button>
            <Button onClick={() => p.open({ kind: "source" })}>
              <Plus />
              Añadir fuente
            </Button>
          </>
        }
      />
      <div className="grid lg:grid-cols-[1.6fr_1fr] gap-5">
        <Panel
          title="Carpetas y archivos"
          caption={`${p.data.sources.length} fuentes registradas`}
        >
          {p.data.sources.length ? (
            p.data.sources.map((s) => (
              <div
                className="flex items-start gap-4 justify-between p-5 border-b last:border-0"
                key={s.id}
              >
                <div className="flex gap-3 min-w-0">
                  <FolderOpen
                    size={18}
                    className="text-primary shrink-0 mt-0.5"
                  />
                  <div>
                    <div className="font-medium text-xs break-all mb-1">
                      {s.path}
                    </div>
                    <p className="meta">
                      {s.adapter} · {date(s.created)}
                    </p>
                    {s.error && (
                      <p className="text-xs text-destructive mt-2 break-words">
                        {s.error === "source_byte_limit"
                          ? "Archivo mayor que el límite configurado. Aumenta Tamaño máximo de fuente."
                          : s.error}
                      </p>
                    )}
                  </div>
                </div>
                <div className="flex items-center gap-3 shrink-0">
                  <Button variant="ghost" size="sm" onClick={() => p.open({kind: "source", row: s})} aria-label={"Cambiar ruta de " + s.path}>
                    <FolderOpen /> Cambiar ruta
                  </Button>
                  <Status value={s.enabled ? "active" : "Pausada"} />
                  <Switch
                    checked={!!s.enabled}
                    aria-label={(s.enabled ? "Pausar " : "Activar ") + s.path}
                    onCheckedChange={(enabled) =>
                      p.run(
                        "source.toggle",
                        { id: s.id, enabled },
                        "Fuente actualizada.",
                      )
                    }
                  />
                </div>
              </div>
            ))
          ) : (
            <Empty
              title="Sin fuentes"
              text="Admite Codex, Claude Code, Pi, OMP y archivos JSONL canónicos."
              action={
                <Button onClick={() => p.open({ kind: "source" })}>
                  Añadir fuente
                </Button>
              }
            />
          )}
        </Panel>
        <div className="space-y-5">
          <Panel title="Última captura">
            <div className="panel-content">
              <div className="grid grid-cols-3 gap-3 mb-5">
                {[
                  ["Archivos", c.files || 0],
                  ["Cambios", c.changed || 0],
                  ["Errores", c.errors || 0],
                ].map(([l, v]) => (
                  <div key={l} className="rounded-lg border p-3">
                    <div className="meta">{l}</div>
                    <div className="text-xl mt-2">{v}</div>
                  </div>
                ))}
              </div>
              {c.limited && (
                <Notice tone="warning">
                  Se alcanzó el presupuesto de tiempo o archivos. Puedes ampliar
                  los límites en Configuración.
                </Notice>
              )}
              {c.error && <Notice tone="error">{c.error}</Notice>}
              {(c.issues || []).slice(0, 20).map((i: Row, n: number) => (
                <div className="border-t pt-3 mt-3" key={n}>
                  <p className="text-xs break-all">{i.file_name || "Fuente"}</p>
                  <p className="meta">
                    {i.reason === "source_byte_limit"
                      ? "Archivo excede el límite configurado; aumenta Tamaño máximo de fuente."
                      : i.reason === "source_changed_retryable"
                        ? "El archivo sigue creciendo; se reintentará la captura."
                        : i.reason === "source_unavailable"
                          ? "La fuente no está disponible."
                          : "No se pudo capturar este registro. Revisa el archivo y su formato."}
                  </p>
                </div>
              ))}
              {(c.issues || []).length > 20 && (
                <p className="meta mt-3">
                  Se muestran 20 de {c.issues.length} incidencias.
                </p>
              )}
            </div>
          </Panel>
          {!p.data.config.capture && (
            <Notice tone="warning">
              Captura pausada.
              <Button
                variant="link"
                size="sm"
                onClick={() => p.onOpenCaptureSettings ? p.onOpenCaptureSettings() : p.open({ kind: "page", id: "settings" })}
              >
                Activar en Configuración
              </Button>
            </Notice>
          )}
        </div>
      </div>
    </>
  );
}
