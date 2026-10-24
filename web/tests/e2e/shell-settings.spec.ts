import { expect, test, type Response } from "@playwright/test";
import {
  cardByHeading,
  requiredProjectDir,
  requireRelease,
  restoreSettings,
  serverSettings,
  signIn,
  watchConsole,
  type ConsoleGuard,
} from "./helpers";

// M02 settings (PL-06/PL-07/PL-08) against the real release: every write is answered by
// the installation itself and nothing flips locally. Needs the real binary:
// E2E_BASE_URL + E2E_ACCESS_TOKEN, no mock, no skip.
// The project case also needs E2E_PROJECT_DIR: an absolute path of a folder that really
// exists ON THE SERVER under test (the coordinator prepares it; the browser cannot make
// directories there). Without it the case fails loudly instead of inventing a path.
//
//   E2E_BASE_URL=https://workbench.example E2E_ACCESS_TOKEN=... E2E_PROJECT_DIR=/srv/e2e-proyecto \
//     bun run test tests/e2e/shell-settings.spec.ts

let consoleGuard: ConsoleGuard;

test.beforeAll(requireRelease);

test.beforeEach(({ page }) => {
  consoleGuard = watchConsole(page);
});

test.afterEach(() => {
  expect(consoleGuard.errors, "consola del navegador").toEqual([]);
});

test("una carpeta ausente se rechaza y la carpeta preparada se añade y se quita", async ({ page }) => {
  await signIn(page);
  const before = await serverSettings(page);
  const fixture = requiredProjectDir();
  const found = before.projects ?? [];
  // The fixture must not be configured before the case: a leftover from an aborted
  // run is removed first (and every original entry is restored at the end).
  const existing = found.filter((project) => project.path !== fixture);
  const expectedName = fixture.split("/").filter(Boolean).pop();
  if (existing.length !== found.length) await restoreSettings(page, { projects: existing });

  await page.goto("/?section=settings");
  const card = cardByHeading(page, "Proyectos");
  const pathField = card.getByLabel("Ruta de la carpeta");
  const add = card.getByRole("button", { name: "Añadir", exact: true });
  const settingsPost = (result: Response) =>
    result.url().endsWith("/api/settings") && result.request().method() === "POST";
  try {
    // Negative: the server, not the browser, decides whether a folder exists.
    const missing = `${fixture}/ausente-${Date.now()}`;
    consoleGuard.expected.push("/api/settings");
    const [rejected] = await Promise.all([
      page.waitForResponse(settingsPost),
      (async () => {
        await pathField.fill(missing);
        await add.click();
      })(),
    ]);
    expect(rejected.status()).toBe(400);
    await expect(page.getByRole("alert")).toBeVisible();
    expect((await serverSettings(page)).projects ?? [], "la lista no cambió").toEqual(existing);

    // Positive: the exact folder the coordinator prepared on the server.
    const [saved] = await Promise.all([
      page.waitForResponse(settingsPost),
      (async () => {
        await pathField.fill(fixture);
        await add.click();
      })(),
    ]);
    expect(saved.status()).toBe(200);
    const receipt = await saved.json();
    const stored: { path: string; name: string }[] = receipt.settings?.projects ?? [];
    const entry = stored.find((project) => project.path === fixture);
    expect(entry, `${fixture} must be stored exactly as sent`).toBeTruthy();
    expect(entry!.name).toBe(expectedName);
    expect(await pathField.inputValue(), "el campo se limpia solo tras confirmar").toBe("");

    await page.reload();
    await expect(cardByHeading(page, "Proyectos").getByText(fixture, { exact: true })).toBeVisible();
    await expect(
      cardByHeading(page, "Proyectos").getByText(entry!.name, { exact: true }),
      "la carpeta añadida sobrevive la recarga con su nombre derivado",
    ).toBeVisible();

    const [removed] = await Promise.all([
      page.waitForResponse(settingsPost),
      cardByHeading(page, "Proyectos")
        .getByRole("button", { name: `Quitar: ${fixture}`, exact: true })
        .click(),
    ]);
    expect(removed.status()).toBe(200);
    expect((await serverSettings(page)).projects ?? []).toEqual(existing);
  } finally {
    await restoreSettings(page, { projects: found });
  }
});
