import { expect, test, type Page } from "@playwright/test";
import { nativeViews, viewOf, views } from "../../src/modules/models/domain";
import { translate } from "../../src/modules/models/i18n";
import { requireRelease, signIn, watchConsole, type ConsoleGuard } from "./helpers";

// Modelos es el contenedor del Magpie ORIGINAL de cada host: el marco nativo, el host en
// ?host= y la pestaña grande nueva. La sección no dibuja cuentas, claves ni uso propios.
// Release real: E2E_BASE_URL + E2E_ACCESS_TOKEN, sin mock, sin route.fulfill, sin skip.
//
//   E2E_BASE_URL=http://127.0.0.1:5184 E2E_ACCESS_TOKEN=... \
//     bun run test tests/e2e/models-native-release.spec.ts

let guard: ConsoleGuard | null = null;

test.beforeAll(requireRelease);

test.beforeEach(({ page }) => {
  guard = watchConsole(page);
  // The unreachable host of this file is asked on purpose: its refusal is an expected
  // answer of that case, and the old remote-history route still answers 501.
  guard.expected.push("/api/hosts/");
  guard.expected.push("/history");
});

test.afterEach(() => {
  expect(guard?.errors ?? [], "consola del navegador").toEqual([]);
});

test.describe("contratos puros del contenedor", () => {
  test("los enlaces profundos ya existentes siguen nombrando la misma pestaña", () => {
    // Trabajo abre Cuentas (WorkSection), Equipos y los marcadores retirados de Atlas, Uso.
    expect(viewOf("codex")).toBe("codex");
    expect(viewOf("usage")).toBe("usage");
    expect(viewOf("catálogo")).toBe(views[0][0]);
    expect(viewOf(undefined)).toBe(views[0][0]);
    const ids = views.map(([id]) => id);
    expect(ids).toEqual(["codex", "agents", "catalog", "routing", "keys", "usage", "settings"]);
  });

  test("cada pestaña tiene su pantalla nativa y ninguna se queda sin destino", () => {
    for (const [id] of views) expect(nativeViews[id], id).toBeTruthy();
    // Cuentas y Catálogo son dos entradas del mismo Providers nativo; el resto no se repite.
    expect(nativeViews.codex).toBe("providers");
    expect(nativeViews.catalog).toBe("providers");
    expect(nativeViews.agents).toBe("agents");
    expect(nativeViews.routing).toBe("routing");
    expect(nativeViews.keys).toBe("gateway");
    expect(nativeViews.usage).toBe("usage");
    expect(nativeViews.settings).toBe("settings");
  });

  test("las cadenas del contenedor tienen inglés", () => {
    const missing = [
      "Recargar",
      "Abrir en pestaña",
      "Abriendo Magpie…",
      "No se pudo abrir Magpie",
      "Preparar Magpie y revisar SSH",
      "Panel nativo completo de Magpie",
      "Host de los modelos",
    ].filter((text) => translate(text, "en") === text && !/^[A-Za-z0-9 .·…]+$/.test(text));
    expect(missing, "cadenas sin inglés").toEqual([]);
  });
});

/** Real record of this installation, created through the authorized root API: enabled
 *  against a port where nothing listens, so the panel cannot be prepared and the case
 *  proves the refusal, never a credential. Find-or-create: the same record the Atlas
 *  regression uses, kept between runs. */
async function unreachableHost(page: Page) {
  const marker = "regresion-panel-host";
  const list = (await (await page.request.get("/api/hosts")).json()) as {
    hosts?: { id: string; name?: string; enabled?: boolean }[];
  };
  const existing = (list.hosts ?? []).find((row) => row.name === marker);
  if (existing) return existing.id;
  const created = await page.request.post("/api/hosts", {
    data: {
      name: marker,
      address: "127.0.0.1",
      user: "e2e",
      port: 18999,
      auth: "password",
      password: "e2e-sentinel-not-a-credential",
      enabled: true,
    },
  });
  expect([200, 201], "alta del equipo de la regresión").toContain(created.status());
  const body: unknown = await created.json();
  if (!body || typeof body !== "object" || !("id" in body) || typeof body.id !== "string")
    throw new Error("el alta del equipo no devolvió su id");
  return body.id;
}

test("Modelos enmarca el Magpie original del host local en la pantalla pedida", async ({ page }) => {
  await signIn(page, "/?section=models&view=agents");
  await expect(
    page.frameLocator('iframe[title^="Magpie"]').locator("#view-agents"),
  ).toHaveJSProperty("hidden", false);
  await expect(page.getByLabel("Host de los modelos")).toHaveValue("local");
  const frame = page.frameLocator('iframe[title^="Magpie"]');
  // La UI original, no una pantalla propia: su navegación completa está ahí.
  await expect(frame.locator("#nav button[data-view]").first()).toBeVisible();
  expect(await frame.locator("#nav button[data-view]").count()).toBeGreaterThanOrEqual(6);
  // La pantalla pedida es la activa de la UI nativa (show() oculta las demás).
  await expect(frame.locator("#view-agents")).toHaveJSProperty("hidden", false);
  await expect(frame.locator("#view-providers")).toHaveJSProperty("hidden", true);
  await expect(frame.locator('#nav button[data-view="agents"]')).toHaveAttribute("aria-current", "page");
  // Nada del panel queda guardado en este navegador: ni la concesión ni una clave.
  const stored = await page.evaluate(() =>
    JSON.stringify([{ ...localStorage }, { ...sessionStorage }]),
  );
  expect(stored).not.toMatch(/grant|magpie_web|token/i);
});

test("cambiar de pantalla y recargar conserva host y pantalla nativa", async ({ page }) => {
  await signIn(page, "/?section=models&view=codex");
  await expect(
    page.frameLocator('iframe[title^="Magpie"]').locator("#view-providers"),
  ).toHaveJSProperty("hidden", false);
  await page.goto("/?section=models&view=usage");
  await expect(page).toHaveURL(/section=models&view=usage/);
  await expect(
    page.frameLocator('iframe[title^="Magpie"]').locator("#view-usage"),
  ).toHaveJSProperty("hidden", false);
  await page.reload();
  await expect(page).toHaveURL(/section=models&view=usage/);
  await expect(
    page.frameLocator('iframe[title^="Magpie"]').locator("#view-usage"),
  ).toHaveJSProperty("hidden", false);
  await expect(page.getByLabel("Host de los modelos")).toHaveValue("local");
  // Volver atrás devuelve a la pantalla anterior del mismo host, sin recargar la sección.
  await page.goBack();
  await expect(page).toHaveURL(/section=models&view=codex/);
  await expect(
    page.frameLocator('iframe[title^="Magpie"]').locator("#view-providers"),
  ).toHaveJSProperty("hidden", false);
  await expect(page.getByLabel("Host de los modelos")).toHaveValue("local");
});

test("un host sin Magpie se ve como error real y Reintentar lo vuelve a pedir", async ({ page }) => {
  await signIn(page, "/?section=models&view=usage");
  const id = await unreachableHost(page);
  const asked: string[] = [];
  page.on("request", (request) => {
    if (request.url().includes("/magpie/open")) asked.push(request.url());
  });
  // Cambiar de host desde el selector: el panel del servidor local se sustituye por el
  // error real del host elegido, y volver al local recupera su panel.
  await expect(
    page.frameLocator('iframe[title^="Magpie"]').locator("#view-usage"),
  ).toHaveJSProperty("hidden", false);
  await page.getByLabel("Host de los modelos").selectOption(id);
  const alert = page.getByRole("alert");
  await expect(alert).toBeVisible();
  await expect(alert.getByRole("button", { name: "Reintentar" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Preparar Magpie y revisar SSH" })).toBeVisible();
  // El marco no se dibuja sobre una promesa fallida.
  await expect(page.locator('iframe[title^="Magpie"]')).toHaveCount(0);
  const before = asked.length;
  await alert.getByRole("button", { name: "Reintentar" }).click();
  await expect.poll(() => asked.length, "el reintento pregunta otra vez").toBeGreaterThan(before);
  await expect(alert).toBeVisible();
  await page.getByLabel("Host de los modelos").selectOption("local");
  await expect(
    page.frameLocator('iframe[title^="Magpie"]').locator("#view-usage"),
  ).toHaveJSProperty("hidden", false);
});

test("cada pestaña abre su pantalla original del panel nativo", async ({ page }) => {
  // Una por una: el contenedor tiene que preparar el panel del host para TODAS las
  // pantallas de la UI original que esta sección ofrece, no sólo Agents y Uso. Una
  // pantalla rechazada por el contenedor se ve aquí como la alerta real, así que este
  // caso falla en cuanto una pestaña deja de poder montar su panel.
  test.setTimeout(120_000);
  await signIn(page, "/?section=models&view=agents");
  for (const [id, label] of views) {
    if (label === "Cuentas") continue; // la misma pantalla nativa que Catálogo
    await page.goto(`/?section=models&view=${id}`);
    await expect(page).toHaveURL(new RegExp(`section=models&view=${id}`));
    await expect(
      page.getByRole("alert"),
      `la pestaña ${label} no pudo montar su panel`,
    ).toHaveCount(0);
    const frame = page.frameLocator('iframe[title^="Magpie"]');
    await expect(
      frame.locator(`#view-${nativeViews[id]}`),
      `la pestaña ${label} no llegó a la pantalla nativa ${nativeViews[id]}`,
    ).toHaveJSProperty("hidden", false);
  }
});

test("Abrir en pestaña entrega el mismo panel nativo en una pestaña nueva", async ({ page, context }) => {
  await signIn(page, "/?section=models&view=usage");
  const [tab] = await Promise.all([
    context.waitForEvent("page"),
    page.getByRole("button", { name: "Abrir en pestaña" }).click(),
  ]);
  await expect(tab.locator("#nav button[data-view]").first()).toBeVisible();
  await expect(tab.locator("#view-usage")).toHaveJSProperty("hidden", false);
  // La pestaña nueva sirve el panel del host, no otra copia de Workbench.
  expect(tab.url().replace(/\/$/, "")).not.toBe(process.env.E2E_BASE_URL);
  await tab.close();
});
