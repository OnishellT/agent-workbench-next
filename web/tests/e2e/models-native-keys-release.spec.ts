import { expect, request, test, type APIRequestContext, type FrameLocator, type Page } from "@playwright/test";
import { readFileSync } from "node:fs";
import { BASE, TOKEN, requireRelease, signIn, watchConsole, type ConsoleGuard } from "./helpers";

// MO-07: claves nativas de Magpie — crear, renombrar, revocar y comprobar consumidores.
// Todo ocurre en el panel ORIGINAL de Magpie que la instalación enmarca por host (pestaña
// «Claves y LAN» de Modelos) y en la entrada de máquina de esa misma instalación
// (/v1/models), que valida la clave contra la lista nativa del Magpie local antes de
// proxyear. Nada se simula: sin route.fulfill, sin mock y sin proveedor; el consumidor
// positivo responde con el catálogo real (vacío sin cuentas), suficiente para probar
// autoridad, aislamiento y revocación.
//
// La variante «otro host» (host SSH con su Magpie propio) necesita la fixture SSH de los
// cortes de host; aquí se prueba el aislamiento entre instalaciones («otro perfil»), el
// mismo invariante observable: la clave de un Magpie no autoriza a otro, y la entrada de
// máquina de una instalación sólo sirve al Magpie local.
//
//   E2E_BASE_URL=http://127.0.0.1:19130 E2E_ACCESS_TOKEN_FILE=.../state/access.token \
//   E2E_SECOND_BASE_URL=http://127.0.0.1:19131 E2E_SECOND_ACCESS_TOKEN_FILE=.../state-b/access.token \
//     bun run test tests/e2e/models-native-keys-release.spec.ts

const SECOND = process.env.E2E_SECOND_BASE_URL;
const SECOND_TOKEN =
  process.env.E2E_SECOND_ACCESS_TOKEN ??
  (process.env.E2E_SECOND_ACCESS_TOKEN_FILE
    ? readFileSync(process.env.E2E_SECOND_ACCESS_TOKEN_FILE, "utf8").trim()
    : undefined);

let guard: ConsoleGuard | null = null;

test.beforeAll(() => {
  requireRelease();
  if (!SECOND || !SECOND_TOKEN)
    throw new Error(
      "Dos instalaciones reales con su token hacen falta: E2E_BASE_URL/E2E_ACCESS_TOKEN y E2E_SECOND_BASE_URL/E2E_SECOND_ACCESS_TOKEN (o su *_FILE).",
    );
  if (new URL(BASE!).origin === new URL(SECOND).origin)
    throw new Error("La segunda instalación debe tener otro origen.");
});

test.beforeEach(({ page }) => {
  guard = watchConsole(page);
  // Los negativos de esta prueba piden /v1/models sin autoridad a propósito.
  guard.expected.push("/v1/models");
});

test.afterEach(() => {
  expect(guard?.errors ?? [], "consola del navegador").toEqual([]);
});

async function unlock(page: Page, origin: string, token: string) {
  await page.goto(origin + "/");
  const form = page.locator("#access-token");
  const shell = page.getByRole("navigation", { name: "Navegación principal", exact: true });
  await form.or(shell).first().waitFor();
  if (await form.isVisible().catch(() => false)) {
    await form.fill(token);
    await page.getByRole("button", { name: "Entrar", exact: true }).click();
  }
  await expect(shell).toBeVisible();
}

/** Pestaña «Claves y LAN» de Modelos: el panel ORIGINAL de Magpie del host local. */
async function keysPanel(page: Page, origin: string): Promise<FrameLocator> {
  await page.goto(origin + "/?section=models&view=keys");
  const panel = page.frameLocator('iframe[title^="Magpie ·"]');
  await expect(panel.locator("#gatewayKeysBlock")).toBeAttached({ timeout: 30_000 });
  return panel;
}

/** Compartir en red local es la regla nativa que habilita las claves; el interruptor se
 *  lee antes de tocarlo, así que una instalación que ya comparte no falla. */
async function sharingOn(panel: FrameLocator) {
  // La regla nativa: sin compartir, el panel no ofrece claves en absoluto.
  const hidden = await panel.locator("#gatewayKeysBlock").isHidden();
  await panel.locator("#prefs").click();
  await panel.locator("#setTab-network").click();
  const option = panel.locator("#lanList > .row.pref").first().locator(".segs button.opt").last();
  await expect(option).toBeAttached();
  if ((await option.getAttribute("aria-pressed")) !== "true") {
    expect(hidden, "las claves se ocultan mientras no se comparte").toBe(true);
    await option.click();
    await expect(option).toHaveAttribute("aria-pressed", "true");
  }
  await panel.locator('button[data-view="gateway"]').first().click();
  await expect(panel.locator("#gatewayKeysBlock")).toBeVisible();
}

function keyRow(panel: FrameLocator, name: string) {
  return panel.locator("#gatewayKeys .acc").filter({ hasText: name });
}

/** Crear clave con nombre en la UI original; el secreto viaja una sola vez en la
 *  respuesta nativa de add-key y el producto no guarda copia en el navegador. */
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

/** Consumidor de máquina de esa instalación, llamado como lo llama un cliente real. */
async function consumer(page: Page, origin: string, secret: string) {
  return page.evaluate(
    async ([target, key]) => {
      const response = await fetch(target + "/v1/models", {
        headers: key ? { Authorization: "Bearer " + key } : {},
      });
      return { status: response.status, body: (await response.text()).slice(0, 200) };
    },
    [origin, secret] as const,
  );
}

/** Cliente de máquina sin cookies: lo que un ejecutor externo manda de verdad. */
function machine(): Promise<APIRequestContext> {
  return request.newContext({ ignoreHTTPSErrors: process.env.E2E_TLS_SELF_SIGNED === "1" });
}

async function removeKey(page: Page, origin: string, token: string, name: string) {
  await unlock(page, origin, token);
  const panel = await keysPanel(page, origin);
  await keyRow(panel, name).locator("button.text.quiet").last().click();
  await panel.locator("#modal .bar button.primary").click();
  await expect(keyRow(panel, name)).toHaveCount(0);
}

test("claves nativas: crear, renombrar, revocar y servir al consumidor del mismo host", async ({ page }) => {
  const name = `MO07 claves ${Date.now()}`;
  const renamed = name + " renombrada";
  await signIn(page);
  let panel = await keysPanel(page, BASE!);
  await sharingOn(panel);

  const secret = await createKey(page, panel, name);
  expect(secret, "el secreto de la clave nueva").toMatch(/^sk-magpie-key-/);
  // Secretos no se listan: la fila y la lista nativa muestran sólo el enmascarado.
  await expect(keyRow(panel, name)).toContainText("…");
  await expect(keyRow(panel, name)).not.toContainText(secret);
  const listed = await panel.locator("#gatewayKeys").evaluate(async () => {
    const response = await fetch("/api/caller-keys");
    return (await response.json()) as { keys: Record<string, unknown>[] };
  });
  expect(listed.keys.some((row) => row.secret)).toBe(false);
  // Ni la SPA ni las APIs de la instalación publican el secreto.
  for (const path of ["/api/bootstrap", "/api/models/state", "/api/diagnostics"]) {
    const response = await page.request.get(new URL(path, BASE!).href);
    expect(await response.text(), path).not.toContain(secret);
  }
  expect(await page.locator("body").innerHTML()).not.toContain(secret);
  // La clave de máquina no da sesión de operador: sólo autoriza inferencia.
  const executor = await machine();
  expect(
    (await executor.get(new URL("/api/bootstrap", BASE!).href, {
      headers: { Authorization: "Bearer " + secret },
    })).status(),
  ).toBe(401);

  // Consumidor: la clave sirve, la cookie del navegador no y una inventada tampoco.
  expect(await consumer(page, BASE!, secret)).toMatchObject({ status: 200 });
  expect((await consumer(page, BASE!, "")).status).toBe(401);
  expect((await consumer(page, BASE!, "sk-magpie-key-not-a-real-key-000000")).status).toBe(401);

  // Renombrar desde la UI original conserva el consumidor y persiste al recargar.
  await keyRow(panel, name).locator("button.n.rename").click();
  await panel.locator("#gatewayKeys input.rename-in").fill(renamed);
  await panel.locator("#gatewayKeys input.rename-in").press("Enter");
  await expect(keyRow(panel, renamed)).toBeVisible();
  expect((await consumer(page, BASE!, secret)).status).toBe(200);
  panel = await keysPanel(page, BASE!);
  await expect(keyRow(panel, renamed)).toContainText("…");

  // Revocar (off-key) corta al consumidor; reactivar lo recupera.
  await keyRow(panel, renamed).locator("button.tick").click();
  await expect(panel.locator("#gatewayKeys .acc.off").filter({ hasText: renamed })).toBeVisible();
  expect((await consumer(page, BASE!, secret)).status).toBe(401);
  await keyRow(panel, renamed).locator("button.tick").click();
  await expect(panel.locator("#gatewayKeys .acc.in-use").filter({ hasText: renamed })).toBeVisible();
  expect((await consumer(page, BASE!, secret)).status).toBe(200);

  // Otro perfil: la clave de esta instalación no autoriza a la segunda, y al revés.
  await unlock(page, SECOND!, SECOND_TOKEN!);
  const otherPanel = await keysPanel(page, SECOND!);
  await sharingOn(otherPanel);
  const otherName = `MO07 otra ${Date.now()}`;
  const otherSecret = await createKey(page, otherPanel, otherName);
  expect((await consumer(page, SECOND!, otherSecret)).status).toBe(200);
  expect(
    (await executor.get(new URL("/v1/models", BASE!).href, {
      headers: { Authorization: "Bearer " + otherSecret },
    })).status(),
  ).toBe(401);
  expect(
    (await executor.get(new URL("/v1/models", SECOND!).href, {
      headers: { Authorization: "Bearer " + secret },
    })).status(),
  ).toBe(401);
  await executor.dispose();

  // Limpieza: las dos claves de prueba se retiran por la UI original.
  await removeKey(page, SECOND!, SECOND_TOKEN!, otherName);
  await removeKey(page, BASE!, TOKEN!, renamed);
});
