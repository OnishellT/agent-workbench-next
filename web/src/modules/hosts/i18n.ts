import type { Language } from "@/app/language";

export type { Language };

/** Module strings are authored in Spanish, the way the ported modules do it. English lives
 *  here; an entry that is missing stays Spanish and is visible in review. */
const english: Record<string, string> = {
  "Equipos SSH · Magpie e historial": "SSH hosts · Magpie and history",
  "Añadir equipo": "Add host",
  "Este servidor": "This server",
  "Abrir panel": "Open panel",
  "Magpie en el host": "Magpie on the host",
  pausado: "paused",
  activo: "active",
  "sin comprobar": "not checked",
  Sincronizar: "Sync",
  "Comprobar VPN": "Check VPN",
  "Instalar / iniciar": "Install / start",
  "Estado de Magpie": "Magpie status",
  Editar: "Edit",
  Pausar: "Pause",
  Activar: "Activate",
  "Equipo activado": "Host activated",
  "Equipo pausado": "Host paused",
  "Equipo guardado": "Host saved",
  "Sincronización iniciada": "Sync started",
  "Estado de Magpie actualizado.": "Magpie status refreshed.",
  "No se pudo consultar el historial.": "The history could not be read.",
  "No se pudo comprobar la ruta.": "The route check failed.",
  "No se pudo consultar el estado de Magpie.":
    "Magpie status could not be read.",
  "No se pudo guardar.": "It could not be saved.",
  "La operación falló.": "The operation failed.",
  "Magpie está preparado en": "Magpie is ready on",
  "Ruta por ": "Route through ",
  "La ruta predeterminada principal también usa WireGuard.":
    "The main default route also uses WireGuard.",
  "Configuración de red conservada.": "Network configuration left unchanged.",
  "La conexión SSH usa ": "The SSH connection uses ",
  "; revisa la IP de WireGuard del host.":
    "; check the host's WireGuard address.",
  "una ruta desconocida": "an unknown route",
  "una interfaz": "an interface",
  "confirmada": "confirmed",
  "archivos importados": "files imported",
  "Historial sincronizado:": "History synchronized:",
  "conversaciones nuevas o actualizadas.": "new or updated conversations.",
  "Historial sincronizado; no hay cambios nuevos.":
    "History synchronized; there are no new changes.",
  "Consentimiento revocado; las conversaciones aceptadas se conservan.":
    "Consent revoked; the accepted conversations are kept.",
  "Consentimiento para historial remoto concedido.":
    "Consent for remote history granted.",
  "Autorizar historial remoto": "Authorize remote history",
  "Revocar consentimiento": "Revoke consent",
  "sin dato": "no data",
  "Historial remoto": "Remote history",
  "Ver uso por equipo": "Usage per host",
  "identidad SSH sin confirmar": "unconfirmed SSH identity",
  autorizado: "authorized",
  "host pausado": "host paused",
  "identidad SSH cambiada: requiere nueva activación":
    "SSH identity changed: it needs a new authorization",
  "error de sincronización": "sync error",
  "sin autorización": "not authorized",
  "No instalado en este host": "Not installed on this host",
  "Preparado y en ejecución": "Ready and running",
  "Instalado, todavía no preparado": "Installed, not ready yet",
  "Helper instalado; comprueba su estado antes de ejecutar agentes.":
    "Helper installed; check its status before running agents.",
  "Configuración y diagnóstico": "Configuration and diagnostics",
  Conexión: "Connection",
  "sin dirección": "no address",
  Autenticación: "Authentication",
  "contraseña guardada": "saved password",
  "agente SSH": "SSH agent",
  "clave privada": "private key",
  "Contraseña cifrada": "Encrypted password",
  "Identidad SSH (pin)": "SSH identity (pin)",
  "sin pin confirmado todavía": "no confirmed pin yet",
  "Clave observada": "Observed key",
  "La identidad de este equipo no está confirmada: la conexión queda bloqueada hasta que contrastes esta clave.":
    "This host's identity is not confirmed: the connection stays blocked until you verify this key.",
  "Confirmar e instalar Magpie": "Confirm and install Magpie",
  "La identidad del host cambió; revisa su conexión antes de continuar.":
    "The host identity changed; check its connection before continuing.",
  "Identidad SSH de": "SSH identity of",
  "la huella SSH cambió; conexión bloqueada hasta confirmación explícita":
    "the SSH fingerprint changed; the connection is blocked until explicit confirmation",
  "huella SSH desconocida; confirma la identidad antes de operar":
    "unknown SSH fingerprint; confirm the identity before operating",
  "la huella no coincide con la clave observada; vuelve a comprobar el host":
    "the fingerprint does not match the observed key; check the host again",
  "huella SSH inválida": "invalid SSH fingerprint",
  "Magpie del host": "Magpie on the host",
  "Editar equipo SSH": "Edit SSH host",
  "Añadir equipo SSH": "Add SSH host",
  "Alias del equipo (opcional)": "Host alias (optional)",
  "IP o nombre de host": "IP or host name",
  "Usuario SSH": "SSH user",
  Puerto: "Port",
  Contraseña: "Password",
  "Archivo de clave privada": "Private key file",
  "Agente SSH de esta computadora": "SSH agent of this machine",
  "Contraseña SSH": "SSH password",
  "Déjala vacía para conservarla": "Leave it empty to keep it",
  "Contraseña del usuario remoto": "Password of the remote user",
  "Ruta a la clave privada en esta computadora":
    "Path to the private key on this machine",
  "Guardar cambios": "Save changes",
  "Guardar y conectar": "Save and connect",
  Cancelar: "Cancel",
};

const escape = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
const pattern = new RegExp(
  "(?<![\\p{L}\\p{N}_-])(?:" +
    Object.keys(english)
      .sort((a, b) => b.length - a.length)
      .map(escape)
      .join("|") +
    ")(?![\\p{L}\\p{N}_-])",
  "gu",
);

export function translate(value: unknown, language: Language) {
  const text = String(value ?? "");
  return language === "es"
    ? text
    : text.replace(pattern, (key) => english[key]);
}
