import { expect, request, test, type APIRequestContext, type FrameLocator, type Page } from "@playwright/test";
import { BASE, requireRelease, signIn, watchConsole, type ConsoleGuard } from "./helpers";

// MO-06: gateway y LAN. Todo ocurre en la instalación real (Workbench + su Magpie
// ORIGINAL enmarcado por host) y en su entrada de máquina (`/v1`), la que usa un
// ejecutor que no es este navegador:
//
//   1. Sin compartir (estado de una instalación nueva) el panel nativo ni ofrece
//      claves, y el ejecutor no autoriza: el gateway no sirve a otra máquina.
//   2. Compartir explícitamente en Ajustes → Red y uso compartido habilita el
//      panel de claves; una clave con nombre se crea en la UI original.
//   3. Un ejecutor nuevo (proceso aparte, sin cookies, con su User-Agent) usa esa
//      clave contra `/v1`; su llamada queda registrada y se ve en «Recent calls»
//      del mismo panel nativo.
//   4. Negativos: la cookie del navegador no autoriza inferencia, una clave
//      inventada no sirve, y volver a apagar el compartir corta a la misma clave
//      habilitada. Recuperación: reactivar el compartir y la clave vuelven a servir.
//
// Sin mock, sin route.fulfill y sin proveedor: el consumidor positivo responde con
// el catálogo real (vacío sin cuentas) y la llamada del ejecutor es la que el
// gateway registra de verdad — un modelo que ninguna cuenta sirve no se convierte
// en éxito, se convierte en una llamada fallida visible.
//
//   E2E_BASE_URL=http://127.0.0.1:19190 E2E_ACCESS_TOKEN_FILE=.../state/access.token \
//     bun run test tests/e2e/models-gateway-lan-release.spec.ts

let guard: ConsoleGuard | null = null;

test.use({ ignoreHTTPSErrors: process.env.E2E_TLS_SELF_SIGNED === "1" });

test.beforeAll(requireRelease);

test.beforeEach(({ page }) => {
  guard = watchConsole(page);
  // Los negativos de esta prueba piden /v1 sin autoridad a propósito.
  guard.expected.push("/v1/");
});

test.afterEach(() => {
  expect(guard?.errors ?? [], "consola del navegador").toEqual([]);
});

/** Pestaña «Claves y LAN» de Modelos: el panel ORIGINAL de Magpie del host local. */
async function gatewayPanel(page: Page, origin: string): Promise<FrameLocator> {
  await page.goto(origin + "/?section=models&view=keys");
  const panel = page.frameLocator('iframe[title^="Magpie ·"]');
  await expect(panel.locator("#gatewayKeysBlock")).toBeAttached({ timeout: 30_000 });
  return panel;
}

/** Compartir en red local es la regla nativa que habilita las claves y, en esta
 *  instalación, la que autoriza a un ejecutor de otra máquina contra `/v1`. */
async function sharingState(panel: FrameLocator) {
  await panel.locator("#prefs").click();
  await panel.locator("#setTab-network").click();
  const option = panel.locator("#lanList > .row.pref").first().locator(".segs button.opt").last();
  await expect(option).toBeAttached();
  return (await option.getAttribute("aria-pressed")) === "true";
}

async function setSharing(panel: FrameLocator, on: boolean) {
  await panel.locator("#prefs").click();
  await panel.locator("#setTab-network").click();
  const option = panel.locator("#lanList > .row.pref").first().locator(".segs button.opt").last();
  await expect(option).toBeAttached();
  const off = panel.locator("#lanList > .row.pref").first().locator(".segs button.opt").first();
  if ((await option.getAttribute("aria-pressed")) !== String(on)) {
    await (on ? option : off).click();
  }
  await expect(option).toHaveAttribute("aria-pressed", String(on));
  await panel.locator('button[data-view="gateway"]').first().click();
  // La regla nativa: sin compartir el panel no ofrece claves en absoluto.
  await expect(panel.locator("#gatewayKeysBlock"))[on ? "toBeVisible" : "toBeHidden"]({ timeout: 30_000 });
}

function keyRow(panel: FrameLocator, name: string) {
  return panel.locator("#gatewayKeys .acc").filter({ hasText: name });
}

/** Crear clave con nombre en la UI original; el secreto viaja una sola vez y el
 *  producto no guarda copia en el navegador. */
async function createKey(page: Page, panel: FrameLocator, name: string) {
  await panel.locator("#addGatewayKey").click();
  await panel.locator("#gatewayKeys .kf input").first().fill(name);
  const [response] = await Promise.all([
    page.waitForResponse((candidate) => candidate.url().endsWith("/api/caller-keys/add-key")),
    panel.locator("#gatewayKeys .kb button.primary").click(),
  ]);
  expect(response.status()).toBe(200);
  const body = (await response.json()) as { secret?: string };
  await expect(keyRow(panel, name)).toBeVisible();
  return body.secret ?? "";
}

/** El ejecutor nuevo: un proceso aparte, sin cookies y con su propio nombre. */
function executor(): Promise<APIRequestContext> {
  return request.newContext({
    ignoreHTTPSErrors: process.env.E2E_TLS_SELF_SIGNED === "1",
    extraHTTPHeaders: { "User-Agent": "mo06-executor/1" },
  });
}

async function catalog(client: APIRequestContext, origin: string, key: string) {
  const response = await client.get(origin + "/v1/models", { headers: { Authorization: "Bearer " + key } });
  return response.status();
}

/** Una llamada de inferencia con la clave: sin cuenta que sirva el modelo el
 *  gateway la registra como la llamada fallida que es, nunca como éxito. */
async function inference(client: APIRequestContext, origin: string, key: string, model: string) {
  const response = await client.post(origin + "/v1/chat/completions", {
    headers: { Authorization: "Bearer " + key, "Content-Type": "application/json" },
    data: { model, messages: [{ role: "user", content: "mo06" }] },
  });
  return { status: response.status(), body: (await response.text()).slice(0, 300) };
}

test("gateway y LAN: compartir explícito, clave con nombre y la llamada del ejecutor en la UI", async ({ page }) => {
  const name = `MO06 ejecutor ${Date.now()}`;
  const model = `mo06-lan-${Date.now()}`;
  const machine = await executor();

  await signIn(page);
  const panel = await gatewayPanel(page, BASE!);
  const wasShared = await sharingState(panel);

  // 1. Sin compartir: el panel nativo no ofrece claves y el ejecutor no autoriza.
  await setSharing(panel, false);
  await expect(panel.locator("#gatewayKeysBlock")).toBeHidden();
  expect(await catalog(machine, BASE!, "sk-magpie-key-not-a-real-key-000000")).toBe(401);
  expect(await catalog(machine, BASE!, "")).toBe(401);

  // 2. Compartir explícito y clave con nombre desde la UI original.
  await setSharing(panel, true);
  const secret = await createKey(page, panel, name);
  expect(secret, "el secreto de la clave nueva").toMatch(/^sk-magpie-key-/);

  // 3. El ejecutor nuevo usa la clave: catálogo real, y su llamada se registra.
  expect(await catalog(machine, BASE!, secret)).toBe(200);
  expect(await catalog(machine, BASE!, secret + "-wrong")).toBe(401);
  await expect(panel.locator("#activity .call-item").filter({ hasText: model })).toHaveCount(0);
  const called = await inference(machine, BASE!, secret, model);
  expect(called.status, "sin cuenta que sirva el modelo no hay 200 inventado").toBeGreaterThanOrEqual(400);
  // La llamada, hecha con la clave, aparece en la UI original del mismo panel.
  await panel.locator('button[data-view="gateway"]').first().click();
  const call = panel.locator("#activity .call-item").filter({ hasText: model });
  await expect(call).toBeVisible({ timeout: 30_000 });
  await expect(call).toHaveClass(/bad/);
  await expect(call.locator(".a")).toHaveText("mo06-executor");

  // 4. La cookie del navegador no autoriza inferencia: sin clave, 401.
  const cookieOnly = await page.evaluate(async () => {
    const response = await fetch("/v1/models");
    return response.status;
  });
  expect(cookieOnly).toBe(401);

  // 5. Volver a apagar el compartir corta a la misma clave habilitada.
  await setSharing(panel, false);
  await expect(panel.locator("#gatewayKeysBlock")).toBeHidden();
  const refused = await catalog(machine, BASE!, secret);
  expect(refused, "sin compartir el gateway no sirve a otra máquina").toBe(403);

  // 6. Recuperación: compartir otra vez, revocar y reactivar la clave.
  await setSharing(panel, true);
  await expect(keyRow(panel, name)).toContainText("…");
  expect(await catalog(machine, BASE!, secret)).toBe(200);
  await keyRow(panel, name).locator("button.tick").click();
  await expect(panel.locator("#gatewayKeys .acc.off").filter({ hasText: name })).toBeVisible();
  expect(await catalog(machine, BASE!, secret)).toBe(401);
  await keyRow(panel, name).locator("button.tick").click();
  await expect(panel.locator("#gatewayKeys .acc.in-use").filter({ hasText: name })).toBeVisible();
  expect(await catalog(machine, BASE!, secret)).toBe(200);

  // 7. Persistencia: recargar el panel conserva clave y compartir.
  const reloaded = await gatewayPanel(page, BASE!);
  await expect(keyRow(reloaded, name)).toContainText("…");
  expect(await catalog(machine, BASE!, secret)).toBe(200);

  // Limpieza por la UI original: la clave de prueba se retira y el compartir vuelve
  // al estado en que estaba la instalación.
  await keyRow(reloaded, name).locator("button.text.quiet").last().click();
  await reloaded.locator("#modal .bar button.primary").click();
  await expect(keyRow(reloaded, name)).toHaveCount(0);
  await setSharing(reloaded, wasShared);
  if (!wasShared) await expect(reloaded.locator("#gatewayKeysBlock")).toBeHidden();
  await machine.dispose();
});
