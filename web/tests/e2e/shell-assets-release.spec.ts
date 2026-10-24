import { expect, test, type BrowserContext } from "@playwright/test";
import { BASE, requireRelease, signIn, TOKEN, watchConsole } from "./helpers";

declare global {
  interface Window {
    __pl12Inline?: string;
  }
}

// M02 PL-12: la instalación sirve su shell y sus assets EMBEBIDOS desde su propio
// origen, sin Internet externo, y una API desconocida o un asset ausente fallan con su
// código/cuerpo/MIME honestos (nunca una SPA 200). Release real: E2E_BASE_URL +
// E2E_ACCESS_TOKEN. El bloqueo de lo externo es un control negativo del caso, no un
// mock: sólo se aborta lo que no sea este origen y todo lo demás va contra el Root real.
//
//   E2E_BASE_URL=http://127.0.0.1:19012 E2E_ACCESS_TOKEN=... \
//     bun run test tests/e2e/shell-assets-release.spec.ts

test.beforeAll(requireRelease);

const LOOPBACK = new Set(["127.0.0.1", "localhost", "::1", "[::1]"]);

interface Spy {
  external: string[];
  requests: { url: string; type: string }[];
  responses: { url: string; status: number }[];
}

/** Este origen sigue permitido; cualquier CDN o servicio externo queda bloqueado. */
async function blockExternal(context: BrowserContext): Promise<Spy> {
  const spy: Spy = { external: [], requests: [], responses: [] };
  await context.route("**/*", async (route) => {
    const url = new URL(route.request().url());
    if (!LOOPBACK.has(url.hostname)) {
      spy.external.push(url.href);
      await route.abort();
      return;
    }
    await route.continue();
  });
  context.on("request", (request) => spy.requests.push({ url: request.url(), type: request.resourceType() }));
  context.on("response", (response) => spy.responses.push({ url: response.url(), status: response.status() }));
  return spy;
}

/** Nada fuera de loopback: ni CDN, ni fuentes, ni telemetría externa. El panel nativo
 *  de cada host es otro puerto/alias de loopback y por eso también está permitido. */
async function expectNoExternal(spy: Spy) {
  expect(spy.external, "peticiones fuera de este origen").toEqual([]);
  for (const host of new Set(spy.requests.map((row) => new URL(row.url).hostname)))
    expect(LOOPBACK.has(host), `origen pedido por el shell: ${host}`).toBe(true);
}

/** Sin panel nativo abierto, todo lo que pide la shell sale de su propio origen. */
function sameOriginOnly(spy: Spy) {
  expect([...new Set(spy.requests.map((row) => new URL(row.url).origin))]).toEqual([
    new URL(BASE!).origin,
  ]);
}

test("el shell, la ruta profunda y sus assets salen del propio origen sin Internet", async ({
  browser,
}) => {
  test.setTimeout(120_000);
  const context = await browser.newContext();
  const spy = await blockExternal(context);
  const page = await context.newPage();
  const guard = watchConsole(page);
  await signIn(page);

  // Navegación real por los controles de la interfaz.
  const nav = page.getByRole("navigation", { name: "Navegación principal" });
  await nav.getByRole("button", { name: "Conversaciones", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Conversaciones" })).toBeVisible();

  // Ruta profunda con query, servida por el fallback del shell, y recarga en el mismo módulo.
  await page.goto("/instalacion/profunda/no-esta-en-el-router?section=settings&view=");
  await expect(page.getByRole("heading", { level: 1, name: "Ajustes" })).toBeVisible();
  const deep = await page.content();
  expect(deep, "la ruta profunda es el shell, no un error").toContain("<div id=\"root\"");
  await page.reload();
  await expect(page.getByRole("heading", { level: 1, name: "Ajustes" })).toBeVisible();

  // Contexto nuevo y frío (sin caché ni almacenamiento): el mismo recurso vuelve a salir del Root.
  const cold = await browser.newContext();
  const coldSpy = await blockExternal(cold);
  const coldPage = await cold.newPage();
  await signIn(coldPage);
  await coldPage.goto("/instalacion/profunda/no-esta-en-el-router?section=settings&view=");
  await expect(coldPage.getByRole("heading", { level: 1, name: "Ajustes" })).toBeVisible();
  await expectNoExternal(coldSpy);
  sameOriginOnly(coldSpy);
  const scripts = coldSpy.responses.filter((row) => /\/assets\/.+\.js$/.test(row.url));
  expect(scripts.length, "scripts embebidos pedidos al Root en contexto nuevo").toBeGreaterThan(0);
  expect(scripts.filter((row) => row.status !== 200), "todos los scripts embebidos responden 200").toEqual([]);
  await cold.close();

  await expectNoExternal(spy);
  sameOriginOnly(spy);
  for (const type of ["script", "stylesheet"]) {
    expect(
      spy.requests.filter((row) => row.type === type).length,
      `assets ${type} servidos por la instalación`,
    ).toBeGreaterThan(0);
  }
  expect(guard.errors, "consola del navegador").toEqual([]);
  await context.close();
});

test("una API desconocida y un asset ausente fallan con su código y el shell sigue vivo", async ({
  browser,
}) => {
  test.setTimeout(120_000);
  // Sin sesión, una API desconocida no concede nada ni se disfraza de página.
  const anonymous = await browser.newContext({ baseURL: BASE });
  const denied = await anonymous.request.get("/api/desconocida");
  expect(denied.status(), "API desconocida sin sesión").toBe(401);
  expect(denied.headers()["content-type"]).toContain("application/json");
  expect(await denied.text()).toContain("authentication_required");
  await anonymous.close();

  const context = await browser.newContext();
  const spy = await blockExternal(context);
  const page = await context.newPage();
  const guard = watchConsole(page);
  await signIn(page);
  await expect(TOKEN ?? "").not.toBe("");

  const unknown = await page.request.get("/api/desconocida");
  expect(unknown.status(), "API desconocida con sesión").toBe(404);
  expect(unknown.headers()["content-type"]).toContain("application/json");
  expect(await unknown.text()).toContain("not_found");
  expect((await page.request.head("/api/desconocida")).status(), "HEAD de API desconocida").toBe(404);

  const missing = await page.request.get("/assets/no-existe.js");
  expect(missing.status(), "asset ausente").toBe(404);
  expect(missing.headers()["content-type"]).toContain("application/json");
  expect(await missing.text(), "un asset ausente nunca devuelve la SPA").not.toContain("<div id=\"root\"");

  // Metadatos reales del recurso embebido principal.
  const html = await (await page.request.get("/")).text();
  const asset = /assets\/index-[A-Za-z0-9_-]+\.js/.exec(html)?.[0];
  expect(asset, "el shell publica su script embebido").toBeTruthy();
  const assetResponse = await page.request.get(`/${asset}`);
  expect(assetResponse.status()).toBe(200);
  expect(assetResponse.headers()["content-type"], "MIME del script").toContain("text/javascript");
  expect(assetResponse.headers()["cache-control"], "los assets no se cachean").toBe("no-store");
  expect(assetResponse.headers()["x-content-type-options"]).toBe("nosniff");
  const head = await page.request.head(`/${asset}`);
  expect(head.status()).toBe(200);
  const invalid = await page.request.put("/");
  expect(invalid.status(), "método inválido sobre el shell").toBe(405);
  expect(invalid.headers()["allow"]).toContain("GET");

  // Recuperación: tras los negativos la interfaz sigue funcionando en el mismo contexto.
  await page.goto("/?section=settings");
  await expect(page.getByRole("heading", { level: 1, name: "Ajustes" })).toBeVisible();
  await expectNoExternal(spy);
  expect(guard.errors, "consola del navegador").toEqual([]);
  await context.close();
});

test("la política del shell bloquea script en línea y deja intactos widgets y panel nativo", async ({
  browser,
}) => {
  test.setTimeout(180_000);
  const context = await browser.newContext();
  const spy = await blockExternal(context);
  const page = await context.newPage();
  const guard = watchConsole(page);
  const violations: string[] = [];
  page.on("console", (message) => {
    if (message.text().includes("Content Security Policy")) violations.push(message.text());
  });
  // La violación del script en línea la provoca este caso a propósito.
  guard.expected.push("Content Security Policy");
  await signIn(page);

  // La política viaja en la respuesta real del documento y no fija los frames de runtime.
  const shellDocument = await page.request.get("/?section=settings");
  expect(shellDocument.status()).toBe(200);
  const policy = shellDocument.headers()["content-security-policy"] ?? "";
  for (const directive of ["script-src 'self'", "object-src 'none'", "base-uri 'none'", "frame-ancestors 'self'"])
    expect(policy, `política del shell: ${directive}`).toContain(directive);
  expect(policy, "la política no fija los orígenes de los frames").not.toContain("default-src");
  expect(policy, "la política no fija los orígenes de los frames").not.toContain("frame-src");

  // Inserción en el DOM: el script en línea queda sujeto a la política y no se ejecuta.
  await page.evaluate(() => {
    const probe = document.createElement("script");
    probe.textContent = "window.__pl12Inline = 'ran'";
    document.body.append(probe);
  });
  expect(await page.evaluate(() => window.__pl12Inline ?? null), "script en línea sin autorizar").toBeNull();
  await expect
    .poll(() => violations.length, { message: "la política se aplica de verdad", timeout: 5_000 })
    .toBeGreaterThan(0);

  // CSS y widgets siguen intactos: la hoja del tema se aplica y la paleta (Base UI) abre.
  const themeToken = await page.evaluate(() =>
    getComputedStyle(document.documentElement).getPropertyValue("--background").trim(),
  );
  expect(themeToken, "hoja de estilo del tema aplicada").not.toBe("");
  await page.keyboard.press("Control+k");
  await expect(page.getByRole("dialog"), "paleta de la shell").toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toHaveCount(0);

  // El panel original del Magpie de este host sigue visible: su origen lo valida la app,
  // no una allow-list estática en la política del shell.
  await page.goto("/?section=models&view=agents");
  const panel = page.frameLocator('iframe[title^="Magpie"]');
  await expect(panel.getByRole("button", { name: "Agents", exact: true })).toBeVisible({ timeout: 30_000 });
  expect(violations.filter((text) => /frame|Magpie/i.test(text)), "el frame no viola la política del shell").toEqual([]);
  await expectNoExternal(spy);
  expect(guard.errors, "consola del navegador").toEqual([]);
  await context.close();
});
