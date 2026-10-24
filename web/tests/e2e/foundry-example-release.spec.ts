import { expect, test } from "@playwright/test";
import { requireRelease, signIn, watchConsole, type ConsoleGuard } from "./helpers";

// Foundry end to end in a real browser: theme sync with the shell and Foundry's guided
// example (it prepares a redacted conversation, a task and two cases in the Foundry store
// of the instance under test: no model is called). Use a trial instance, not a store you keep.
//
//   E2E_BASE_URL=http://127.0.0.1:19177 E2E_ACCESS_TOKEN_FILE=... bun run test:e2e foundry-example-release.spec.ts

let consoleGuard: ConsoleGuard;

test.beforeAll(requireRelease);

test.beforeEach(async ({ page }) => {
  consoleGuard = watchConsole(page);
});

test.afterEach(() => {
  expect(consoleGuard.errors, "consola del navegador").toEqual([]);
});

const frameOf = (page: import("@playwright/test").Page) => page.frameLocator('iframe[title="Foundry"]');

test("Foundry sigue el tema del shell sin recargar y arranca en él", async ({ page }) => {
  await page.emulateMedia({ colorScheme: "light" });
  await signIn(page, "/?section=foundry");
  await expect(frameOf(page).getByRole("heading", { name: "Resumen", exact: true })).toBeVisible({ timeout: 30_000 });
  const frameDark = () => page.frames().find((f) => f.url().includes("/foundry/"))!.evaluate(() => document.documentElement.classList.contains("dark"));
  const colour = () =>
    page.frames().find((f) => f.url().includes("/foundry/"))!.evaluate(() => getComputedStyle(document.body).backgroundColor);
  const shellColour = () => page.evaluate(() => getComputedStyle(document.body).backgroundColor);

  expect(await frameDark(), "el marco arranca en el tema claro del shell").toBe(false);
  expect(await colour(), "mismo fondo que el shell").toBe(await shellColour());

  await page.getByRole("button", { name: "Usar tema oscuro" }).click();
  await expect.poll(frameDark).toBe(true);
  expect(await colour(), "mismo fondo que el shell en oscuro").toBe(await shellColour());

  // A fresh load starts in the theme the shell is in.
  await page.reload();
  await expect(frameOf(page).getByRole("heading", { name: "Resumen", exact: true })).toBeVisible({ timeout: 30_000 });
  expect(await frameDark()).toBe(true);
  await page.getByRole("button", { name: "Usar tema claro" }).click();
  await expect.poll(frameDark).toBe(false);
});

test("el ejemplo guiado se prepara desde el marco y deja su conversación, tarea y casos", async ({ page }) => {
  test.setTimeout(120_000);
  await signIn(page, "/?section=foundry");
  const frame = frameOf(page);
  await expect(frame.getByRole("heading", { name: "Resumen", exact: true })).toBeVisible({ timeout: 30_000 });
  await frame.getByRole("button", { name: "Preparar ejemplo" }).click();
  // Done when every example job has finished (a store that already had the example runs it again).
  const settled = async () => {
    const body = await (await page.request.get("/foundry/api/state?compact=1")).json();
    const jobs = body.jobs.filter((job: { kind: string }) => job.kind === "example");
    return jobs.length > 0 && jobs.every((job: { status: string }) => job.status === "succeeded") && body.cases.length >= 2;
  };
  await expect.poll(settled, { timeout: 60_000 }).toBe(true);
  const state = await (await page.request.get("/foundry/api/state?compact=1")).json();
  // No model ran and nothing was adopted or promoted by itself.
  expect(state.config.auto_promote).toBe(false);
  expect(state.candidates).toHaveLength(0);

  await page.getByRole("navigation", { name: "Secciones de Foundry" }).getByRole("button", { name: "Conversaciones", exact: true }).click();
  await expect(frame.getByText("DATA_FILE", { exact: false }).first()).toBeVisible({ timeout: 20_000 });
  await page.getByRole("navigation", { name: "Secciones de Foundry" }).getByRole("button", { name: "Experimentos", exact: true }).click();
  await expect(frame.getByRole("heading", { level: 1, name: "Experimentos", exact: true })).toBeVisible();
});
