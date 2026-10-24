/** Connections (UI-05/UI-06): name + address of other installations of this browser.
 *  Pure rules live here so the contract test can walk the limits without a browser:
 *  an address never carries credentials, a path, a query or a fragment, and the
 *  export file contains nothing but names and addresses. */

export interface ConnectionDTO {
  name: string;
  origin: string;
}

export const CONNECTIONS_KEY = "agent-workbench.connections.v1";
export const MAX_CONNECTIONS = 24;
export const MAX_CONNECTIONS_BYTES = 65536;
const MAX_ORIGIN = 2048;
const MAX_NAME = 64;
const control = /[\u0000-\u001f\u007f]/;
const invalidFile = "Selecciona un archivo exportado desde Conexiones.";

function isLoopbackHost(hostname: string) {
  return ["localhost", "127.0.0.1", "::1", "[::1]"].includes(hostname);
}

/** Accepts only the origin of an installation: HTTPS, or HTTP on loopback. */
export function connectionOrigin(value: string): string {
  let url: URL;
  try {
    url = new URL(value.trim());
  } catch {
    throw new Error(
      "Escribe una dirección completa, como https://workbench.example.com.",
    );
  }
  if (url.username || url.password || url.search || url.hash || url.pathname !== "/")
    throw new Error(
      "Usa sólo la dirección de la instalación, sin rutas, tokens ni credenciales.",
    );
  if (url.protocol !== "https:" && !(url.protocol === "http:" && isLoopbackHost(url.hostname)))
    throw new Error(
      "Las conexiones remotas necesitan HTTPS. HTTP sólo está permitido en localhost.",
    );
  return url.origin;
}

export function connectionName(value: string): string {
  const name = value.trim();
  if (!name || name.length > MAX_NAME || control.test(name))
    throw new Error("Escribe un nombre de hasta 64 caracteres.");
  return name;
}

/** Reads whatever the browser stored; a stale or edited record is dropped, never
 *  turned into a navigation target. A corrupt store is an error, not an empty list. */
export function readConnections(raw: string | null): ConnectionDTO[] {
  if (!raw) return [];
  if (raw.length > MAX_CONNECTIONS_BYTES)
    throw new Error("El archivo de conexiones es demasiado grande.");
  const values: unknown = JSON.parse(raw);
  if (!Array.isArray(values)) throw new Error("Formato de conexiones inválido.");
  const seen = new Set<string>();
  const result: ConnectionDTO[] = [];
  for (const value of values.slice(0, MAX_CONNECTIONS)) {
    if (!value || typeof value !== "object" || Array.isArray(value)) continue;
    if (!("name" in value) || !("origin" in value)) continue;
    if (typeof value.name !== "string" || typeof value.origin !== "string") continue;
    if (value.origin.length > MAX_ORIGIN) continue;
    try {
      const name = connectionName(value.name);
      const origin = connectionOrigin(value.origin);
      if (seen.has(origin)) continue;
      seen.add(origin);
      result.push({ name, origin });
    } catch {
      // A record the browser can no longer justify is dropped instead of kept.
    }
  }
  return result;
}

/** Deduplicates by origin and keeps the operator under the 24-connection limit. */
export function mergeConnection(
  connections: ConnectionDTO[],
  entry: ConnectionDTO,
): ConnectionDTO[] {
  const index = connections.findIndex((c) => c.origin === entry.origin);
  if (index === -1) {
    if (connections.length >= MAX_CONNECTIONS)
      throw new Error(
        "Puedes guardar hasta 24 conexiones. Quita una antes de añadir otra.",
      );
    return [...connections, entry];
  }
  return connections.map((c, i) => (i === index ? entry : c));
}

export function exportConnections(connections: ConnectionDTO[]) {
  return (
    JSON.stringify(
      {
        version: 1,
        connections: connections.map(({ name, origin }) => ({ name, origin })),
      },
      null,
      2,
    ) + "\n"
  );
}

/** Validates the whole file before touching the stored list: a bad entry leaves the
 *  current connections exactly as they were. */
export function importConnections(
  connections: ConnectionDTO[],
  text: string,
): ConnectionDTO[] {
  if (text.length > MAX_CONNECTIONS_BYTES)
    throw new Error("El archivo de conexiones debe tener menos de 64 KiB.");
  const value: unknown = JSON.parse(text);
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw new Error(invalidFile);
  if (Object.keys(value).some((key) => !["version", "connections"].includes(key)))
    throw new Error(invalidFile);
  if (
    !("version" in value) ||
    value.version !== 1 ||
    !("connections" in value) ||
    !Array.isArray(value.connections)
  )
    throw new Error(invalidFile);
  const records: unknown[] = value.connections;
  if (records.length > MAX_CONNECTIONS)
    throw new Error("El archivo contiene más de 24 conexiones.");
  let next = [...connections];
  for (const record of records) {
    if (!record || typeof record !== "object" || Array.isArray(record))
      throw new Error("Cada conexión debe contener sólo un nombre y una dirección.");
    if (Object.keys(record).some((key) => !["name", "origin"].includes(key)))
      throw new Error("Cada conexión debe contener sólo un nombre y una dirección.");
    if (
      !("name" in record) ||
      typeof record.name !== "string" ||
      !("origin" in record) ||
      typeof record.origin !== "string" ||
      record.origin.length > MAX_ORIGIN
    )
      throw new Error("Cada conexión debe contener sólo un nombre y una dirección.");
    next = mergeConnection(next, {
      name: connectionName(record.name),
      origin: connectionOrigin(record.origin),
    });
  }
  return next;
}
