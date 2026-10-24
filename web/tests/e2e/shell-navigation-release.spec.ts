import { expect, test } from "@playwright/test";
import { requireRelease, signIn, watchConsole, type ConsoleGuard } from "./helpers";

// UI-01 shell navigation: sections and their views, back/forward, deep link/reload and the
// host a cross-section link carries. Real release only: E2E_BASE_URL + E2E_ACCESS_TOKEN,
// no mock, no route fulfill, no skip.
//
//   E2E_BASE_URL=http://127.0.0.1:19054 E2E_ACCESS_TOKEN=... \
//     bun run test tests/e2e/shell-navigation-release.spec.ts

let guard: ConsoleGuard | null = null;

test.beforeAll(requireRelease);

test.beforeEach(({ page }) => {
  guard = watchConsole(page);
  // Slices still pending answer 501 where a screen reads them (remote history); the
  // section reports it as its own alert. An expected answer of this release, kept out of
  // the shell console check.
  guard.expected.push("/history");
  // A host that only exists in the address has no panel: its refusal is the point of the
  // negative case, not a shell failure.
  guard.expected.push("/magpie/open");
});

test.afterEach(() => {
  expect(guard?.errors ?? [], "consola del navegador").toEqual([]);
});

/** Sidebar entry and the heading it must open: the shell's own contract, not a module's. */
const sections: [string, string][] = [
  ["conversations", "Conversaciones"],
  ["tasks", "Tareas"],
  ["foundry", "Foundry"],
  ["sim", "SIM"],
  ["models", "Configuración de agentes"],
  ["connections", "Conexiones"],
  ["settings", "Ajustes"],
];

test("la barra abre cada sección vigente y marca la actual", async ({ page }) => {
  await signIn(page, "/?section=conversations");
  const nav = page.getByRole("navigation", { name: "Navegación principal" });
  for (const [id, label] of sections) {
    const entry = nav.getByRole("button", { name: label, exact: true });
    await entry.click();
    await expect(page).toHaveURL(new RegExp(`section=${id}(&|$)`));
    await expect(page.locator("#wb-content h1")).toHaveText(label);
    await expect(entry).toHaveAttribute("aria-current", "page");
  }
});

test("la vista del módulo viaja en la URL y sobrevive recarga, Atrás y Adelante", async ({ page }) => {
  await signIn(page, "/?section=conversations");
  await page.getByRole("navigation", { name: "Navegación principal" }).getByRole("button", { name: "Conversaciones", exact: true }).click();
  const tabs = page.getByRole("navigation", { name: "Secciones de Conversaciones" });
  await tabs.getByRole("button", { name: "Fuentes", exact: true }).click();
  await expect(page).toHaveURL(/section=conversations&view=sources/);
  await expect(tabs.getByRole("button", { name: "Fuentes", exact: true })).toHaveAttribute("aria-current", "page");

  // Reload is the deep link the operator pasted: same section, same view.
  await page.reload();
  await expect(tabs.getByRole("button", { name: "Fuentes", exact: true })).toHaveAttribute("aria-current", "page");

  // One step back leaves the view, another one forward restores it.
  await page.goBack();
  await expect(page).toHaveURL(/section=conversations&view=conversations/);
  await expect(tabs.getByRole("button", { name: "Conversaciones", exact: true })).toHaveAttribute("aria-current", "page");
  await page.goForward();
  await expect(page).toHaveURL(/section=conversations&view=sources/);
  await expect(tabs.getByRole("button", { name: "Fuentes", exact: true })).toHaveAttribute("aria-current", "page");
});

test("repetir el mismo destino no añade historial: un Atrás vuelve a la sección anterior", async ({ page }) => {
  await signIn(page, "/?section=conversations");
  const nav = page.getByRole("navigation", { name: "Navegación principal" });
  const hosts = nav.getByRole("button", { name: "Conexiones", exact: true });
  await hosts.click();
  await expect(page).toHaveURL(/section=connections/);
  // Same destination again: one entry per navigation, as the frozen shell did. A second
  // pushState made this Back land on the same screen and look broken.
  await hosts.click();
  await expect(page).toHaveURL(/section=connections/);
  await page.goBack();
  await expect(page).toHaveURL(/section=conversations/);
  await expect(page.locator("#wb-content h1")).toHaveText("Conversaciones");
});

test("una ruta desconocida no abre otra sección ni el host de la dirección", async ({ page }) => {
  await signIn(page);
  await page.goto("/?section=atlantis&host=otro-equipo");
  await expect(page.locator("#wb-content h1")).toHaveText("Sección no encontrada");
  // No module rendered and no panel requested for the host left in the address.
  await expect(page.getByLabel("Host de los modelos")).toHaveCount(0);
  expect(new URL(page.url()).searchParams.get("section")).toBe("atlantis");

  // An unknown view keeps the section's first tab, the way readRoute resolved it.
  await page.goto("/?section=models&view=nope");
  await expect(page.getByLabel("Host de los modelos")).toHaveValue("local");
  await expect(
    page.frameLocator('iframe[title^="Magpie"]').locator("#view-providers"),
  ).toHaveJSProperty("hidden", false);

  // The retired Orbit bookmark lands on SIM, the app that replaced it, without reviving it.
  await page.goto("/?section=orbit");
  await expect(page.locator("#wb-content h1")).toHaveText("SIM");
  await expect(
    page.getByRole("navigation", { name: "Navegación principal" }).getByRole("button", { name: "SIM", exact: true }),
  ).toHaveAttribute("aria-current", "page");
});

test("el uso de Este servidor no hereda el host que quedó en la dirección", async ({ page }) => {
  await signIn(page, "/?section=models&host=otro-equipo");
  // Deep link and reload keep the host the address names.
  await expect(page.getByLabel("Host de los modelos")).toHaveValue("otro-equipo");
  await page.reload();
  await expect(page.getByLabel("Host de los modelos")).toHaveValue("otro-equipo");

  // The local card of Equipos has no host: its usage link must not open the host left by
  // the previous section.
  await page.getByRole("navigation", { name: "Navegación principal" }).getByRole("button", { name: "Conexiones", exact: true }).click();
  await expect(page).toHaveURL(/section=connections&host=otro-equipo/);
  await page.getByRole("button", { name: "Ver uso por equipo", exact: true }).click();
  await expect(page).toHaveURL(/section=models&view=usage/);
  expect(new URL(page.url()).searchParams.get("host")).toBeNull();
  await expect(page.getByLabel("Host de los modelos")).toHaveValue("local");
});
