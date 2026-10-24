import { Dependencies } from "./Dependencies";
import { useEffect, useRef, useState } from "react";
import { translateError } from "@/app/api";
import { Loader2, Pencil, Plus, RefreshCw, ShieldCheck, Wifi } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select";
import {
  checkWireguard,
  confirmTrust,
  fetchHistory,
  setHistoryConsent,
  syncHostHistory,
  installMagpie,
  magpieStatus,
  saveHost,
  syncHost,
  useHosts,
  hostsSignature,
} from "./client";
import { translate, type Language } from "./i18n";
import type {
  HistoryDTO,
  HostInputDTO,
  HostSummaryDTO,
  MagpieStatusDTO,
} from "./types";

export interface HostsSectionProps {
  locale: Language;
  /** Cross-section link: the target host travels as ?host= so Modelos opens on it. */
  onNavigate?: (section: string, view?: string, host?: string) => void;
  /** False hides the "agent" SSH option: the server has no SSH agent (Docker, service). */
  sshAgent?: boolean;
}

/** Equipos SSH: one registry, its trust pin, the Magpie readiness of the host and the
 *  consent for its remote history. Ported from `Hosts.tsx` (Workbench) and the Equipos
 *  view of the frozen Atlas UI, without a second host list. */
export function HostsSection({ locale, onNavigate, sshAgent = true }: HostsSectionProps) {
  const t = (value: unknown) => translate(value, locale);
  const { hosts, error: loadError, refresh } = useHosts();
  const [history, setHistory] = useState<Record<string, HistoryDTO>>({});
  const [route, setRoute] = useState<Record<string, { text: string; error: boolean }>>(
    {},
  );
  const [magpie, setMagpie] = useState<
    Record<string, { status?: MagpieStatusDTO; error?: string }>
  >({});
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [form, setForm] = useState<{ host: HostSummaryDTO | null } | null>(null);

  // Histories are refetched when the hosts really change, not on every poll.
  const signature = hostsSignature(hosts);
  const hostsRef = useRef(hosts);
  hostsRef.current = hosts;
  const localeRef = useRef(locale);
  localeRef.current = locale;
  useEffect(() => {
    const controller = new AbortController();
    for (const host of hostsRef.current) {
      void fetchHistory(host.id, controller.signal)
        .then((value) => {
          if (!controller.signal.aborted)
            setHistory((previous) => ({ ...previous, [host.id]: value }));
        })
        .catch((value: unknown) => {
          if (!controller.signal.aborted)
            setError(
              translateError(value, (text) => translate(text, localeRef.current), translate("No se pudo consultar el historial.", localeRef.current)),
            );
        });
    }
    return () => controller.abort();
  }, [signature]);

  async function run(id: string, action: () => Promise<string>) {
    setBusy(id);
    setError("");
    setNotice("");
    try {
      setNotice(await action());
    } catch (value) {
      setError(translateError(value, t, t("La operación falló.")));
    } finally {
      setBusy("");
    }
  }

  const checkRoute = (host: HostSummaryDTO) =>
    run(host.id, async () => {
      try {
        const value = await checkWireguard(host.id);
        const text = value.wireguard
          ? `${t("Ruta por ")}${value.interface || t("una interfaz")} · MTU ${value.mtu ?? t("sin dato")}. ${
              value.main_default_uses_wireguard
                ? t("La ruta predeterminada principal también usa WireGuard.")
                : t("Configuración de red conservada.")
            }`
          : `${t("La conexión SSH usa ")}${value.interface || t("una ruta desconocida")}${t(
              "; revisa la IP de WireGuard del host.",
            )}`;
        setRoute((previous) => ({ ...previous, [host.id]: { text, error: false } }));
        return text;
      } catch (value) {
        const text =
          translateError(value, t, t("No se pudo comprobar la ruta."));
        setRoute((previous) => ({ ...previous, [host.id]: { text, error: true } }));
        throw value;
      }
    });

  async function prepareHost(host: HostSummaryDTO) {
    const value = await installMagpie(host.id, host.revision);
    setMagpie((previous) => ({ ...previous, [host.id]: { status: value } }));
    return value.ready
      ? `${t("Magpie está preparado en")} ${host.name || host.id}.`
      : t("Helper instalado; comprueba su estado antes de ejecutar agentes.");
  }

  const prepare = (host: HostSummaryDTO) =>
    run(host.id, () => prepareHost(host));

  const confirmIdentity = (host: HostSummaryDTO) =>
    run(host.id, async () => {
      const revision = host.revision ?? 0;
      const observed = host.observed_fingerprint ?? "";
      const trusted = await confirmTrust(host.id, revision, observed);
      await syncHost(trusted.id);
      await refresh();
      return `${t("Identidad SSH de")} ${trusted.name || host.name || host.id} ${t("confirmada")} (${observed}). ${await prepareHost(trusted)}`;
    });

  const inspect = (host: HostSummaryDTO) =>
    run(host.id, async () => {
      try {
        const value = await magpieStatus(host.id);
        setMagpie((previous) => ({ ...previous, [host.id]: { status: value } }));
      } catch (value) {
        const text =
          translateError(value, t, t("No se pudo consultar el estado de Magpie."));
        setMagpie((previous) => ({ ...previous, [host.id]: { error: text } }));
        throw value;
      }
      return t("Estado de Magpie actualizado.");
    });

  const sync = (host: HostSummaryDTO) =>
    run(host.id, async () => {
      await syncHost(host.id);
      await refresh();
      return t("Sincronización iniciada");
    });

  const setHistoryEnabled = (host: HostSummaryDTO, enabled: boolean) =>
    run(host.id, async () => {
      const value = await setHistoryConsent(host.id, enabled);
      setHistory((previous) => ({ ...previous, [host.id]: value }));
      return enabled
        ? t("Consentimiento para historial remoto concedido.")
        : t("Consentimiento revocado; las conversaciones aceptadas se conservan.");
    });

  const syncHistory = (host: HostSummaryDTO) =>
    run(host.id, async () => {
      const value = await syncHostHistory(host.id);
      setHistory((previous) => ({ ...previous, [host.id]: value }));
      return value.imported
        ? `${t("Historial sincronizado:")} ${value.imported} ${t("conversaciones nuevas o actualizadas.")}`
        : t("Historial sincronizado; no hay cambios nuevos.");
    });

  const setEnabled = (host: HostSummaryDTO, enabled: boolean) =>
    run(host.id, async () => {
      await saveHost({
        id: host.id,
        name: host.name ?? "",
        address: host.address ?? "",
        user: host.user ?? "",
        port: host.port ?? 22,
        auth: host.auth ?? "password",
        key_path: host.key_path,
        enabled,
      });
      await refresh();
      return enabled ? t("Equipo activado") : t("Equipo pausado");
    });

  const historyLine = (value: HistoryDTO) => {
    const states: Record<string, string> = {
      ready: t("autorizado"),
      host_paused: t("host pausado"),
      untrusted: t("identidad SSH sin confirmar"),
      identity_changed: t("identidad SSH cambiada: requiere nueva activación"),
      error: t("error de sincronización"),
      disabled: t("sin autorización"),
    };
    return `${t("Historial remoto")} · ${states[value.state ?? "disabled"] ?? t("sin autorización")} · ${value.files ?? 0} ${t("archivos importados")}${value.error ? ` · ${value.error}` : ""}`;
  };

  const magpieLine = (value: { status?: MagpieStatusDTO; error?: string }) => {
    if (value.error) return value.error;
    if (!value.status) return "";
    const version = value.status.version ? " · " + value.status.version : "";
    if (value.status.installed && value.status.ready)
      return t("Preparado y en ejecución") + version;
    if (value.status.installed) return t("Instalado, todavía no preparado") + version;
    return t("No instalado en este host");
  };

  return (
    <div className="space-y-5">
      <Card>
        <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3">
          <CardTitle role="heading" aria-level={3}>
            {t("Equipos SSH · Magpie e historial")}
          </CardTitle>
          <Button onClick={() => setForm({ host: null })}>
            <Plus />
            {t("Añadir equipo")}
          </Button>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="flex flex-wrap items-center gap-3 rounded-lg border p-3">
            <p className="min-w-0 flex-1 text-sm font-medium">{t("Este servidor")}</p>
            <div className="flex flex-wrap gap-2">
              {onNavigate && (
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => onNavigate("models", undefined, "local")}
                >
                  {t("Abrir panel")}
                </Button>
              )}
              {onNavigate && (
                <Button
                  variant="ghost"
                  size="sm"
                  // Clear a host carried from another section.
                  onClick={() => onNavigate("models", "usage", "local")}
                >
                  {t("Ver uso por equipo")}
                </Button>
              )}
            </div>
          </div>
          {hosts.map((host) => {
            // Connection actions need an enabled host whose SSH identity is not pending.
            const unavailable = !host.enabled || !!host.observed_fingerprint;
            const hostHistory = history[host.id];
            const hostRoute = route[host.id];
            const magpieText = magpieLine(magpie[host.id] ?? {});
            return (
              <div
                key={host.id}
                data-host={host.id}
                aria-busy={busy === host.id}
                className="space-y-3 rounded-lg border p-3"
              >
                <div className="flex flex-wrap items-start justify-between gap-3">
                  <div className="min-w-0">
                    <p className="text-sm font-medium break-words">
                      {host.name || host.id}
                    </p>
                    <p className="text-xs text-muted-foreground break-words">
                      {host.enabled
                        ? host.ssh_status || host.status || t("sin comprobar")
                        : ""}
                      {host.error ? `${host.enabled ? " · " : ""}${host.error}` : ""}
                    </p>
                  </div>
                  <div className="flex flex-wrap items-center gap-2">
                    {host.enabled ? (
                      <Badge variant="secondary">{t("activo")}</Badge>
                    ) : (
                      <Badge variant="outline">{t("pausado")}</Badge>
                    )}
                    {busy === host.id && (
                      <Loader2 className="size-4 animate-spin" aria-hidden="true" />
                    )}
                  </div>
                </div>
                <div className="flex flex-wrap gap-2">
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={!!busy || unavailable}
                    onClick={() => void sync(host)}
                  >
                    <RefreshCw />
                    {t("Sincronizar")}
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={!!busy}
                    onClick={() => void checkRoute(host)}
                  >
                    <Wifi />
                    {t("Comprobar VPN")}
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={!!busy || unavailable}
                    onClick={() => void prepare(host)}
                  >
                    {t("Instalar / iniciar")}
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={!!busy || !!host.observed_fingerprint}
                    onClick={() => void inspect(host)}
                  >
                    {t("Estado de Magpie")}
                  </Button>
                  {onNavigate && (
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={!!busy || unavailable}
                      onClick={() => onNavigate("models", undefined, host.id)}
                    >
                      {t("Abrir panel")}
                    </Button>
                  )}
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={!!busy}
                    onClick={() => setForm({ host })}
                  >
                    <Pencil />
                    {t("Editar")}
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={!!busy}
                    onClick={() => void setEnabled(host, !host.enabled)}
                  >
                    {host.enabled ? t("Pausar") : t("Activar")}
                  </Button>
                </div>
                {hostRoute?.text && (
                  <p
                    role={hostRoute.error ? "alert" : "status"}
                    className={`text-xs ${hostRoute.error ? "text-destructive" : "text-muted-foreground"}`}
                  >
                    {hostRoute.text}
                  </p>
                )}
                {hostHistory && (
                  <p
                    role={hostHistory.error ? "alert" : "status"}
                    className="text-xs text-muted-foreground break-words"
                  >
                    {historyLine(hostHistory)}
                  </p>
                )}
                {hostHistory && (
                  <div className="flex flex-wrap gap-2">
                    {!hostHistory.enabled && (
                      <Button
                        variant="outline"
                        size="sm"
                        disabled={!!busy || unavailable || !host.fingerprint}
                        onClick={() => void setHistoryEnabled(host, true)}
                      >
                        {t("Autorizar historial remoto")}
                      </Button>
                    )}
                    {hostHistory.enabled && (
                      <>
                        <Button
                          variant="outline"
                          size="sm"
                          disabled={!!busy || unavailable || !host.fingerprint}
                          onClick={() => void syncHistory(host)}
                        >
                          <RefreshCw />
                          {t("Sincronizar historial")}
                        </Button>
                        <Button
                          variant="outline"
                          size="sm"
                          disabled={!!busy}
                          onClick={() => void setHistoryEnabled(host, false)}
                        >
                          {t("Revocar consentimiento")}
                        </Button>
                      </>
                    )}
                  </div>
                )}
                {host.revision !== undefined && host.enabled && host.fingerprint && !host.observed_fingerprint && <Dependencies hostId={host.id} revision={host.revision} locale={locale} />}
                <details className="text-xs">
                  <summary className="cursor-pointer text-sm font-medium text-primary">
                    {t("Configuración y diagnóstico")}
                  </summary>
                  <div className="mt-3 space-y-2 text-muted-foreground">
                    <p className="break-words">
                      {t("Conexión")}: {host.address || t("sin dirección")}
                      {host.port ? `:${host.port}` : ""}
                      {host.user ? ` · ${host.user}` : ""}
                    </p>
                    <p>
                      {t("Autenticación")}:{" "}
                      {t(
                        host.auth === "password"
                          ? "contraseña guardada"
                          : host.auth === "agent"
                            ? "agente SSH"
                            : "clave privada",
                      )}
                      {host.home ? ` · ${host.home}` : ""}
                      {host.password_set ? ` · ${t("Contraseña cifrada")}` : ""}
                    </p>
                    <p className="break-words">
                      {t("Identidad SSH (pin)")}:{" "}
                      {host.fingerprint ? (
                        <code className="break-all">{host.fingerprint}</code>
                      ) : (
                        t("sin pin confirmado todavía")
                      )}
                    </p>
                    {host.observed_fingerprint && (
                      <div className="space-y-2" data-observed={host.observed_fingerprint}>
                        <p className="break-words">
                          {t("Clave observada")}:{" "}
                          <code className="break-all">{host.observed_fingerprint}</code>
                        </p>
                        <p className="text-destructive">
                          {t(
                            "La identidad de este equipo no está confirmada: la conexión queda bloqueada hasta que contrastes esta clave.",
                          )}
                        </p>
                        <Button
                          variant="outline"
                          size="sm"
                          disabled={!!busy || host.revision === undefined}
                          onClick={() => void confirmIdentity(host)}
                        >
                          <ShieldCheck />
                          {t("Confirmar e instalar Magpie")}
                        </Button>
                      </div>
                    )}
                    {magpieText && (
                      <p role="status" className="break-words">
                        {t("Magpie en el host")}: {magpieText}
                      </p>
                    )}
                  </div>
                </details>
              </div>
            );
          })}
          {(error || loadError) && (
            <p role="alert" className="text-sm text-destructive">
              {error || t(loadError)}
            </p>
          )}
          {notice && (
            <p role="status" className="text-sm text-muted-foreground">
              {notice}
            </p>
          )}
        </CardContent>
      </Card>
      {form && (
        <HostForm
          host={form.host}
          locale={locale}
          sshAgent={sshAgent}
          onClose={() => setForm(null)}
          onSaved={async () => {
            await refresh();
            setNotice(t("Equipo guardado"));
          }}
        />
      )}
    </div>
  );
}

function HostForm({
  host,
  locale,
  sshAgent,
  onClose,
  onSaved,
}: {
  host: HostSummaryDTO | null;
  locale: Language;
  sshAgent: boolean;
  onClose: () => void;
  onSaved: () => Promise<void>;
}) {
  const t = (value: unknown) => translate(value, locale);
  const [auth, setAuth] = useState(host?.auth || "password");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const password = String(data.get("password") ?? "");
    const input: HostInputDTO = {
      id: host?.id,
      name: String(data.get("name") ?? ""),
      address: String(data.get("address") ?? ""),
      user: String(data.get("user") ?? ""),
      port: Number(data.get("port")),
      auth,
      password: auth === "password" && password ? password : undefined,
      key_path: auth === "key" ? String(data.get("key_path") ?? "") : undefined,
      enabled: host?.enabled ?? true,
    };
    setBusy(true);
    setError("");
    try {
      await saveHost(input);
      await onSaved();
      onClose();
    } catch (value) {
      setError(translateError(value, t, t("No se pudo guardar.")));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogContent
        showCloseButton={false}
        className="max-h-[90dvh] overflow-auto sm:max-w-xl"
      >
        <DialogTitle>
          {t(host ? "Editar equipo SSH" : "Añadir equipo SSH")}
        </DialogTitle>
        <form className="grid gap-4 sm:grid-cols-2" onSubmit={submit}>
          <div className="space-y-1.5 sm:col-span-2">
            <Label htmlFor="host-name">{t("Alias del equipo (opcional)")}</Label>
            <Input
              id="host-name"
              name="name"
              defaultValue={host?.name ?? ""}
              maxLength={120}
            />
          </div>
          <div className="space-y-1.5 sm:col-span-2">
            <Label htmlFor="host-address">{t("IP o nombre de host")}</Label>
            <Input
              id="host-address"
              name="address"
              defaultValue={host?.address ?? ""}
              placeholder="10.0.0.50"
              maxLength={253}
              autoComplete="off"
              required
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="host-user">{t("Usuario SSH")}</Label>
            <Input
              id="host-user"
              name="user"
              defaultValue={host?.user ?? ""}
              maxLength={128}
              autoComplete="off"
              required
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="host-port">{t("Puerto")}</Label>
            <Input
              id="host-port"
              name="port"
              type="number"
              min="1"
              max="65535"
              defaultValue={host?.port ?? 22}
              required
            />
          </div>
          <div className="space-y-1.5 sm:col-span-2">
            <Label htmlFor="host-auth">{t("Autenticación")}</Label>
            <NativeSelect
              id="host-auth"
              name="auth"
              value={auth}
              onChange={(event) => setAuth(event.target.value)}
            >
              <NativeSelectOption value="password">
                {t("Contraseña")}
              </NativeSelectOption>
              <NativeSelectOption value="key">
                {t("Archivo de clave privada")}
              </NativeSelectOption>
              {sshAgent && (
                <NativeSelectOption value="agent">
                  {t("Agente SSH de esta computadora")}
                </NativeSelectOption>
              )}
            </NativeSelect>
          </div>
          {auth === "password" && (
            <div className="space-y-1.5 sm:col-span-2">
              <Label htmlFor="host-password">{t("Contraseña SSH")}</Label>
              <Input
                id="host-password"
                name="password"
                type="password"
                autoComplete="new-password"
                maxLength={4096}
                required={!host?.password_set}
                placeholder={t(
                  host?.password_set
                    ? "Déjala vacía para conservarla"
                    : "Contraseña del usuario remoto",
                )}
              />
            </div>
          )}
          {auth === "key" && (
            <div className="space-y-1.5 sm:col-span-2">
              <Label htmlFor="host-key">{t("Ruta a la clave privada en esta computadora")}</Label>
              <Input
                id="host-key"
                name="key_path"
                defaultValue={host?.key_path ?? ""}
                placeholder="~/.ssh/id_ed25519"
                required
              />
            </div>
          )}
          {error && (
            <p role="alert" className="text-sm text-destructive sm:col-span-2">
              {error}
            </p>
          )}
          <div className="flex gap-2 sm:col-span-2">
            <Button type="submit" disabled={busy}>
              {t(host ? "Guardar cambios" : "Guardar y conectar")}
            </Button>
            <Button type="button" variant="ghost" onClick={onClose} disabled={busy}>
              {t("Cancelar")}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
