import type { Language } from "./language";

/** Shell strings are authored in Spanish, the same way the ported modules do it.
 *  English lives in this explicit dictionary: an entry that is missing stays in
 *  Spanish (never blanks out), so it is visible in review and in the contract test
 *  that walks every section/view label. */
const english: Record<string, string> = {
  // Sections and groups (navigation.ts)
  Conexiones: "Connections",
  Ajustes: "Settings",
  Conversaciones: "Conversations",
  Tareas: "Tasks",
  Propuestas: "Proposals",
  "Equipos y agentes": "Hosts and agents",
  "Abrir configuración de agentes": "Open agent configuration",
  "Configuración de agentes": "Agent configuration",
  Cuentas: "Accounts",
  Catálogo: "Catalog",
  Rutas: "Routing",
  "Claves y LAN": "Keys and LAN",
  Uso: "Usage",
  Fuentes: "Sources",
  "Ejemplo guiado": "Guided example",
  Aprendizajes: "Learning",
  "Vista general": "Overview",
  Experimentos: "Experiments",
  Configuración: "Configuration",
  "Espacio de trabajo": "Workspace",
  Instalación: "Installation",
  // Shell chrome
  "Navegación principal": "Main navigation",
  "Avisos, licencias y fuente correspondiente": "Notices, licenses and corresponding source",
  "Secciones y preferencias de Workbench": "Workbench sections and preferences",
  "Ir a…": "Go to…",
  "Sección no encontrada": "Section not found",
  "Salta al contenido": "Skip to content",
  "Abrir navegación": "Open navigation",
  "Actualizar estado de la instalación": "Refresh installation status",
  "Usar tema claro": "Use light theme",
  "Usar tema oscuro": "Use dark theme",
  Idioma: "Language",
  "Conectando con esta instalación…": "Connecting to this installation…",
  "Reintentar": "Retry",
  "Cargando sección…": "Loading section…",
  "No se pudo abrir esta sección. Puedes usar el resto de la aplicación.":
    "This section could not open. You can still use the rest of the application.",
  "Token de acceso": "Access token",
  Entrar: "Sign in",
  "Conectando…": "Connecting…",
  Salir: "Sign out",
  "Cerrar": "Close",
  "Módulos sin montar": "Modules not mounted",
  // Command palette
  "Buscar sección o ajuste": "Search section or setting",
  "Buscar una sección, una vista o un ajuste…":
    "Search a section, a view or a setting…",
  "No hay coincidencias con ese nombre.": "Nothing matches that name.",
  "Ir a una sección o vista": "Go to a section or view",
  "Preferencias rápidas": "Quick preferences",
  "Cambiar de tema": "Switch theme",
  "Usar inglés": "Switch to English",
  "Usar español": "Switch to Spanish",
  // Connections (UI-05/06)
  "Tus instalaciones": "Your installations",
  "Instalación actual": "Current installation",
  Abrir: "Open",
  "Quitar conexión": "Remove connection",
  "Añadir conexión": "Add connection",
  Nombre: "Name",
  Dirección: "Address",
  "Guardar conexión": "Save connection",
  "Importando…": "Importing…",
  "Mi VPS": "My VPS",
  "Exportar direcciones": "Export addresses",
  "Importar direcciones": "Import addresses",
  "Importar archivo de conexiones": "Import connections file",
  "Conexión guardada. Ábrela para iniciar sesión.":
    "Connection saved. Open it to sign in.",
  "Nombre de conexión actualizado.": "Connection name updated.",
  "Conexión quitada de este navegador.":
    "Connection removed from this browser.",
  "Conexiones importadas. Inicia sesión por separado en cada instalación.":
    "Connections imported. Sign in separately on each installation.",
  "Conexiones exportadas. El archivo contiene sólo nombres y direcciones.":
    "Connections exported. The file contains only names and addresses.",
  "No se pudieron leer las conexiones guardadas en este navegador.":
    "The connections saved in this browser could not be read.",
  "El navegador no permitió guardar las conexiones. Revisa sus permisos de almacenamiento.":
    "The browser did not allow saving the connections. Check its storage permissions.",
  "Escribe una dirección completa, como https://workbench.example.com.":
    "Write a full address, such as https://workbench.example.com.",
  "Usa sólo la dirección de la instalación, sin rutas, tokens ni credenciales.":
    "Use only the installation address, without paths, tokens or credentials.",
  "Las conexiones remotas necesitan HTTPS. HTTP sólo está permitido en localhost.":
    "Remote connections need HTTPS. HTTP is only allowed on localhost.",
  "Escribe un nombre de hasta 64 caracteres.":
    "Write a name of up to 64 characters.",
  "Puedes guardar hasta 24 conexiones. Quita una antes de añadir otra.":
    "You can save up to 24 connections. Remove one before adding another.",
  "El archivo de conexiones debe tener menos de 64 KiB.":
    "The connections file must be smaller than 64 KiB.",
  "Selecciona un archivo exportado desde Conexiones.":
    "Select a file exported from Connections.",
  "El archivo contiene más de 24 conexiones.":
    "The file contains more than 24 connections.",
  "Cada conexión debe contener sólo un nombre y una dirección.":
    "Each connection must contain only a name and an address.",
  "El archivo no contiene un JSON válido.":
    "The file does not contain valid JSON.",
  "El archivo de conexiones es demasiado grande.":
    "The connections file is too large.",
  "Formato de conexiones inválido.": "Invalid connections format.",
  // Settings (M02)
  "No se pudo confirmar el cambio. Se muestra el último estado confirmado por la instalación.":
    "The change could not be confirmed. The last state confirmed by the installation is shown.",
  "Proyectos": "Projects",
  "Ruta de la carpeta": "Folder path",
  "Añadir": "Add",
  "Quitar": "Remove",
  "No se pudo conectar con el servicio. Reintenta en unos segundos.":
    "Could not reach the service. Try again in a few seconds.",
  "El servicio no devolvió una respuesta válida.":
    "The service did not return a valid response.",
  "No se pudieron consultar los hosts SSH.": "Could not query the SSH hosts.",
  "Proyecto añadido.": "Project added.",
  "Proyecto quitado.": "Project removed.",
  "Guardar": "Save",
  "Abrir Conexiones": "Open Connections",
  "Esta instalación no reportó ajustes en bootstrap. No se puede confirmar ni cambiar la configuración desde aquí.":
    "This installation did not report settings in bootstrap. The configuration cannot be confirmed or changed here.",
  // Settings — PL09 module diagnostics
  "Diagnóstico": "Diagnostics",
  "sin portar": "not ported",
  "Esta instalación no expone sonda ni registro del módulo gestionado: no se muestra un estado saludable.":
    "This installation exposes no probe or log for the managed module: no healthy state is shown.",
  "No se pudo leer el diagnóstico y no se muestra ningún estado anterior.":
    "The diagnostics could not be read and no previous state is shown.",
  "Consultando el módulo gestionado…": "Reading the managed module…",
  "Consultando…": "Reading…",
  Sondas: "Probes",
  "Fallos de sonda": "Probe failures",
  "Última lectura correcta": "Last successful read",
  "Registro del módulo gestionado": "Managed module log",
  Listo: "Ready",
  "Sin responder": "Not answering",
  Detenido: "Stopped",
  "Sin módulo gestionado": "No managed module",
  "proceso iniciado": "process started",
  listo: "ready",
  "sin respuesta en el arranque": "no answer at startup",
  "sonda correcta": "probe ok",
  "sonda fallida": "probe failed",
  "proceso terminado": "process exited",
};

/** Exact-text translation for shell strings: unknown text passes through unchanged. */
export function translateShell(value: unknown, language: Language) {
  const text = String(value ?? "");
  if (language === "es") return text;
  return english[text] ?? text;
}

/** Consumed by the contract test: sections and views must be translatable. */
export const shellDictionary = english;
