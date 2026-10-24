import { expect, test, type Page } from "@playwright/test";
import {
  requireRelease,
  signIn,
  watchConsole,
  type ConsoleGuard,
} from "./helpers";

// Retirada de Trabajo y Terminal: la shell ya no los monta y el servidor responde 404
// a /api/work*, /api/terminal* y /api/usage/overview. Este spec prueba esa retirada en
// real (sin mock, sin stub, sin skip) y conserva la cobertura de Equipos y del estado
// nativo del Magpie que no dependía de la UI retirada.
//
//   E2E_BASE_URL=https://workbench.example E2E_ACCESS_TOKEN=... \
//     bun run test tests/e2e/work-hosts-terminal-release.spec.ts

const HOSTS_ROUTE = process.env.E2E_HOSTS_ROUTE ?? "/?section=hosts";

let guard: ConsoleGuard | null = null;

test.beforeAll(requireRelease);

test.beforeEach(() => {
  guard = null;
});

test.afterEach(() => {
  expect(guard?.errors ?? [], "consola del navegador").toEqual([]);
});

/** A new SSH host of the test environment: no default, never an existing user host.
 *  Key auth is preferred when the coordinator prepared a private key path. */
function requiredSSHHost() {
  const address = process.env.E2E_SSH_ADDRESS;
  const user = process.env.E2E_SSH_USER;
  const keyPath = process.env.E2E_SSH_KEY_PATH;
  const password = process.env.E2E_SSH_PASSWORD;
  if (!address || !user || (!keyPath && !password))
    throw new Error(
      "E2E_SSH_ADDRESS, E2E_SSH_USER and E2E_SSH_KEY_PATH (or E2E_SSH_PASSWORD) are not set: prepare a new reachable test host for this run (no default, no reuse of another host).",
    );
  return {
    address,
    user,
    port: process.env.E2E_SSH_PORT ?? "22",
    keyPath,
    password,
  };
}

async function json<T>(page: Page, path: string): Promise<T> {
  const response = await page.request.get(path);
  expect(response.status(), `GET ${path}`).toBe(200);
  return (await response.json()) as T;
}

/** The root envelope of the host projection: `{hosts:[...]}`. */
async function registryHosts(page: Page) {
  const body = await json<{ hosts?: ApiHost[] }>(page, "/api/hosts");
  return body.hosts ?? [];
}

interface ApiHost {
  id: string;
  name?: string;
  address?: string;
  enabled?: boolean;
  revision?: number;
  password_set?: boolean;
  fingerprint?: string;
  observed_fingerprint?: string;
  error?: string;
  password?: string;
}

async function openHosts(page: Page) {
  guard = watchConsole(page);
  // The history receipt is still blocked by the Foundry normalizer and this release
  // answers 501: the screen reads it as soon as it mounts, so it is declared expected
  // before the section loads instead of racing the console message.
  guard.expected.push("/history");
  await signIn(page);
  await page.goto(HOSTS_ROUTE);
  await expect(
    page.getByRole("heading", { name: "Equipos SSH · Magpie e historial" }),
  ).toBeVisible();
}

/** Registers the test host through the UI and returns its real registry record. */
async function registerHost(page: Page, alias: string) {
  const fixture = requiredSSHHost();
  await page.getByRole("button", { name: "Añadir equipo", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("Alias del equipo (opcional)").fill(alias);
  await dialog.getByLabel("IP o nombre de host").fill(fixture.address);
  await dialog.getByLabel("Usuario SSH").fill(fixture.user);
  await dialog.getByLabel("Puerto").fill(fixture.port);
  if (fixture.keyPath) {
    await dialog.getByLabel("Autenticación").selectOption("key");
    await dialog
      .getByLabel("Ruta a la clave privada en esta computadora")
      .fill(fixture.keyPath);
  } else {
    await dialog.getByLabel("Autenticación").selectOption("password");
    await dialog.getByLabel("Contraseña SSH").fill(fixture.password ?? "");
  }
  await dialog.getByRole("button", { name: "Guardar y conectar" }).click();
  await expect(dialog).toBeHidden();
  const hosts = await registryHosts(page);
  const host = hosts.find((row) => row.name === alias);
  expect(host, `el registro de ${alias} tras guardarlo desde la UI`).toBeTruthy();
  if (!host) throw new Error("unreachable");
  // The registry never publishes the secret it stores sealed.
  expect(host.password, "contraseña publicada por la API").toBeUndefined();
  await expect(page.locator(`[data-host="${host.id}"]`)).toBeVisible();
  return host;
}

/** A retired API path answers 404 with a plain API body, never the shell HTML. */
async function expectRetired(page: Page, method: string, path: string, data?: unknown) {
  const response = await page.request.fetch(path, { method, data });
  expect(response.status(), `${method} ${path}`).toBe(404);
  expect(await response.text(), `${method} ${path} no devuelve HTML`).not.toContain("<html");
}

test("la API retirada de Trabajo responde 404 en raíz y por host", async ({ page }) => {
  guard = watchConsole(page);
  await signIn(page);
  for (const path of ["/api/work/runtime", "/api/work/sessions", "/api/work/sessions/abc"]) {
    await expectRetired(page, "GET", path);
  }
  await expectRetired(page, "POST", "/api/work/sessions", {});
  await expectRetired(page, "DELETE", "/api/work/sessions/abc");
  // El proxy por host tampoco sirve Trabajo: el host existe en el registro o no,
  // la ruta retirada responde 404 en ambos casos.
  await expectRetired(page, "GET", "/api/hosts/local/work/runtime");
  await expectRetired(page, "GET", "/api/hosts/desconocido/work/runtime");
});

test("la API retirada de Terminal responde 404 en raíz y por host", async ({ page }) => {
  guard = watchConsole(page);
  await signIn(page);
  for (const path of ["/api/terminal", "/api/terminal/abc", "/api/terminal/abc/ws"]) {
    await expectRetired(page, "GET", path);
  }
  await expectRetired(page, "POST", "/api/terminal", { engine: "omp" });
  await expectRetired(page, "DELETE", "/api/terminal/abc");
  await expectRetired(page, "GET", "/api/hosts/local/terminal");
  await expectRetired(page, "GET", "/api/hosts/desconocido/terminal");
});

test("siete secciones sin Trabajo ni Terminal; ?section=work cae en Conversaciones", async ({
  page,
}) => {
  guard = watchConsole(page);
  await signIn(page);
  const nav = page.getByRole("navigation", { name: "Navegación principal" });
  await expect(nav.getByRole("button", { name: "Conversaciones", exact: true })).toBeVisible();
  expect(await nav.getByRole("button").allTextContents()).toEqual([
    "Conversaciones",
    "Tareas",
    "Aprendizajes",
    "SIM",
    "Configuración de agentes",
    "Conexiones",
    "Ajustes",
  ]);
  await expect(nav.getByRole("button", { name: "Trabajo", exact: true })).toHaveCount(0);
  await expect(nav.getByRole("button", { name: "Terminal", exact: true })).toHaveCount(0);

  for (const retired of ["work", "terminal"]) {
    await page.goto(`/?section=${retired}`);
    // El marcador retirado resuelve en la sección que hoy guarda sus datos; la URL
    // queda como vino, sin reescritura.
    await expect(
      page.getByRole("navigation", { name: /Secciones de Conversaciones/ }),
    ).toBeVisible();
    expect(new URL(page.url()).searchParams.get("section")).toBe(retired);
    await expect(
      page.getByRole("tab", { name: "Chat", exact: true }),
    ).toHaveCount(0);
    await expect(
      page.getByRole("region", { name: "Terminal interactiva" }),
    ).toHaveCount(0);
    await page.reload();
    await expect(
      page.getByRole("navigation", { name: /Secciones de Conversaciones/ }),
    ).toBeVisible();
  }
});

test("Equipos: registra un host nuevo, lo pausa y lo conserva tras recargar", async ({
  page,
}) => {
  await openHosts(page);
  const alias = `M03 ${Date.now().toString(36)}`;
  const host = await registerHost(page, alias);
  const row = page.locator(`[data-host="${host.id}"]`);
  await expect(row.getByText("activo", { exact: true })).toBeVisible();
  await row.getByRole("button", { name: "Pausar", exact: true }).click();
  await expect(row.getByText("pausado", { exact: true }).first()).toBeVisible();
  await page.reload();
  const paused = page.locator(`[data-host="${host.id}"]`);
  await expect(paused.getByText("pausado", { exact: true }).first()).toBeVisible();
  // Durable registry state, read from the API and not from the screen.
  expect(
    (await registryHosts(page)).find(
      (row) => row.id === host.id,
    )?.enabled,
  ).toBe(false);
  await paused.getByRole("button", { name: "Activar", exact: true }).click();
  await expect(paused.getByText("activo", { exact: true })).toBeVisible();
  expect(
    (await registryHosts(page)).find(
      (row) => row.id === host.id,
    )?.enabled,
  ).toBe(true);
});

test("Equipos: pin SSH, diagnóstico de ruta y consentimiento sin falso éxito", async ({
  page,
}) => {
  test.setTimeout(180_000);
  await openHosts(page);
  const alias = `M03d ${Date.now().toString(36)}`;
  const host = await registerHost(page, alias);
  const row = page.locator(`[data-host="${host.id}"]`);
  // Saving an enabled host already starts its connection, so the key may be observed by the
  // time the row renders and Sincronizar is blocked on purpose. Either way the reading is the
  // same: the connection observes the key and stays blocked until the operator confirms it.
  const sync = row.getByRole("button", { name: "Sincronizar", exact: true });
  if (await sync.isEnabled()) await sync.click();
  await expect
    .poll(
      async () =>
        (await registryHosts(page)).find(
          (row) => row.id === host.id,
        )?.observed_fingerprint ?? "",
      { timeout: 120_000, message: "clave SSH observada y conexión bloqueada" },
    )
    .toMatch(/^SHA256:/);
  const observed = (await registryHosts(page)).find(
    (row) => row.id === host.id,
  );
  expect(observed?.fingerprint ?? "", "pin confiado sin confirmación").toBe("");
  expect(observed?.observed_fingerprint, "contraste independiente con la clave del sshd de ensayo")
    .toBe(process.env.E2E_SSH_FINGERPRINT);
  // Reload so the screen holds the revision the server will accept.
  await page.reload();
  const observedRow = page.locator(`[data-host="${host.id}"]`);
  await observedRow.getByText("Configuración y diagnóstico").click();
  await expect(observedRow.getByText("Clave observada")).toBeVisible();
  await expect(observedRow.locator("code")).toHaveText(
    observed?.observed_fingerprint ?? "",
  );
  await observedRow
    .getByRole("button", { name: "Confirmar e instalar Magpie", exact: true })
    .click();
  await expect
    .poll(
      async () =>
        (await registryHosts(page)).find(
          (row) => row.id === host.id,
        )?.fingerprint ?? "",
      { timeout: 60_000, message: "pin SSH confiado tras confirmar la clave" },
    )
    .toBe(observed?.observed_fingerprint ?? "");
  const trusted = (await registryHosts(page)).find(
    (row) => row.id === host.id,
  );
  expect(trusted?.observed_fingerprint ?? "").toBe("");
  // The revision advanced with the confirmation: the receipt is not a screen-only claim.
  expect(trusted?.revision ?? 0).toBeGreaterThan(observed?.revision ?? 0);
  await expect(observedRow.getByText("Preparado y en ejecución", { exact: true }))
    .toBeVisible({ timeout: 180_000 });
  // With the identity confirmed, the same control performs a real read on the host.
  await expect
    .poll(
      async () =>
        (await registryHosts(page)).find(
          (row) => row.id === host.id,
        )?.error ?? "",
      { timeout: 120_000, message: "lectura real tras confirmar la identidad" },
    )
    .not.toMatch(/huella/);
  // WireGuard diagnostics are read-only and never invented by the screen. A rejected
  // diagnostic is the screen's own alert, so its console entry is expected too.
  guard?.expected.push("/api/network/wireguard");
  const route = await page.request.get(
    `/api/network/wireguard?host_id=${encodeURIComponent(host.id)}`,
  );
  await row.getByRole("button", { name: "Comprobar VPN", exact: true }).click();
  if (route.status() === 200) {
    const body = (await route.json()) as { interface?: string; wireguard?: boolean };
    if (body.wireguard)
      await expect(row.getByText(`Ruta por ${body.interface}`)).toBeVisible();
    else await expect(row.getByText(/revisa la IP de WireGuard/)).toBeVisible();
  } else {
    expect(route.status(), "diagnóstico de ruta").toBe(400);
    await expect(row.getByRole("alert")).toBeVisible();
    await expect(row.getByText(/^Ruta por /)).toHaveCount(0);
  }
  const installResponse = page.waitForResponse((response) =>
    response.url().endsWith(`/api/hosts/${host.id}/magpie/install`) && response.request().method() === "POST",
  );
  await row.getByRole("button", { name: "Instalar / iniciar", exact: true }).click();
  const installed = await installResponse;
  expect(installed.request().postDataJSON()).toEqual({ revision: trusted?.revision });
  expect([200, 201, 202], "instalación real o idempotente del helper Go").toContain(installed.status());
});

test("el estado nativo del Magpie local publica sus agentes, sin UI de Trabajo", async ({
  page,
}) => {
  guard = watchConsole(page);
  await signIn(page);
  // La cobertura del estado nativo que tenían las pantallas retiradas, leída directo
  // de la API del Magpie de este servidor: agentes publicados, sin catálogo propio.
  const state = await json<{ agents?: Array<{ id: string; fields?: Array<{ key: string }> }> }>(
    page,
    "/api/models/state",
  );
  expect(Array.isArray(state.agents), "el Magpie publica su lista de agentes").toBe(true);
  expect(state.agents?.length ?? 0, "al menos un agente nativo").toBeGreaterThan(0);
  expect(
    state.agents?.find((agent) => agent.id === "codex"),
    "el Magpie del host publica el agente Codex",
  ).toBeTruthy();
  const groups = await json<{ groups?: unknown }>(page, "/api/models/groups");
  expect(Array.isArray(groups.groups), "los grupos nativos llegan como lista").toBe(true);
});
