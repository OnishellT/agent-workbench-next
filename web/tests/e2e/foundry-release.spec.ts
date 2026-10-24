import { expect, test } from "@playwright/test";
import { requireRelease, signIn, watchConsole, type ConsoleGuard } from "./helpers";

// The Foundry section: the managed Conversation Foundry served behind the Workbench
// session and framed by the shell. Needs the real release (E2E_BASE_URL + token), no mock
// and no skip; the machine must have Python 3.11+, as the product does.
//
//   E2E_BASE_URL=http://127.0.0.1:19177 E2E_ACCESS_TOKEN_FILE=... bun run test:e2e foundry-release.spec.ts

let consoleGuard: ConsoleGuard;

test.beforeAll(requireRelease);

test.beforeEach(async ({ page }) => {
  consoleGuard = watchConsole(page);
});

test.afterEach(() => {
  expect(consoleGuard.errors, "consola del navegador").toEqual([]);
});

const frameOf = (page: import("@playwright/test").Page) => page.frameLocator('iframe[title="Foundry"]');

test("la sección abre Foundry enmarcado, sin segundo login ni token en el navegador", async ({ page }) => {
  await signIn(page, "/?section=foundry");
  await expect(page.getByRole("heading", { level: 1, name: "Foundry", exact: true })).toBeVisible();
  // Foundry starts on demand; its own first page renders inside the frame.
  await expect(frameOf(page).getByRole("heading", { name: "Resumen", exact: true })).toBeVisible({ timeout: 30_000 });
  await expect(page.getByText("Abriendo Foundry…")).toHaveCount(0);
  // The framed app runs without its standalone sidebar and without a token prompt.
  await expect(frameOf(page).getByText("Conecta tu sesión local")).toHaveCount(0);
  await expect(frameOf(page).locator("aside.sidebar")).toHaveCount(0);
  const frame = page.frames().find((candidate) => candidate.url().includes("/foundry/"));
  expect(frame, "el marco de Foundry").toBeTruthy();
  expect(await frame!.evaluate(() => sessionStorage.getItem("foundry-token") ?? ""), "Foundry no recibe su token en el navegador").toBe("");
  expect(await page.evaluate(() => JSON.stringify([...Object.entries(localStorage), ...Object.entries(sessionStorage)]))).not.toMatch(/token/i);
});

test("las pestañas del shell conducen la página de Foundry y su navegación se refleja en la URL", async ({ page }) => {
  await signIn(page, "/?section=foundry");
  await expect(frameOf(page).getByRole("heading", { name: "Resumen", exact: true })).toBeVisible({ timeout: 30_000 });

  await page.getByRole("button", { name: "Fuentes", exact: true }).click();
  await expect(page).toHaveURL(/section=foundry&view=sources/);
  await expect(frameOf(page).getByRole("heading", { name: "Fuentes conectadas", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Fuentes", exact: true })).toHaveAttribute("aria-current", "page");

  await page.getByRole("button", { name: "Experimentos", exact: true }).click();
  await expect(page).toHaveURL(/section=foundry&view=experiments/);
  await expect(page.getByRole("button", { name: "Experimentos", exact: true })).toHaveAttribute("aria-current", "page");

  // Navigation inside the app is reported to the shell (the frame's own page change).
  const frame = page.frames().find((candidate) => candidate.url().includes("/foundry/"))!;
  await frame.evaluate(() => window.parent.postMessage({ type: "foundry:navigated", view: "settings" }, location.origin));
  await expect(page).toHaveURL(/section=foundry&view=settings/);
  await expect(page.getByRole("button", { name: "Configuración", exact: true })).toHaveAttribute("aria-current", "page");
  // Messages from anywhere else do not move the shell.
  await page.evaluate(() => window.postMessage({ type: "foundry:navigated", view: "sources" }, location.origin));
  await page.waitForTimeout(300);
  await expect(page).toHaveURL(/view=settings/);

  // Back and reload keep the page the address names.
  await page.goBack();
  await expect(page).toHaveURL(/view=experiments/);
  await page.reload();
  await expect(page).toHaveURL(/view=experiments/);
  await expect(page.getByRole("button", { name: "Experimentos", exact: true })).toHaveAttribute("aria-current", "page");
});

test("Foundry sólo responde con la sesión de Workbench y expone su propio contrato", async ({ page, playwright, baseURL }) => {
  await signIn(page, "/?section=foundry");
  await expect(frameOf(page).getByRole("heading", { name: "Resumen", exact: true })).toBeVisible({ timeout: 30_000 });

  const state = await page.request.get("/foundry/api/state?compact=1");
  expect(state.status()).toBe(200);
  const body = await state.json();
  expect(body.config).toBeTruthy();
  expect(Array.isArray(body.sources)).toBe(true);
  expect(body.config.auto_promote, "ninguna promoción automática").toBe(false);

  // A client without the Workbench session reaches neither the UI nor the API.
  const anonymous = await playwright.request.newContext({ baseURL });
  try {
    for (const path of ["/foundry/", "/foundry/api/state", "/foundry/health"])
      expect((await anonymous.get(path)).status(), path).toBe(401);
  } finally {
    await anonymous.dispose();
  }
  // The browser's own Foundry token header never decides: the proxy supplies the real one.
  expect((await page.request.get("/foundry/api/state", { headers: { "X-Foundry-Token": "forged" } })).status()).toBe(200);
  // Only the UI entrypoints are served as files, never the package source.
  expect((await page.request.get("/foundry/foundry_loop/store.py")).status()).toBe(404);
});

test("la captura de Foundry es opt-in: una instalación nueva no registra fuentes sola", async ({ page }) => {
  await signIn(page, "/?section=foundry&view=sources");
  await expect(frameOf(page).getByRole("heading", { name: "Fuentes conectadas", exact: true })).toBeVisible({ timeout: 30_000 });
  const state = await (await page.request.get("/foundry/api/state?compact=1")).json();
  // Opening the section scanned nothing: sources exist only if somebody added them on purpose.
  expect(state.capture_status.errors).toBe(0);
  for (const source of state.sources) expect(source.path, "una fuente la añadió alguien").toBeTruthy();
});

test("el enlace antiguo de Aprendizajes abre la página de aprendizajes de Foundry", async ({ page }) => {
  await signIn(page, "/?section=learnings");
  await expect(page.getByRole("heading", { level: 1, name: "Foundry", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Aprendizajes", exact: true })).toHaveAttribute("aria-current", "page");
  await expect(frameOf(page).getByRole("heading", { level: 1 })).toBeVisible({ timeout: 30_000 });
});

test("en móvil el marco cabe en la pantalla sin desbordar la página", async ({ page }) => {
  await signIn(page, "/?section=foundry");
  await page.setViewportSize({ width: 390, height: 800 });
  await page.reload();
  await expect(frameOf(page).getByRole("heading", { name: "Resumen", exact: true })).toBeVisible({ timeout: 30_000 });
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(overflow, "desbordamiento horizontal de la página").toBeLessThanOrEqual(0);
  const box = await page.locator('iframe[title="Foundry"]').boundingBox();
  expect(box!.width).toBeLessThanOrEqual(390);
});
