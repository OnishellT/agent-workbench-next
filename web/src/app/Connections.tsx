import { useEffect, useRef, useState } from "react";
import {
  ArrowUpRight,
  Computer,
  Download,
  Link2,
  Plus,
  Server,
  Trash2,
  Upload,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  CONNECTIONS_KEY,
  MAX_CONNECTIONS_BYTES,
  connectionName,
  connectionOrigin,
  exportConnections,
  importConnections,
  mergeConnection,
  readConnections,
  type ConnectionDTO,
} from "./connections-store";
import { translateShell } from "./i18n";
import type { Language } from "./language";

function Section({
  title,
  icon,
  children,
}: {
  title: string;
  icon?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between gap-3">
        <CardTitle role="heading" aria-level={3}>
          {title}
        </CardTitle>
        {icon}
      </CardHeader>
      <CardContent>{children}</CardContent>
    </Card>
  );
}

/** Other installations this browser remembers (UI-05/UI-06). Only names and
 *  addresses are stored locally; opening one is a plain navigation to that
 *  installation's own login, never an embedded frame and never a shared session. */
export default function Connections({ language }: { language: Language }) {
  const t = (value: unknown) => translateShell(value, language);
  const [connections, setConnections] = useState<ConnectionDTO[]>([]);
  const [name, setName] = useState("");
  const [address, setAddress] = useState("");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [importing, setImporting] = useState(false);
  const importFile = useRef<HTMLInputElement>(null);
  const currentOrigin = location.origin;
  const tracked = connections.find((c) => c.origin === currentOrigin);
  const saved = connections.filter((c) => c.origin !== currentOrigin);

  useEffect(() => {
    const load = () => {
      try {
        setConnections(readConnections(localStorage.getItem(CONNECTIONS_KEY)));
        setError("");
      } catch {
        setError("No se pudieron leer las conexiones guardadas en este navegador.");
      }
    };
    load();
    const changed = (event: StorageEvent) => {
      if (event.key === CONNECTIONS_KEY || event.key === null) load();
    };
    window.addEventListener("storage", changed);
    return () => window.removeEventListener("storage", changed);
  }, []);

  function store(next: ConnectionDTO[], message: string) {
    try {
      localStorage.setItem(CONNECTIONS_KEY, JSON.stringify(next));
      setConnections(next);
      setError("");
      setNotice(message);
    } catch {
      setError(
        "El navegador no permitió guardar las conexiones. Revisa sus permisos de almacenamiento.",
      );
      setNotice("");
    }
  }

  function download() {
    const url = URL.createObjectURL(
      new Blob([exportConnections(connections)], { type: "application/json" }),
    );
    const link = document.createElement("a");
    link.href = url;
    link.download = "workbench-connections.json";
    link.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
    setNotice("Conexiones exportadas. El archivo contiene sólo nombres y direcciones.");
    setError("");
  }

  async function upload(file: File) {
    setImporting(true);
    setError("");
    setNotice("");
    try {
      // File size counts bytes; the parser counts UTF-16 string units.
      if (file.size > MAX_CONNECTIONS_BYTES)
        throw new Error("El archivo de conexiones debe tener menos de 64 KiB.");
      const next = importConnections(connections, await file.text());
      store(next, "Conexiones importadas. Inicia sesión por separado en cada instalación.");
    } catch (value) {
      setError(
        value instanceof SyntaxError
          ? "El archivo no contiene un JSON válido."
          : (value as Error).message,
      );
    } finally {
      setImporting(false);
      if (importFile.current) importFile.current.value = "";
    }
  }

  return (
    <div className="space-y-5">
      <Section
        title={t("Tus instalaciones")}
        icon={<Link2 className="mt-1 size-4 shrink-0 text-muted-foreground" />}
      >
        <div className="divide-y">
          <div className="flex flex-wrap items-center gap-3 py-4">
            <span className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-secondary text-secondary-foreground">
              <Server className="size-4" />
            </span>
            <div className="min-w-0 flex-1">
              <strong className="block truncate">
                {tracked?.name || t("Instalación actual")}
              </strong>
              <span className="mt-1 block break-all text-xs text-muted-foreground">
                {currentOrigin}
              </span>
            </div>
            {tracked?.name && <Badge variant="secondary">{t("Instalación actual")}</Badge>}
            {tracked && (
              <Button
                variant="ghost"
                size="icon"
                aria-label={`${t("Quitar conexión")}: ${tracked.name}`}
                onClick={() =>
                  store(
                    connections.filter((c) => c.origin !== currentOrigin),
                    "Conexión quitada de este navegador.",
                  )
                }
              >
                <Trash2 />
              </Button>
            )}
          </div>
          {saved.map((connection) => (
            <div key={connection.origin} className="flex flex-wrap items-center gap-3 py-4">
              <span className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-secondary text-secondary-foreground">
                <Computer className="size-4" />
              </span>
              <div className="min-w-0 flex-1">
                <strong className="block truncate">{connection.name}</strong>
                <span className="mt-1 block break-all text-xs text-muted-foreground">
                  {connection.origin}
                </span>
              </div>
              <a
                className="inline-flex min-h-9 items-center gap-1.5 rounded-lg border px-3 text-sm text-primary hover:bg-muted"
                href={connection.origin}
                rel="noopener noreferrer"
                referrerPolicy="no-referrer"
              >
                {t("Abrir")}
                <ArrowUpRight className="size-3.5" />
              </a>
              <Button
                variant="ghost"
                size="icon"
                aria-label={`${t("Quitar conexión")}: ${connection.name}`}
                onClick={() =>
                  store(
                    connections.filter((c) => c.origin !== connection.origin),
                    "Conexión quitada de este navegador.",
                  )
                }
              >
                <Trash2 />
              </Button>
            </div>
          ))}
        </div>
        <div className="mt-4 flex flex-wrap gap-2">
          <Button
            variant="outline"
            disabled={!connections.length || importing}
            onClick={download}
          >
            <Download className="size-3.5" />
            {t("Exportar direcciones")}
          </Button>
          <Button
            variant="outline"
            disabled={importing}
            onClick={() => importFile.current?.click()}
          >
            <Upload className="size-3.5" />
            {importing ? t("Importando…") : t("Importar direcciones")}
          </Button>
          <input
            ref={importFile}
            type="file"
            accept="application/json,.json"
            hidden
            aria-label={t("Importar archivo de conexiones")}
            disabled={importing}
            onChange={(event) => {
              const file = event.target.files?.[0];
              if (file) void upload(file);
            }}
          />
        </div>
      </Section>

      <Section
        title={t("Añadir conexión")}
        icon={<Plus className="mt-1 size-4 shrink-0 text-muted-foreground" />}
      >
        <form
          className="grid max-w-xl gap-4"
          onSubmit={(event) => {
            event.preventDefault();
            setError("");
            setNotice("");
            try {
              const entry = {
                name: connectionName(name),
                origin: connectionOrigin(address),
              };
              store(
                mergeConnection(connections, entry),
                connections.some((c) => c.origin === entry.origin)
                  ? "Nombre de conexión actualizado."
                  : "Conexión guardada. Ábrela para iniciar sesión.",
              );
              setName("");
              setAddress("");
            } catch (value) {
              setError((value as Error).message);
            }
          }}
        >
          <label className="grid gap-2 text-sm font-medium" htmlFor="connection-name">
            {t("Nombre")}
            <Input
              id="connection-name"
              value={name}
              onChange={(event) => setName(event.target.value)}
              placeholder={t("Mi VPS")}
              autoComplete="off"
              maxLength={64}
              required
            />
          </label>
          <label className="grid gap-2 text-sm font-medium" htmlFor="connection-address">
            {t("Dirección")}
            <Input
              id="connection-address"
              type="url"
              value={address}
              onChange={(event) => setAddress(event.target.value)}
              placeholder="https://workbench.example.com"
              autoComplete="off"
              spellCheck={false}
              maxLength={2048}
              required
            />
          </label>
          <Button
            type="submit"
            className="justify-self-start"
            disabled={!name.trim() || !address.trim()}
          >
            <Plus className="size-3.5" />
            {t("Guardar conexión")}
          </Button>
        </form>
      </Section>

      {notice && (
        <p role="status" className="rounded-lg border bg-card p-4 text-sm">
          {t(notice)}
        </p>
      )}
      {error && (
        <p
          role="alert"
          className="rounded-lg border border-destructive/30 bg-destructive/5 p-4 text-sm text-destructive"
        >
          {t(error)}
        </p>
      )}
    </div>
  );
}
