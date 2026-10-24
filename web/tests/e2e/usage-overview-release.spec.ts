import { expect, test } from "@playwright/test";
import { requireRelease, signIn, watchConsole, type ConsoleGuard } from "./helpers";

// Retirada del Uso global: la pantalla propia desapareció y su API responde 404. El uso
// de cada host se ve en su Magpie (Modelos → Uso), a donde apuntan el marcador antiguo
// y el enlace de Conexiones. Release real, sin mock. Sin skip: cada caso falla cuando
// el flujo que describe no ocurre.
//
//   E2E_BASE_URL=http://127.0.0.1:5184 E2E_ACCESS_TOKEN=... \
//     bun run test tests/e2e/usage-overview-release.spec.ts

let guard: ConsoleGuard | null = null;

test.beforeAll(requireRelease);

test.beforeEach(({ page }) => {
  guard = watchConsole(page);
  guard.expected.push("/api/hosts/");
});

test.afterEach(() => {
  expect(guard?.errors ?? [], "consola del navegador").toEqual([]);
});

test("la API retirada de Uso global responde 404", async ({ page }) => {
  await signIn(page);
  for (const method of ["GET", "POST"]) {
    const response = await page.request.fetch("/api/usage/overview", { method });
    expect(response.status(), `${method} /api/usage/overview`).toBe(404);
    expect(await response.text(), "sin HTML de la shell").not.toContain("<html");
  }
});

test("siete secciones sin Uso global; ?section=usage cae en Modelos → Uso", async ({
  page,
}) => {
  await signIn(page);
  const nav = page.getByRole("navigation", { name: "Navegación principal" });
  expect(await nav.getByRole("button").allTextContents()).toEqual([
    "Conversaciones",
    "Tareas",
    "Aprendizajes",
    "SIM",
    "Configuración de agentes",
    "Conexiones",
    "Ajustes",
  ]);
  await expect(nav.getByRole("button", { name: "Uso global", exact: true })).toHaveCount(0);

  // El marcador retirado resuelve en el Uso del Magpie; la URL queda como vino.
  await page.goto("/?section=usage");
  await expect(page.getByLabel("Host de los modelos")).toBeVisible();
  await expect(
    page
      .getByRole("navigation", { name: "Navegación principal" })
      .getByRole("button", { name: "Configuración de agentes", exact: true }),
  ).toHaveAttribute("aria-current", "page");
  await expect(
    page.frameLocator('iframe[title^="Magpie"]').locator("#view-usage"),
  ).toHaveJSProperty("hidden", false);
  expect(new URL(page.url()).searchParams.get("section")).toBe("usage");
  await expect(page.getByRole("table", { name: "Uso por host" })).toHaveCount(0);
  await page.reload();
  await expect(page.getByLabel("Host de los modelos")).toBeVisible();
});

test("el uso por equipo se abre desde Conexiones en el Magpie", async ({ page }) => {
  await signIn(page, "/?section=connections");
  await expect(
    page.getByRole("heading", { name: "Equipos SSH · Magpie e historial" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Ver uso por equipo", exact: true }).click();
  await expect(page).toHaveURL(/section=models&view=usage/);
  await expect(page.getByLabel("Host de los modelos")).toBeVisible();
  await expect(
    page.frameLocator('iframe[title^="Magpie"]').locator("#view-usage"),
  ).toHaveJSProperty("hidden", false);
});
