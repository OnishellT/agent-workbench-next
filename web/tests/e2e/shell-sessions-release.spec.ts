import { expect, test, type Browser, type BrowserContext, type Page } from "@playwright/test";
import { watchConsole, type ConsoleGuard } from "./helpers";

// M02 PL-01/PL-02/PL-03: primer inicio vacío, canje de sesión y aislamiento por
// instancia, contra DOS instalaciones reales y vacías (ninguna con datos heredados).
// El marcador de señuelo es opcional: si se pasa E2E_CANARY_MARKER, ese texto vive en
// archivos plantados FUERA del estado de A y no puede aparecer en ninguna respuesta ni
// en la interfaz. Nada de aquí es mock: cada efecto lo confirma la instalación.
//
//   E2E_BASE_URL=http://127.0.0.1:19012 E2E_ACCESS_TOKEN=... \
//   E2E_SECOND_BASE_URL=http://127.0.0.1:19014 E2E_SECOND_ACCESS_TOKEN=... \
//   E2E_PROJECT_DIR=/.../a/projects/marker-a E2E_SECOND_PROJECT_DIR=/.../b/projects/marker-b \
//   E2E_CANARY_MARKER=CANARY-PL01 \
//     bun run test tests/e2e/shell-sessions-release.spec.ts

const A = process.env.E2E_BASE_URL;
const B = process.env.E2E_SECOND_BASE_URL;
const A_TOKEN = process.env.E2E_ACCESS_TOKEN;
const B_TOKEN = process.env.E2E_SECOND_ACCESS_TOKEN;
const CANARY = process.env.E2E_CANARY_MARKER ?? "";

test.beforeAll(() => {
  if (!A || !B || !A_TOKEN || !B_TOKEN)
    throw new Error(
      "Two real installations and their own access tokens must be configured explicitly: E2E_BASE_URL/E2E_ACCESS_TOKEN and E2E_SECOND_BASE_URL/E2E_SECOND_ACCESS_TOKEN.",
    );
  if (new URL(A).origin === new URL(B).origin)
    throw new Error("The second installation must have a different origin.");
});

let guard: ConsoleGuard | null = null;
test.beforeEach(({ page }) => {
  guard = watchConsole(page);
});
test.afterEach(() => {
  expect(guard?.errors ?? [], "consola del navegador").toEqual([]);
  guard = null;
});

interface Bootstrap {
  settings?: { projects?: { path: string }[] };
  status?: string;
}
interface Hosts {
  hosts?: unknown[];
}
interface Sources {
  sources?: unknown[];
}
interface NativeState {
  agents?: { id?: string; wired?: boolean }[];
}
interface Runtime {
  projects?: { path: string }[];
}

/** Una lectura del API real: el límite donde el cuerpo JSON pasa a una forma con nombre. */
async function readJSON<T>(source: Page | BrowserContext, url: string): Promise<T> {
  const response = await source.request.get(url);
  expect(response.status(), url).toBe(200);
  return (await response.json()) as T;
}

/** Deja una sesión real en esa instalación: la shell puede pintarse mientras bootstrap
 *  carga, así que la verdad la da el propio API, no el primer control visible. */
async function unlock(page: Page, origin: string, token: string) {
  await page.goto(origin);
  const shell = page.getByRole("navigation", { name: "Navegación principal" });
  const bootstrapURL = new URL("/api/bootstrap", origin).href;
  for (let attempt = 0; attempt < 20; attempt++) {
    const granted = await page.request
      .get(bootstrapURL)
      .then((response) => response.status() === 200)
      .catch(() => false);
    if (granted) {
      await expect(shell).toBeVisible();
      return;
    }
    const form = page.locator("#access-token");
    if (await form.isVisible().catch(() => false)) {
      await form.fill(token);
      await page.getByRole("button", { name: "Entrar", exact: true }).click();
    }
    await page.waitForTimeout(500);
  }
  throw new Error("la sesión no quedó establecida en esta instalación");
}

function expectNoCanary(text: string, where: string) {
  if (!CANARY) return;
  expect(text, `${where} publicó el señuelo`).not.toContain(CANARY);
}

test("PL-01: un primer inicio vacío no hereda ni lee nada de fuera de su estado", async ({
  browser,
}) => {
  const context = await browser.newContext();
  const page = await context.newPage();
  await page.goto(A!);
  await expect(page.locator("#access-token"), "el primer inicio pide sesión").toBeVisible();
  await unlock(page, A!, A_TOKEN!);
  await expect(page.getByRole("heading", { level: 1, name: "Conversaciones" })).toBeVisible();

  // Lo que la instalación reporta de sí misma: nada heredado.
  const bootstrap = await readJSON<Bootstrap>(page, "/api/bootstrap");
  expect(bootstrap.settings?.projects ?? [], "proyectos heredados").toEqual([]);
  const hosts = await readJSON<Hosts>(page, "/api/hosts");
  expect(hosts.hosts ?? [], "hosts remotos heredados").toEqual([]);
  const sources = await readJSON<Sources>(page, "/api/conversations/sources");
  expect(sources.sources ?? [], "fuentes heredadas").toEqual([]);
  const native = await readJSON<NativeState>(page, "/api/models/state");
  expect(
    (native.agents ?? []).filter((agent) => agent.wired),
    "cuentas nativas conectadas en un primer inicio",
  ).toEqual([]);
  expectNoCanary(JSON.stringify(bootstrap), "bootstrap");
  expectNoCanary(JSON.stringify(hosts), "hosts");
  expectNoCanary(JSON.stringify(sources), "sources");
  expectNoCanary(await page.locator("body").innerText(), "la interfaz");
  await context.close();
});

test("PL-02: el canje de sesión deja cookie HttpOnly y una URL sin secreto", async ({ browser }) => {
  test.setTimeout(120_000);
  // La cookie la emite la instalación real: sus banderas se leen de su respuesta.
  const probe = await browser.newContext({ baseURL: A });
  const login = await probe.request.post("/api/auth", { data: { token: A_TOKEN } });
  expect(login.status()).toBe(200);
  const setCookie = login.headers()["set-cookie"] ?? "";
  expect(setCookie, "cookie HttpOnly").toContain("HttpOnly");
  expect(setCookie, "cookie SameSite=Strict").toContain("SameSite=Strict");
  expect(setCookie, "sin Secure en HTTP local (PL-04 es el gate de TLS)").not.toContain("Secure");
  await probe.close();

  // Enlace privado con fragmento: el token se canjea y desaparece de la URL.
  const context = await browser.newContext();
  const page = await context.newPage();
  await page.goto(`${A}/#token=${A_TOKEN}`);
  await expect(page.getByRole("navigation", { name: "Navegación principal" })).toBeVisible();
  expect(page.url(), "el token no queda en la URL").not.toContain("token");
  expect(page.url()).not.toContain("#");
  await readJSON<Bootstrap>(page, "/api/bootstrap");
  // HttpOnly visto desde la propia página: el script no puede leer la cookie.
  expect(await page.evaluate(() => document.cookie), "la cookie no es legible por script").not.toContain(
    "workbench_session",
  );
  await page.reload();
  await expect(page.getByRole("heading", { level: 1, name: "Conversaciones" })).toBeVisible();
  expect(page.url()).not.toContain("token");

  // Token inválido y acceso anónimo fallan sin conceder autoridad.
  const denied = await context.request.post(new URL("/api/auth", A!).href, {
    data: { token: "no-es-un-token-valido-1234567890" },
  });
  expect(denied.status(), "token inválido").toBe(401);
  expect(denied.headers()["set-cookie"] ?? "", "un token inválido no crea cookie").not.toContain(
    "workbench_session",
  );
  const anonymous = await browser.newContext({ baseURL: A });
  expect((await anonymous.request.get("/api/bootstrap")).status(), "anónimo").toBe(401);
  await anonymous.close();

  // El documento y su propio recurso embebido tampoco publican el token.
  const shellDocument = await page.request.get("/");
  expect(shellDocument.status()).toBe(200);
  const html = await shellDocument.text();
  expect(html, "el documento publicó el token").not.toContain(A_TOKEN!);
  const script = await page.evaluate(
    () => document.querySelector('script[type="module"][src]')?.getAttribute("src") ?? "",
  );
  expect(script, "el shell publica su script embebido").not.toBe("");
  const resource = await page.request.get(new URL(script, A!).href);
  expect(resource.status(), script).toBe(200);
  expect(await resource.text(), `${script} publicó el token`).not.toContain(A_TOKEN!);
  await context.close();
});

/** Quita los marcadores que este caso escribe, en las dos instalaciones, para que la
 *  fixture siga siendo reutilizable aunque el caso falle a mitad. */
async function restoreMarkers(browser: Browser, markerA: string, markerB: string) {
  for (const [origin, token, marker] of [
    [A!, A_TOKEN!, markerA],
    [B!, B_TOKEN!, markerB],
  ] as const) {
    const context = await browser.newContext({ baseURL: origin });
    const login = await context.request.post(new URL("/api/auth", origin).href, { data: { token } });
    expect(login.status(), `sesión de ${origin}`).toBe(200);
    const current = await readJSON<Bootstrap>(context, new URL("/api/bootstrap", origin).href);
    const found = current.settings?.projects ?? [];
    const kept = found.filter((row) => row.path !== marker);
    if (kept.length !== found.length) {
      const receipt = await context.request.post(new URL("/api/settings", origin).href, {
        data: { projects: kept },
      });
      expect([200, 201], `restaurar los proyectos de ${origin}`).toContain(receipt.status());
    }
    await context.close();
  }
}

test("PL-03: dos instalaciones conservan su autoridad, sus datos y su logout", async ({
  browser,
}) => {
  test.setTimeout(180_000);
  const markerA = process.env.E2E_PROJECT_DIR;
  const markerB = process.env.E2E_SECOND_PROJECT_DIR;
  if (!markerA || !markerB)
    throw new Error(
      "This case writes one distinct project per installation: set E2E_PROJECT_DIR and E2E_SECOND_PROJECT_DIR (folders that exist on their servers). No default, no fallback.",
    );

  const contextA = await browser.newContext();
  const contextB = await browser.newContext();
  try {
    const pageA = await contextA.newPage();
    await unlock(pageA, A!, A_TOKEN!);
    // Marcador propio de A: preferencias por la UI y un proyecto de su propio estado.
    await pageA.getByRole("button", { name: "Usar tema oscuro" }).click();
    await pageA.getByLabel("Idioma").selectOption("en");
    await expect(pageA.getByRole("heading", { level: 1, name: "Work" })).toBeVisible();
    const beforeA = (await readJSON<Runtime>(pageA, "/api/work/runtime")).projects ?? [];
    if (!beforeA.some((row) => row.path === markerA)) {
      await pageA.goto(new URL("/?section=settings", A!).href);
      await pageA.getByLabel("Folder path").fill(markerA);
      await pageA.getByRole("button", { name: "Add", exact: true }).click();
      await expect(pageA.getByText("Project added.")).toBeVisible();
    }

    const pageB = await contextB.newPage();
    await unlock(pageB, B!, B_TOKEN!);
    await expect(pageB.getByRole("heading", { level: 1, name: "Conversaciones" })).toBeVisible();
    // Marcador propio de B, distinto del de A y en su propio estado.
    const beforeB = (await readJSON<Runtime>(pageB, new URL("/api/work/runtime", B!).href)).projects ?? [];
    if (!beforeB.some((row) => row.path === markerB)) {
      // Absoluto: el runner apunta su baseURL a la instalación A.
      await pageB.goto(new URL("/?section=settings", B!).href);
      await pageB.getByLabel("Ruta de la carpeta").fill(markerB);
      await pageB.getByRole("button", { name: "Añadir", exact: true }).click();
      await expect(pageB.getByText("Proyecto añadido.")).toBeVisible();
    }

    // La autoridad de A no concede nada en B (mismo host, otra instancia) y el estado
    // de A no aparece en B.
    const foreign = await pageA.request.get(new URL("/api/bootstrap", B!).href);
    expect(foreign.status(), "la cookie de A no autoriza B").toBe(401);
    const runtimeB = await readJSON<Runtime>(pageB, new URL("/api/work/runtime", B!).href);
    expect(
      (runtimeB.projects ?? []).map((row) => row.path),
      "B ve un proyecto de A",
    ).not.toContain(markerA);
    const bootstrapB = await readJSON<Bootstrap>(pageB, new URL("/api/bootstrap", B!).href);
    expect(
      (bootstrapB.settings?.projects ?? []).map((row) => row.path),
      "B heredó el proyecto de A",
    ).not.toContain(markerA);

    // Logout en A retira sólo su autoridad; B sigue con la suya tras recargar.
    await pageA.getByRole("button", { name: "Sign out" }).click();
    await expect(pageA.locator("#access-token"), "A vuelve a pedir sesión").toBeVisible();
    expect(
      (await pageA.request.get(new URL("/api/bootstrap", A!).href)).status(),
      "la autoridad de A fue retirada",
    ).toBe(401);
    await pageB.goto(new URL("/", B!).href);
    await expect(pageB.getByRole("heading", { level: 1, name: "Conversaciones" })).toBeVisible();
    expect(
      (await pageB.request.get(new URL("/api/bootstrap", B!).href)).status(),
      "B conserva su autoridad",
    ).toBe(200);
    await pageB.reload();
    await expect(pageB.getByRole("heading", { level: 1, name: "Conversaciones" })).toBeVisible();
    expect(
      (await readJSON<Bootstrap>(pageB, new URL("/api/bootstrap", B!).href)).settings?.projects ?? [],
    ).toContainEqual(expect.objectContaining({ path: markerB }));
  } finally {
    await contextA.close();
    await contextB.close();
    await restoreMarkers(browser, markerA, markerB);
  }
});

