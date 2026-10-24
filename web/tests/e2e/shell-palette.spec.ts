import { expect, test } from "@playwright/test";
import { requireRelease, signIn, watchConsole, type ConsoleGuard } from "./helpers";

// UI-02 palette/shortcuts and the real light-theme contrast check. Needs the real
// release: E2E_BASE_URL + E2E_ACCESS_TOKEN, no mock, no skip.
//
//   E2E_BASE_URL=https://workbench.example E2E_ACCESS_TOKEN=... bun run test tests/e2e/shell-palette.spec.ts

let consoleGuard: ConsoleGuard;

test.beforeAll(requireRelease);

test.beforeEach(async ({ page }) => {
  consoleGuard = watchConsole(page);
  await page.emulateMedia({ colorScheme: "light" });
});

test.afterEach(() => {
  expect(consoleGuard.errors, "consola del navegador").toEqual([]);
});

test("Ctrl+K busca sin acentos y abre el resultado", async ({ page }) => {
  await signIn(page);
  await page.goto("/?section=conversations");
  // The shortcut belongs to the mounted shell: without waiting for it, the first press
  // can land before the listener exists and the palette never opens (a spec race, not a
  // screen defect).
  await expect(page.getByRole("button", { name: /Ir a…/ })).toBeVisible();
  await page.keyboard.press("Control+k");
  const palette = page.getByRole("dialog");
  await expect(palette).toBeVisible();
  const search = palette.getByRole("combobox", { name: "Buscar sección o ajuste" });
  await search.fill("catalogo");
  const option = palette.getByRole("option", { name: /Catálogo/ });
  await expect(option, "la búsqueda ignora el acento").toBeVisible();
  await page.keyboard.press("Enter");
  await expect(palette).toBeHidden();
  await expect(page).toHaveURL(/section=models&view=catalog/);
await expect(
    page.frameLocator('iframe[title^="Magpie"]').locator("#view-providers"),
  ).toHaveJSProperty("hidden", false);
});

test("Escape cierra la paleta y devuelve el foco al botón que la abrió", async ({ page }) => {
  await signIn(page);
  const trigger = page.getByRole("button", { name: /Ir a…/ });
  await trigger.click();
  await expect(page.getByRole("dialog")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toBeHidden();
  await expect(trigger).toBeFocused();
});

test("los atajos respetan la edición de un campo", async ({ page }) => {
  await signIn(page);
  const settingsWrites: string[] = [];
  page.on("request", (request) => {
    if (request.method() === "POST" && request.url().endsWith("/api/settings"))
      settingsWrites.push(request.url());
  });
  await page.goto("/?section=settings");
  const field = page.getByLabel("Ruta de la carpeta");
  await field.click();
  // "/" is a shortcut only where a search field exists: here it stays a character.
  await field.pressSequentially("/tmp/e2e-atajo");
  await expect(field).toHaveValue("/tmp/e2e-atajo");
  await expect(field).toBeFocused();
  await expect(page.getByRole("dialog")).toBeHidden();

  // Ctrl+K is global and must work while a field has focus, without eating the value.
  await page.keyboard.press("Control+k");
  await expect(page.getByRole("dialog")).toBeVisible();
  await expect(page.getByRole("combobox", { name: "Buscar sección o ajuste" })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toBeHidden();
  await expect(field).toBeFocused();
  await expect(field).toHaveValue("/tmp/e2e-atajo");
  // Nothing was submitted: this case never prepared a folder, so no settings write.
  expect(settingsWrites).toEqual([]);
});

test("Cmd+K abre la paleta y el clic navega a la vista real, que sobrevive la recarga", async ({ page }) => {
  await signIn(page);
  // Same mount race as the Ctrl+K case: the listener belongs to the mounted shell.
  await expect(page.getByRole("button", { name: /Ir a…/ })).toBeVisible();
  await page.keyboard.press("Meta+k");
  const palette = page.getByRole("dialog");
  await expect(palette).toBeVisible();
  await palette.getByRole("combobox", { name: "Buscar sección o ajuste" }).fill("experimentos");
  // A view is labelled with its own section, never a fixed group for every entry
  // (the wrong "Instalación" this case pins): Foundry's page belongs to Foundry.
  await expect(palette.getByRole("option", { name: /Experimentos/ })).toContainText("Foundry");
  await palette.getByRole("option", { name: /Experimentos/ }).click();
  await expect(palette).toBeHidden();
  // The module owns its views: the address and the module's own tab must agree.
  await expect(page).toHaveURL(/section=foundry&view=experiments/);
  await expect(page.getByRole("heading", { level: 1, name: "Foundry" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Experimentos", exact: true })).toHaveAttribute("aria-current", "page");
  // Reload and deep link keep the destination the palette chose.
  await page.reload();
  await expect(page).toHaveURL(/section=foundry&view=experiments/);
  await expect(page.getByRole("button", { name: "Experimentos", exact: true })).toHaveAttribute("aria-current", "page");
});

test("sin coincidencias la paleta se recupera y no ofrece rutas retiradas", async ({ page }) => {
  await signIn(page);
  const trigger = page.getByRole("button", { name: /Ir a…/ });
  await expect(trigger).toBeVisible();
  await page.keyboard.press("Control+k");
  const palette = page.getByRole("dialog");
  const search = palette.getByRole("combobox", { name: "Buscar sección o ajuste" });
  await expect(palette).toBeVisible();
  // Atlas and Orbit are retired: neither their names nor their wording may offer a route.
  for (const retired of ["atlas", "consumo", "orbit"]) {
    await search.fill(retired);
    await expect(
      palette.getByRole("status"),
      `"${retired}" no debe ofrecer ninguna ruta`,
    ).toHaveText("No hay coincidencias con ese nombre.");
  }
  const route = page.url();
  await search.fill("qqqq");
  await page.keyboard.press("Enter");
  // Without a result there is nothing to open: the palette stays and the address holds.
  await expect(palette).toBeVisible();
  expect(page.url()).toBe(route);
  // Clearing the query recovers the default list; Escape returns focus to the opener.
  await search.fill("");
  await expect(palette.getByRole("option").first()).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(palette).toBeHidden();
  await expect(trigger).toBeFocused();
});

test("el texto secundario del tema claro cumple AA sobre las superficies reales", async ({ page }) => {
  await signIn(page);
  await expect(page.locator("html")).not.toHaveClass(/dark/);
  const measurements: { ratio: number; color: string; background: string; url: string }[] = [];
  // Superficies vigentes: el alias retirado hosts duplica Conexiones y no aporta otra.
  for (const route of ["/?section=settings", "/?section=connections", "/?section=models"]) {
    await page.goto(route);
    await expect(page.locator("#wb-content")).toBeVisible();
    measurements.push(
      ...(await page.evaluate(() => {
        const channel = (value: string) => {
          const parts = value.match(/[\d.]+/g);
          if (!parts || parts.length < 3) return null;
          if (parts.length > 3 && Number(parts[3]) < 0.9) return null; // not an opaque surface
          return parts.slice(0, 3).map(Number);
        };
        const luminance = (rgb: number[]) => {
          const linear = rgb.map((v) => {
            const c = v / 255;
            return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
          });
          return 0.2126 * linear[0] + 0.7152 * linear[1] + 0.0722 * linear[2];
        };
        const results: { ratio: number; color: string; background: string; url: string }[] = [];
        for (const element of document.querySelectorAll("#wb-content .text-muted-foreground")) {
          const color = getComputedStyle(element).color;
          let node: HTMLElement | null = element as HTMLElement;
          let background: string | null = null;
          while (node) {
            const candidate = getComputedStyle(node).backgroundColor;
            if (channel(candidate)) {
              background = candidate;
              break;
            }
            node = node.parentElement;
          }
          const fg = channel(color);
          const bg = background ? channel(background) : null;
          if (!fg || !bg) continue;
          const [high, low] = [luminance(fg), luminance(bg)].sort((a, b) => b - a);
          results.push({
            ratio: (high + 0.05) / (low + 0.05),
            color,
            background: background as string,
            url: location.href,
          });
        }
        return results;
      })),
    );
  }
  // Screens carry little secondary text now; the gate needs real samples, not a quota.
  expect(measurements.length, "no muted text was measured").toBeGreaterThan(0);
  const failures = measurements.filter((m) => m.ratio < 4.5);
  expect(
    failures.map((f) => `${f.url} ${f.color} on ${f.background} = ${f.ratio.toFixed(2)}`),
    "contraste AA del texto secundario",
  ).toEqual([]);
});

/** Independent second pass on the same rule: the destructive alert surface is only
 *  rendered by a real failure, so the case produces one through the project form. */
test("el texto de error también cumple AA", async ({ page }) => {
  await signIn(page);
  await page.goto("/?section=settings");
  const card = page.locator('[data-slot="card"]').filter({ has: page.getByRole("heading", { name: "Proyectos", exact: true }) });
  consoleGuard.expected.push("/api/settings");
  await card.getByLabel("Ruta de la carpeta").fill("/ruta/e2e-que-no-existe-12345");
  await card.getByRole("button", { name: "Añadir", exact: true }).click();
  const alert = page.getByRole("alert");
  await expect(alert).toBeVisible();
  const contrast = await alert.evaluate((element: Element) => {
    const parse = (value: string) => (value.match(/[\d.]+/g) || []).slice(0, 3).map(Number);
    const luminance = (rgb: number[]) => {
      const linear = rgb.map((v) => {
        const c = v / 255;
        return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
      });
      return 0.2126 * linear[0] + 0.7152 * linear[1] + 0.0722 * linear[2];
    };
    let node: HTMLElement | null = element as HTMLElement;
    let background: number[] = [255, 255, 255];
    while (node) {
      const candidate = parse(getComputedStyle(node).backgroundColor);
      const alpha = (getComputedStyle(node).backgroundColor.match(/[\d.]+/g) || [])[3];
      if (candidate.length === 3 && (alpha === undefined || Number(alpha) > 0.9)) {
        background = candidate;
        break;
      }
      node = node.parentElement;
    }
    const fg = parse(getComputedStyle(element).color);
    const [high, low] = [luminance(fg), luminance(background)].sort((a, b) => b - a);
    return { ratio: (high + 0.05) / (low + 0.05), fg, background };
  });
  expect(contrast.ratio, JSON.stringify(contrast)).toBeGreaterThanOrEqual(4.5);
});
