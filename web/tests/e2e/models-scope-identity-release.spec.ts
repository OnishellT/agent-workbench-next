import { expect, test, type FrameLocator, type Page } from "@playwright/test";
import { LOCAL_HOST } from "../../src/app/navigation";
import { translate } from "../../src/modules/models/i18n";
import { BASE, requireRelease, signIn, watchConsole, type ConsoleGuard } from "./helpers";

// UI-09: identidad visible de host/scope. Modelos enmarca el Magpie ORIGINAL del host
// elegido: al alternar servidor local y host SSH cada pantalla
// muestra el título y los datos nativos del seleccionado, los conserva al recargar y
// navegar, y un host desconocido o pausado se rechaza sin caer en la instalación local
// ni en otro host. Release real: E2E_BASE_URL + E2E_ACCESS_TOKEN, fixture SSH propia por
// E2E_SSH_*, sin mock, sin route.fulfill, sin skip; un requisito ausente falla.
//
//   E2E_BASE_URL=http://127.0.0.1:19062 E2E_ACCESS_TOKEN=... \
//   E2E_SSH_ADDRESS=127.0.0.1 E2E_SSH_USER=<user> E2E_SSH_KEY_PATH=<key> \
//   E2E_SSH_PORT=<port> E2E_SSH_FINGERPRINT=<ssh-keygen -lf host_key.pub> \
//     bun run test tests/e2e/models-scope-identity-release.spec.ts

const HOSTS_ROUTE = process.env.E2E_HOSTS_ROUTE ?? "/?section=hosts";
const MODELS_ROUTE = process.env.E2E_MODELS_ROUTE ?? "/?section=models";
/** Perfil guardado en la UI ORIGINAL del host: dato nativo de ese contexto. */
const HOST_PROFILE = "ui09 identidad";

let guard: ConsoleGuard | null = null;

test.beforeAll(requireRelease);

test.beforeEach(({ page }) => {
  guard = watchConsole(page);
});

test.afterEach(() => {
  expect(guard?.errors ?? [], "consola del navegador").toEqual([]);
});

interface ApiHost {
  id: string;
  name?: string;
  revision?: number;
  enabled?: boolean;
  fingerprint?: string;
  observed_fingerprint?: string;
}

/** Fixture SSH de esta ejecución: sin valores por defecto ni hosts del operador. */
function requiredSSHHost() {
  const address = process.env.E2E_SSH_ADDRESS;
  const user = process.env.E2E_SSH_USER;
  const keyPath = process.env.E2E_SSH_KEY_PATH;
  const password = process.env.E2E_SSH_PASSWORD;
  const fingerprint = process.env.E2E_SSH_FINGERPRINT;
  if (!address || !user || (!keyPath && !password) || !fingerprint)
    throw new Error(
      "E2E_SSH_ADDRESS, E2E_SSH_USER, E2E_SSH_FINGERPRINT and E2E_SSH_KEY_PATH (or E2E_SSH_PASSWORD) are not set: prepare a new reachable test host for this run (no default, no reuse of another host).",
    );
  return { address, user, port: process.env.E2E_SSH_PORT ?? "22", keyPath, password, fingerprint };
}

/** Origen del release bajo prueba: exigido, nunca un valor por defecto. */
function releaseOrigin() {
  if (!BASE) throw new Error("E2E_BASE_URL is not set: point it at the real release under test.");
  return new URL(BASE);
}

async function json<T>(page: Page, path: string): Promise<T> {
  const response = await page.request.get(path);
  expect(response.status(), `GET ${path}`).toBe(200);
  return (await response.json()) as T;
}

async function registryHosts(page: Page) {
  return (await json<{ hosts?: ApiHost[] }>(page, "/api/hosts")).hosts ?? [];
}

async function hostRecord(page: Page, id: string) {
  const host = (await registryHosts(page)).find((row) => row.id === id);
  expect(host, `host ${id} en el registro`).toBeTruthy();
  if (!host) throw new Error(`host ${id} missing from the registry`);
  return host;
}

/** Deep link de Modelos con el host que la sección lee de la URL. */
function modelsURL(view: string, host?: string) {
  const url = new URL(MODELS_ROUTE, "http://workbench.invalid");
  url.searchParams.set("view", view);
  if (host) url.searchParams.set("host", host);
  return url.pathname + url.search;
}

async function openModels(page: Page, view: string, host?: string) {
  await signIn(page, modelsURL(view, host));
  await expect(page.getByLabel("Host de los modelos")).toHaveValue(host ?? LOCAL_HOST);
}

/** El origen real que el navegador cargó para el panel nativo. */
async function panelOrigin(page: Page) {
  const frame = page
    .frames()
    .find((candidate) => candidate !== page.mainFrame() && candidate.url().includes("?view="));
  expect(frame, "el marco del panel está cargado").toBeTruthy();
  if (!frame) throw new Error("the native panel frame is missing");
  return new URL(frame.url()).origin;
}

/** Espera el panel nativo del host elegido y exige que comparta sitio con el shell:
 *  su cookie de portal es de primera parte dentro del marco. */
async function waitForPanel(page: Page, view: string): Promise<FrameLocator> {
  const frame = page.frameLocator('iframe[title^="Magpie"]');
  await expect(
    frame.locator("#nav button[data-view]").first(),
    "la UI original del host está en el marco",
  ).toBeVisible({ timeout: 60_000 });
  await expect(frame.locator(`#view-${view}`)).toHaveJSProperty("hidden", false);
  const origin = new URL(await panelOrigin(page));
  const shell = releaseOrigin();
  expect(
    origin.hostname === shell.hostname || origin.hostname.endsWith(`.${shell.hostname}`),
    `${origin.origin} no comparte sitio con ${shell.origin}`,
  ).toBe(true);
  return frame;
}

/** Perfil guardado por la UI ORIGINAL del contexto abierto: dato nativo de ese host. */
async function saveProfile(frame: FrameLocator, name: string) {
  await frame.locator("#save").click();
  const field = frame.locator("input.chip-input");
  await field.fill(name);
  await field.press("Enter");
  await expect(frame.locator("#profiles")).toContainText(name, { timeout: 20_000 });
}

/** Registra el host de la fixture desde Equipos, como lo haría el operador. */
async function registerHost(page: Page, alias: string) {
  const fixture = requiredSSHHost();
  await signIn(page, HOSTS_ROUTE);
  await expect(page.getByRole("heading", { level: 1, name: "Conexiones" })).toBeVisible();
  await page.getByRole("button", { name: "Añadir equipo", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("Alias del equipo (opcional)").fill(alias);
  await dialog.getByLabel("IP o nombre de host").fill(fixture.address);
  await dialog.getByLabel("Usuario SSH").fill(fixture.user);
  await dialog.getByLabel("Puerto").fill(fixture.port);
  if (fixture.keyPath) {
    await dialog.getByLabel("Autenticación").selectOption("key");
    await dialog.getByLabel("Ruta a la clave privada en esta computadora").fill(fixture.keyPath);
  } else {
    await dialog.getByLabel("Autenticación").selectOption("password");
    await dialog.getByLabel("Contraseña SSH").fill(fixture.password ?? "");
  }
  await dialog.getByRole("button", { name: "Guardar y conectar" }).click();
  await expect(dialog).toBeHidden();
  const host = (await registryHosts(page)).find((row) => row.name === alias);
  expect(host, `el registro de ${alias} tras guardarlo desde la UI`).toBeTruthy();
  if (!host) throw new Error(`host ${alias} was not registered`);
  return host;
}

/** Pausa o reactiva el host desde su fila en Equipos y confirma el registro. */
async function setEnabled(page: Page, id: string, button: "Pausar" | "Activar") {
  await signIn(page, HOSTS_ROUTE);
  const row = page.locator(`[data-host="${id}"]`);
  await row.getByRole("button", { name: button, exact: true }).click();
  await expect
    .poll(async () => (await hostRecord(page, id)).enabled, {
      timeout: 30_000,
      message: `host ${button === "Pausar" ? "pausado" : "activo"} en el registro`,
    })
    .toBe(button === "Activar");
}

/** Confirma la clave observada desde la ficha del host: sin confianza ciega. */
async function trustHost(page: Page, id: string) {
  await signIn(page, HOSTS_ROUTE);
  await expect
    .poll(async () => (await hostRecord(page, id)).observed_fingerprint ?? "", {
      timeout: 120_000,
      message: "clave SSH observada del fixture",
    })
    .toBe(requiredSSHHost().fingerprint);
  const revision = (await hostRecord(page, id)).revision ?? 0;
  const row = page.locator(`[data-host="${id}"]`);
  await row.getByText("Configuración y diagnóstico").click();
  await row.getByRole("button", { name: "Confirmar e instalar Magpie", exact: true }).click();
  await expect
    .poll(async () => (await hostRecord(page, id)).fingerprint ?? "", {
      timeout: 60_000,
      message: "pin SSH confiado",
    })
    .toBe(requiredSSHHost().fingerprint);
  const trusted = await hostRecord(page, id);
  expect(trusted.revision ?? 0, "la confirmación avanzó la revisión").toBeGreaterThan(revision);
  expect(trusted.observed_fingerprint ?? "", "pin pendiente tras confirmar").toBe("");
  await expect.poll(async () => (await json<{ ready?: boolean }>(
    page, `/api/hosts/${id}/magpie/status`,
  )).ready, { timeout: 180_000, message: "la confirmación instala y prepara Magpie" }).toBe(true);
}

/** Instala y arranca el Magpie del host por el flujo real de Equipos. */
async function installMagpie(page: Page, id: string) {
  const host = await hostRecord(page, id);
  const response = await page.request.post(`/api/hosts/${id}/magpie/install`, {
    data: { revision: host.revision },
  });
  expect([200, 201, 202], "instalación real del Magpie del host").toContain(response.status());
  await expect
    .poll(
      async () => {
        const status = await json<{ installed?: boolean; ready?: boolean }>(
          page,
          `/api/hosts/${id}/magpie/status`,
        );
        return Boolean(status.installed && status.ready);
      },
      { timeout: 120_000, message: "Magpie del host preparado sobre SSH confiado" },
    )
    .toBe(true);
}

test("un host desconocido no cae al servidor local ni a otro host", async ({ page }) => {
  test.setTimeout(180_000);
  const id = `ui09-unknown-${Date.now().toString(36)}`;
  // El host que no existe: su rechazo es la respuesta propia de la instalación.
  guard?.expected.push("/api/hosts/");
  await openModels(page, "agents", id);
  await expect(page.getByLabel("Host de los modelos").locator("option:checked")).toContainText(
    "Host no disponible",
  );
  const alert = page.getByRole("alert");
  await expect(alert).toContainText("No se pudo abrir Magpie");
  await expect(alert.getByRole("button", { name: "Reintentar" })).toBeVisible();
  await expect(alert.getByRole("button", { name: "Preparar Magpie y revisar SSH" })).toBeVisible();
  await expect(page.locator('iframe[title^="Magpie"]'), "ningún panel ajeno").toHaveCount(0);
  expect(await page.evaluate(() => location.search)).toContain(`host=${id}`);

});

test("un host pausado se rechaza y su reactivación recupera el panel de ese host", async ({
  page,
}) => {
  test.setTimeout(300_000);
  const alias = `UI09 pausado ${Date.now().toString(36)}`;
  const host = await registerHost(page, alias);
  // Pausado antes de pedir su panel: decide el estado de la instalación.
  await setEnabled(page, host.id, "Pausar");
  guard?.expected.push("/api/hosts/");
  await openModels(page, "agents", host.id);
  const alert = page.getByRole("alert");
  await expect(alert).toContainText("No se pudo abrir Magpie", { timeout: 60_000 });
  await expect(page.locator('iframe[title^="Magpie"]')).toHaveCount(0);
  // La pantalla sigue nombrando el host elegido: el rechazo es suyo, no del local.
  await expect(page.getByLabel("Host de los modelos")).toHaveValue(host.id);

  // Recuperación: activar, confiar en la clave y preparar su Magpie devuelve ESE panel.
  await setEnabled(page, host.id, "Activar");
  await trustHost(page, host.id);
  await installMagpie(page, host.id);
  await openModels(page, "agents", host.id);
  await waitForPanel(page, "agents");
  await expect(page.locator('iframe[title^="Magpie"]')).toHaveAttribute("title", `Magpie · ${alias}`);
  await expect(page.getByLabel("Host de los modelos")).toHaveValue(host.id);
  await expect(alert).toHaveCount(0);
});

test("cada host muestra sus datos nativos, los conserva al recargar y no los hereda al local", async ({
  page,
}) => {
  test.setTimeout(300_000);
  const alias = `UI09 identidad ${Date.now().toString(36)}`;
  const host = await registerHost(page, alias);
  await trustHost(page, host.id);
  await installMagpie(page, host.id);

  // Servidor local: su propio panel y su propio origen.
  await openModels(page, "agents", LOCAL_HOST);
  await waitForPanel(page, "agents");
  const localOrigin = await panelOrigin(page);
  await expect(page.locator('iframe[title^="Magpie"]')).toHaveAttribute("title", "Magpie · este servidor");

  // Host elegido: otro origen del mismo sitio, su UI original y sus datos.
  await openModels(page, "agents", host.id);
  const hostFrame = await waitForPanel(page, "agents");
  const hostOrigin = await panelOrigin(page);
  expect(hostOrigin, "cada contexto tiene su origen").not.toBe(localOrigin);
  await expect(page.locator('iframe[title^="Magpie"]')).toHaveAttribute("title", `Magpie · ${alias}`);
  await saveProfile(hostFrame, HOST_PROFILE);

  // Recargar conserva el host elegido y su dato.
  await page.reload();
  await expect(page.getByLabel("Host de los modelos")).toHaveValue(host.id);
  const reloaded = await waitForPanel(page, "agents");
  await expect(reloaded.locator("#profiles")).toContainText(HOST_PROFILE);
  expect(await panelOrigin(page)).toBe(hostOrigin);

  // Cambiar de pestaña conserva el host; el local no hereda nada.
  await page.goto(`/?section=models&view=usage&host=${host.id}`);
  await expect(page).toHaveURL(new RegExp(`host=${host.id}`));
  await expect(page).toHaveURL(/view=usage/);
  const usage = await waitForPanel(page, "usage");
  expect(await panelOrigin(page), "la pestaña sigue siendo del host elegido").toBe(hostOrigin);
  await expect(usage.locator("#profiles")).toContainText(HOST_PROFILE);
  await openModels(page, "agents", LOCAL_HOST);
  const localAgain = await waitForPanel(page, "agents");
  expect(await panelOrigin(page)).toBe(localOrigin);
  await expect(localAgain.locator("#profiles")).not.toContainText(HOST_PROFILE);
  // Elegir el servidor deja la URL sin host: el local es su ausencia.
  await page.getByLabel("Host de los modelos").selectOption(LOCAL_HOST);
  await expect(page).not.toHaveURL(/host=/);
  await waitForPanel(page, "agents");

  // El enlace del registro abre el panel de ESE host.
  await signIn(page, HOSTS_ROUTE);
  await page
    .locator(`[data-host="${host.id}"]`)
    .getByRole("button", { name: "Abrir panel", exact: true })
    .click();
  await expect(page).toHaveURL(new RegExp(`host=${host.id}`));
  await expect(page.getByLabel("Host de los modelos")).toHaveValue(host.id);
  // El enlace no fija pestaña: la sección abre la suya, con el host elegido.
  const fromRegistry = page.frameLocator('iframe[title^="Magpie"]');
  await expect(
    fromRegistry.locator("#nav button[data-view]").first(),
    "el panel del host elegido llegó por el enlace del registro",
  ).toBeVisible({ timeout: 60_000 });
  await expect(fromRegistry.locator("#profiles")).toContainText(HOST_PROFILE);
  expect(await panelOrigin(page)).toBe(hostOrigin);
});

test("las cadenas del alcance tienen inglés en sus dos módulos", () => {
  const missing = ["Host no disponible", "pausado", "Host de los modelos", "Este servidor"].filter(
    (text) => translate(text, "en") === text,
  );
  expect(missing, "cadenas del alcance sin inglés").toEqual([]);
});
