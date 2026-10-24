import {
  expect,
  test,
  type Browser,
  type BrowserContext,
  type Frame,
  type Page,
} from "@playwright/test";
import { sections } from "../../src/app/navigation";
import { BASE, requireRelease, signIn, watchConsole, type ConsoleGuard } from "./helpers";

// OP-11: corte de la red EXTERNA del CLIENTE sobre la instalación real. El único punto de
// corte es el navegador: se aborta toda petición que no sea loopback (HTTP y WebSocket) y
// NADA se falsea — no hay route.fulfill ni API simulada, todo lo demás va contra el Root y
// las dos aplicaciones ORIGINALES que esta instalación sirve (el Magpie del host embebido y
// el SIM enmarcado). La sesión del SIM es un fixture SINTÉTICO de su dueño: se entrega como
// fichero de storage al contexto y no se importa ninguna cuenta del operador ni se toca su
// configuración. Release real:
//
//   E2E_BASE_URL=http://127.0.0.1:18996 E2E_ACCESS_TOKEN_FILE=<state>/access.token \
//     bun run test tests/e2e/assets-offline-sim-release.spec.ts
//
// El tramo del SIM original necesita además el fixture sintético:
//   E2E_SIM_STORAGE_STATE=<fixture>/storage.json
//   E2E_SIM_WORKFLOW_URL=http://127.0.0.1:33500/workspace/<workspace>/w/<workflow>

test.beforeAll(requireRelease);

// Loopbacks de la propia máquina: es lo único que el corte deja pasar.
const LOOPBACK: Record<string, true> = { "127.0.0.1": true, localhost: true, "::1": true, "[::1]": true };

interface Reply {
  url: string;
  status: number;
  type: string;
  contentType: string;
}

interface Cut {
  /** Peticiones fuera de loopback que el navegador abortó (el corte, no una simulación). */
  aborted: string[];
  /** Sockets que abrió el navegador: también deben quedarse en loopback. */
  websockets: string[];
  requests: { method: string; url: string; type: string }[];
  replies: Reply[];
}

/** Corte de WAN del cliente: lo que no sea loopback no sale del navegador. */
async function cutExternalWAN(context: BrowserContext): Promise<Cut> {
  const cut: Cut = { aborted: [], websockets: [], requests: [], replies: [] };
  const types = new Map<string, string>();
  await context.route("**/*", async (route) => {
    const url = new URL(route.request().url());
    if (LOOPBACK[url.hostname] !== true) {
      cut.aborted.push(url.href);
      await route.abort();
      return;
    }
    await route.continue();
  });
  if (typeof context.routeWebSocket === "function")
    await context.routeWebSocket(/.*/, (socket) => {
      const url = new URL(socket.url());
      cut.websockets.push(url.href);
      if (LOOPBACK[url.hostname] !== true) return socket.close();
      socket.connectToServer();
    });
  context.on("request", (request) => {
    types.set(request.url(), request.resourceType());
    cut.requests.push({ method: request.method(), url: request.url(), type: request.resourceType() });
  });
  context.on("response", (response) =>
    cut.replies.push({
      url: response.url(),
      status: response.status(),
      type: types.get(response.url()) ?? "other",
      contentType: response.headers()["content-type"] ?? "",
    }),
  );
  return cut;
}

/** Ni un CDN, ni una fuente remota, ni telemetría externa: todo lo pidió a loopback. Sólo
 *  quedan fuera de la cuenta las sondas externas que el propio caso declara. */
function expectLoopbackOnly(cut: Cut, probes: string[] = []) {
  expect(
    cut.aborted.filter((url) => !probes.includes(url)),
    "peticiones fuera de loopback",
  ).toEqual([]);
  for (const socket of cut.websockets)
    expect(LOOPBACK[new URL(socket).hostname] === true, `socket: ${socket}`).toBe(true);
  for (const host of new Set(cut.requests.map((row) => new URL(row.url).hostname)))
    expect(
      LOOPBACK[host] === true || probes.some((url) => new URL(url).hostname === host),
      `origen pedido por el navegador: ${host}`,
    ).toBe(true);
}

/** Resumen del recorrido para que el registro de la ejecución sirva de evidencia. */
function summarize(cut: Cut) {
  const byOrigin: Record<string, number> = {};
  for (const row of cut.requests) {
    const host = new URL(row.url).host;
    byOrigin[host] = (byOrigin[host] ?? 0) + 1;
  }
  const byType: Record<string, number> = {};
  for (const row of cut.replies) byType[row.type] = (byType[row.type] ?? 0) + 1;
  return `orígenes=${JSON.stringify(byOrigin)} tipos=${JSON.stringify(byType)} respuestas=${cut.replies.length} sockets=${cut.websockets.length}`;
}

/** Assets locales: lo que el navegador ejecuta o pinta responde 200 desde loopback y ningún
 *  asset —viejo o ausente— vuelve con el HTML de la shell. Sólo se toleran los negativos que
 *  el propio caso provoca (404 deliberados) y el 401 de arrancar sin sesión. */
function expectLocalAssets(cut: Cut, deliberate: string[]) {
  for (const type of ["script", "stylesheet", "font", "image"]) {
    const rows = cut.replies.filter((reply) => reply.type === type);
    // La shell publica sus scripts y su hoja: esos tienen que estar. Fuentes e imágenes no
    // siempre se piden (usa las del sistema y dibuja iconos en línea); si aparecen, tienen que
    // salir de loopback y responder 200 — una remota no pasaría el corte de red de arriba.
    if (type === "script" || type === "stylesheet")
      expect(rows.length, `assets ${type} pedidos por la interfaz`).toBeGreaterThan(0);
    expect(rows.filter((row) => row.status !== 200), `respuestas ${type}`).toEqual([]);
  }
  expect(
    cut.replies.filter((row) => row.type !== "document" && row.contentType.includes("text/html")),
    "ningún recurso no-documento vuelve como HTML de la shell",
  ).toEqual([]);
  expect(
    cut.replies.filter(
      (row) =>
        row.status >= 400 &&
        !deliberate.includes(row.url) &&
        !(row.status === 401 && row.url.includes("/api/bootstrap")),
    ),
    "respuestas de error inesperadas",
  ).toEqual([]);
}

/** Referencias de marcado que apunten a otro origen: un CDN se ve aquí aunque su política
 *  nunca llegue a pedirlo. Se ejecuta sobre documentos propios, sin tocar nada. */
async function externalMarkupReferences(target: Page | Frame) {
  return target.evaluate(() =>
    [...document.querySelectorAll("script[src], link[href], img[src], source[src]")]
      .map((node) => node.getAttribute("src") ?? node.getAttribute("href") ?? "")
      .filter(
        (value) =>
          /^(https?:)?\/\//i.test(value) &&
          !value.startsWith(location.origin) &&
          !value.startsWith(`//${location.host}`),
      ),
  );
}

interface Release {
  context: BrowserContext;
  page: Page;
  cut: Cut;
  guard: ConsoleGuard;
}

async function openRelease(browser: Browser, storageState?: string): Promise<Release> {
  const context = await browser.newContext({ baseURL: BASE, storageState });
  const cut = await cutExternalWAN(context);
  const page = await context.newPage();
  const guard = watchConsole(page);
  await signIn(page);
  return { context, page, cut, guard };
}

async function magpiePanel(page: Page) {
  const iframe = page.locator("iframe[title^='Magpie']");
  await expect(iframe, "panel embebido del Magpie original").toBeVisible({ timeout: 30_000 });
  const src = (await iframe.getAttribute("src")) ?? "";
  const origin = new URL(src).origin;
  expect(LOOPBACK[new URL(origin).hostname] === true, `origen del panel: ${origin}`).toBe(true);
  const panel = page.frameLocator("iframe[title^='Magpie']");
  await expect(panel.locator("body")).toContainText(/\S/, { timeout: 30_000 });
  const frame = page.frames().find((candidate) => candidate.url().startsWith(origin));
  expect(frame, `sin marco en ${origin}`).toBeTruthy();
  expect(frame!.url(), "el handshake no deja la concesión en la URL").not.toContain("#");
  expect(await externalMarkupReferences(frame!), `referencias externas del panel ${origin}`).toEqual([]);
  return { panel, frame: frame! };
}

test("el corte de WAN del navegador es real, no lleva credenciales fuera y la app sigue navegando", async ({
  browser,
}) => {
  test.setTimeout(120_000);
  const context = await browser.newContext({ baseURL: BASE });
  const cut = await cutExternalWAN(context);
  try {
    const page = await context.newPage();
    // Control negativo ANTES de entrar: el contexto no tiene ninguna cookie que pudiera
    // viajar a un origen externo, así que las sondas salen sin credenciales. Las sondas usan su
    // propia pestaña para no dejar la de la aplicación en un error de navegación.
    expect(await context.cookies("http://example.invalid/"), "cookies aplicables al origen externo").toEqual([]);
    const probes = await context.newPage();

    // Sonda desde un documento propio sin política de por medio: el rechazo es del corte, no
    // del CSP del shell.
    const fetched = await probes.evaluate(() =>
      fetch("http://example.invalid/op11-fetch").then(
        () => "resolved",
        (error: Error) => `rejected:${error.name}`,
      ),
    );
    expect(fetched, "una petición externa se rechaza").toBe("rejected:TypeError");
    expect(cut.aborted, "el corte abortó la sonda externa").toContain("http://example.invalid/op11-fetch");

    // Y una navegación entera al mismo origen externo tampoco se completa.
    const navigation = await probes.goto("http://example.invalid/op11").catch((error) => String(error));
    expect(navigation, "una navegación externa no se completa").toContain("example.invalid");
    expect(cut.aborted, "el corte abortó la navegación externa").toContain("http://example.invalid/op11");
    await probes.close();

    // Recuperación: con el corte activo la instalación real entra y navega por sus secciones.
    await signIn(page);
    const nav = page.getByRole("navigation", { name: "Navegación principal" });
    await nav.getByRole("button", { name: "Conversaciones", exact: true }).click();
    await expect(page.getByRole("heading", { level: 1, name: "Conversaciones" })).toBeVisible();
    expectLoopbackOnly(cut, ["http://example.invalid/op11", "http://example.invalid/op11-fetch"]);
    console.log(`OP11 corte WAN: ${summarize(cut)} sondas abortadas=${cut.aborted.length}`);
    await context.close();
  } catch (error) {
    await context.close();
    throw error;
  }
});

test("todas las secciones sin proveedor y el Magpie original salen de loopback", async ({ browser }) => {
  test.setTimeout(300_000);
  const { context, page, cut, guard } = await openRelease(browser);
  const deliberate: string[] = [];
  try {
    // Recorrido real por los controles de la shell: cada sección sin proveedor se abre y
    // muestra su pantalla, no un hueco.
    const nav = page.getByRole("navigation", { name: "Navegación principal" });
    for (const section of sections) {
      await nav.getByRole("button", { name: section.label, exact: true }).click();
      await expect(
        page.getByRole("heading", { level: 1, name: section.label }),
        `sección ${section.label}`,
      ).toBeVisible();
      expect(
        (await page.locator("body").innerText()).trim().length,
        `contenido de ${section.label}`,
      ).toBeGreaterThan(0);
      // El SIM es otra aplicación enmarcada: sin sesión sintética muestra su propio acceso,
      // pero nunca un marco en blanco.
      if (section.id === "sim")
        await expect(
          page.frameLocator("iframe[title^='SIM']").locator("body"),
          "el SIM original responde en su marco",
        ).toContainText(/\S/, { timeout: 30_000 });
    }
    expect(await externalMarkupReferences(page), "referencias externas de la shell").toEqual([]);

    // Modelos: cada pestaña abre el panel ORIGINAL de ese Magpie por el origen nativo del
    // host (proxy autenticado), sin copia ni fachada propia.
    await page.goto("/?section=models", { waitUntil: "domcontentloaded" });
    const { panel } = await magpiePanel(page);
    await expect(panel.locator("body")).toContainText(/Providers/);
    for (const [view, label] of [["codex", "Cuentas"], ["agents", "Agents"], ["catalog", "Catálogo"], ["routing", "Rutas"], ["keys", "Claves y LAN"], ["usage", "Uso"], ["settings", "Ajustes"]]) {
      await page.goto(`/?section=models&view=${view}`, { waitUntil: "domcontentloaded" });
      const current = await magpiePanel(page);
      expect(await externalMarkupReferences(current.frame), `referencias externas de ${label}`).toEqual([]);
    }

    // Ruta profunda fuera del router: la sirve el fallback de la shell y sobrevive la recarga.
    await page.goto("/instalacion/profunda/no-esta-en-el-router?section=settings&view=", {
      waitUntil: "domcontentloaded",
    });
    await expect(page.getByRole("heading", { level: 1, name: "Ajustes" })).toBeVisible();
    expect(await page.content(), "la ruta profunda es la shell, no un error").toContain('<div id="root"');
    await page.reload({ waitUntil: "domcontentloaded" });
    await expect(page.getByRole("heading", { level: 1, name: "Ajustes" })).toBeVisible();

    // Asset publicado y asset viejo: el que existe sale del Root; un hash que ya no está no
    // puede volver como la SPA.
    const published = await page.evaluate(() =>
      [...document.querySelectorAll("script[src]")]
        .map((node) => node.getAttribute("src") ?? "")
        .filter((value) => /\/assets\/.+\.js$/.test(value)),
    );
    expect(published.length, "el shell publica sus scripts embebidos").toBeGreaterThan(0);
    const stale = published[0]!.replace(/[A-Za-z0-9_-]+\.js$/, "viejo-que-ya-no-esta.js");
    // Los dos 404 son deliberados: se declaran antes de provocarlos para que el registro de
    // consola no los confunda con una rotura inesperada.
    for (const path of [stale, "/assets/op11-no-existe.css"]) guard.expected.push(path);
    for (const path of [stale, "/assets/op11-no-existe.css"]) {
      const reply = await page.evaluate(async (target) => {
        const response = await fetch(target);
        return {
          status: response.status,
          contentType: response.headers.get("content-type") ?? "",
          body: (await response.text()).slice(0, 160),
        };
      }, path);
      expect(reply.status, `asset ausente ${path}`).toBe(404);
      expect(reply.contentType, `MIME de ${path}`).toContain("application/json");
      expect(reply.body, `cuerpo de ${path}`).toContain("not_found");
      expect(reply.body, `un asset ausente nunca devuelve la shell (${path})`).not.toContain('<div id="root"');
      deliberate.push(new URL(path, BASE).href);
    }

    // Recuperación de la ruta original: la misma ruta profunda sigue sirviéndose como shell y
    // la interfaz vuelve a navegar en el mismo contexto.
    await page.reload({ waitUntil: "domcontentloaded" });
    await expect(page.getByRole("heading", { level: 1, name: "Ajustes" })).toBeVisible();
    await nav.getByRole("button", { name: "Uso global", exact: true }).click();
    await expect(page.getByRole("heading", { level: 1, name: "Uso global" })).toBeVisible();

    expectLoopbackOnly(cut);
    expectLocalAssets(cut, deliberate);
    expect(guard.errors, "consola del navegador").toEqual([]);
    console.log(`OP11 secciones+Magpie: ${summarize(cut)} 404 deliberados=${deliberate.length}`);
    await context.close();
  } catch (error) {
    await context.close();
    throw error;
  }
});

test("el workflow sintético del SIM original se abre y se recarga sin salir de loopback", async ({ browser }) => {
  test.setTimeout(300_000);
  const storage = process.env.E2E_SIM_STORAGE_STATE;
  const workflow = process.env.E2E_SIM_WORKFLOW_URL;
  test.skip(
    !storage || !workflow,
    "sin E2E_SIM_STORAGE_STATE ni E2E_SIM_WORKFLOW_URL no hay sesión sintética del SIM: no se importa ninguna cuenta del operador ni se crea otra",
  );
  const { context, page, cut, guard } = await openRelease(browser, storage);
  // El SIM original es otra aplicación: su propia consola (socket de desarrollo incluido) no
  // es el contrato de este archivo, como en sim-release.spec.ts. Los errores del Root sí.
  guard.expected.push(new URL(workflow!).origin);
  try {
    await page.goto("/?section=sim", { waitUntil: "domcontentloaded" });
    const iframe = page.locator("iframe[title^='SIM']");
    await expect(iframe, "el SIM original se enmarca tal cual").toBeVisible({ timeout: 30_000 });
    const src = (await iframe.getAttribute("src")) ?? "";
    expect(src, "el marco apunta al SIM publicado por la instalación").toBeTruthy();

    const origin = new URL(workflow!).origin;
    let frame = page.frames().find((candidate) => candidate.url().startsWith(origin));
    for (let attempt = 0; attempt < 30 && !frame; attempt += 1) {
      await page.waitForTimeout(1000);
      frame = page.frames().find((candidate) => candidate.url().startsWith(origin));
    }
    expect(frame, `sin marco en ${origin}`).toBeTruthy();

    // Ruta profunda del SIM original: el workflow sintético de su dueño, sólo lectura.
    await frame!.goto(workflow!, { waitUntil: "domcontentloaded" });
    const nodes = frame!.locator(".react-flow__node");
    await expect(nodes.first(), "nodos del workflow sintético").toBeVisible({ timeout: 60_000 });
    expect(await nodes.count(), "nodos del workflow sintético").toBeGreaterThanOrEqual(2);
    const rendered = (await nodes.allInnerTexts()).join(" | ");
    expect(rendered, "el lienzo muestra el workflow sintético").toContain("Start");
    await expect(frame!.locator(".react-flow__pane, canvas").first()).toBeVisible();
    expect(await externalMarkupReferences(frame!), "referencias externas del SIM").toEqual([]);

    // Recarga real de esa misma ruta profunda (el marco no tiene reload propio en Playwright):
    // el workflow sigue ahí, sin ejecutarlo.
    await frame!.evaluate(() => location.reload()).catch(() => {});
    await expect(nodes.first(), "nodos tras recargar la ruta profunda").toBeVisible({ timeout: 60_000 });
    expect(frame!.url(), "la recarga se queda en el workflow sintético").toContain(new URL(workflow!).pathname);

    expectLoopbackOnly(cut);
    expectLocalAssets(cut, []);
    expect(cut.replies.filter((row) => row.status === 404), "ningún asset ausente en el SIM").toEqual([]);
    // Sólo lectura: el recorrido no lanza ninguna ejecución del workflow (la app original sí
    // registra la visita del workspace y renueva su propio token de socket, eso no ejecuta nada).
    expect(
      cut.requests
        .filter(
          (row) =>
            !["GET", "HEAD", "OPTIONS"].includes(row.method) &&
            /(run|execute|deploy|invoke|trigger)/i.test(new URL(row.url).pathname),
        )
        .map((row) => `${row.method} ${row.url}`),
      "el recorrido no lanza ninguna ejecución",
    ).toEqual([]);
    expect(guard.errors, "consola del navegador").toEqual([]);
    console.log(`OP11 SIM original: ${summarize(cut)} marco=${frame!.url()} nodos=${await nodes.count()}`);
    await context.close();
  } catch (error) {
    await context.close();
    throw error;
  }
});
