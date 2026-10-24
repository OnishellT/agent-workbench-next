import { expect, test, type Page } from "@playwright/test";
import { requireRelease, signIn, watchConsole, type ConsoleGuard } from "./helpers";

// UI-03 common theme (owner CerrarTemaShellUI03). Real light↔dark switching, durable
// preference, bodies/portals of the shell, invalid stored values and real contrast.
// Needs the real release: E2E_BASE_URL + E2E_ACCESS_TOKEN, no mock, no skip.
//
//   E2E_BASE_URL=http://127.0.0.1:19056 E2E_ACCESS_TOKEN=... bun run test tests/e2e/shell-theme-release.spec.ts
//
// The app's only select is the native one (no Base UI Select exists in web/src), so its
// "portal" contract is the OS popup following the document's color-scheme, asserted here.

let consoleGuard: ConsoleGuard;

test.beforeAll(requireRelease);

test.beforeEach(async ({ page }) => {
  consoleGuard = watchConsole(page);
  await page.emulateMedia({ colorScheme: "light" });
});

test.afterEach(() => {
  expect(consoleGuard.errors, "consola del navegador").toEqual([]);
});

interface ThemeState {
  dark: boolean;
  scheme: string;
  stored: string | null;
  bodyBg: string;
  bodyFg: string;
  bodyLuminance: number;
  bodyTextLuminance: number;
}

/** State the user actually sees: class, resolved color-scheme, stored preference and the
 *  rendered body colours with their luminance (a light body is ~0.9, a dark one ~0.01). */
function rendered(page: Page): Promise<ThemeState> {
  return page.evaluate(() => {
    const parse = (value: string) => (value.match(/[\d.]+/g) || []).slice(0, 3).map(Number);
    const luminance = (rgb: number[]) =>
      rgb
        .map((v) => {
          const c = v / 255;
          return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
        })
        .reduce((sum, v, index) => sum + v * [0.2126, 0.7152, 0.0722][index], 0);
    const body = getComputedStyle(document.body);
    return {
      dark: document.documentElement.classList.contains("dark"),
      scheme: getComputedStyle(document.documentElement).colorScheme,
      stored: localStorage.getItem("workbench-theme"),
      bodyBg: body.backgroundColor,
      bodyFg: body.color,
      bodyLuminance: luminance(parse(body.backgroundColor)),
      bodyTextLuminance: luminance(parse(body.color)),
    };
  });
}

/** Waits for the rendered theme (not for the click) and returns the state it settled on. */
async function waitForTheme(page: Page, theme: "dark" | "light"): Promise<ThemeState> {
  await expect
    .poll(async () => (await rendered(page)).scheme, { message: `color-scheme ${theme}` })
    .toBe(theme);
  const state = await rendered(page);
  expect(state.dark, `clase .dark del documento (${theme})`).toBe(theme === "dark");
  expect(state.stored, `preferencia guardada (${theme})`).toBe(theme);
  return state;
}

async function useTheme(page: Page, theme: "dark" | "light") {
  // The control names the theme it will switch TO, so its label is the previous one.
  await page
    .getByRole("button", { name: theme === "dark" ? "Usar tema oscuro" : "Usar tema claro" })
    .click();
  return waitForTheme(page, theme);
}

interface Surface {
  text: string;
  selector: string;
  color: string;
  surface: string;
  luminance: number;
  ratio: number;
  inRoot: boolean;
}

/** WCAG contrast of every element matching `selector` on the rendered page: the text
 *  colour is composited over the surfaces it really sits on (tints included). */
function contrastRows(page: Page, selector: string): Promise<Surface[]> {
  return page.evaluate((sel) => {
    const parse = (value: string) => {
      const parts = (value.match(/[\d.]+/g) || []).map(Number);
      if (parts.length < 3) return null;
      return { rgb: parts.slice(0, 3), alpha: parts.length > 3 ? parts[3] : 1 };
    };
    const over = (top: { rgb: number[]; alpha: number }, bottom: number[]) =>
      top.rgb.map((v, i) => v * top.alpha + bottom[i] * (1 - top.alpha));
    const luminance = (rgb: number[]) =>
      rgb
        .map((v) => {
          const c = v / 255;
          return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
        })
        .reduce((sum, v, index) => sum + v * [0.2126, 0.7152, 0.0722][index], 0);
    return [...document.querySelectorAll(sel)].map((element) => {
      const stack = [];
      for (let node: Element | null = element; node; node = node.parentElement) {
        const layer = parse(getComputedStyle(node).backgroundColor);
        if (layer && layer.alpha > 0) stack.push(layer);
      }
      let surface = [255, 255, 255];
      for (const layer of stack.reverse()) surface = over(layer, surface);
      const fg = parse(getComputedStyle(element).color) as { rgb: number[]; alpha: number };
      const text = over(fg, surface);
      const [high, low] = [luminance(text), luminance(surface)].sort((a, b) => b - a);
      return {
        text: (element.textContent || "").trim().slice(0, 40),
        selector: element.tagName.toLowerCase() + (element.getAttribute("data-slot") ? `[${element.getAttribute("data-slot")}]` : ""),
        color: getComputedStyle(element).color,
        surface: `rgb(${surface.map(Math.round).join(", ")})`,
        luminance: luminance(surface),
        ratio: Number(((high + 0.05) / (low + 0.05)).toFixed(2)),
        inRoot: Boolean(element.closest("#root")),
      };
    });
  }, selector);
}

/** Luminance of the surface the matched element renders on. */
async function surfaceLuminance(page: Page, selector: string): Promise<number> {
  const rows = await contrastRows(page, selector);
  expect(rows.length, `elementos para medir superficie: ${selector}`).toBeGreaterThan(0);
  return Math.min(...rows.map((row) => row.luminance));
}

/** Surface after the theme change has settled: the Sheet popup transitions its own
 *  background for 200ms when the theme flips while it is open. */
async function settledSurface(page: Page, selector: string, theme: "dark" | "light") {
  const isTheme = (value: number) => (theme === "dark" ? value < 0.05 : value > 0.5);
  await expect
    .poll(async () => isTheme(await surfaceLuminance(page, selector)), {
      message: `superficie de ${selector} en ${theme}`,
    })
    .toBe(true);
  return surfaceLuminance(page, selector);
}

const legible = (rows: Surface[]) =>
  rows
    .filter((row) => row.ratio < 4.5)
    .map((row) => `${row.selector} "${row.text}" ${row.color} on ${row.surface} = ${row.ratio.toFixed(2)}`);

test("el control cambia claro↔oscuro y la preferencia sobrevive la recarga", async ({ page }) => {
  await signIn(page);
  // A fresh context has no preference: the shell starts on the emulated system theme.
  const light = await waitForTheme(page, "light");
  expect(light.bodyLuminance, `fondo claro medido (${light.bodyBg})`).toBeGreaterThan(0.5);
  expect(light.bodyTextLuminance, `texto claro medido (${light.bodyFg})`).toBeLessThan(0.1);

  const dark = await useTheme(page, "dark");
  expect(dark.bodyLuminance, `fondo oscuro medido (${dark.bodyBg})`).toBeLessThan(0.05);
  expect(dark.bodyTextLuminance, `texto oscuro medido (${dark.bodyFg})`).toBeGreaterThan(0.7);
  await expect(page.getByRole("button", { name: "Usar tema claro" })).toHaveAttribute(
    "aria-pressed",
    "true",
  );

  // Durable preference: the reload must not fall back to the system theme.
  await page.reload();
  const reloaded = await waitForTheme(page, "dark");
  expect(reloaded.bodyLuminance, "fondo tras recargar en oscuro").toBeLessThan(0.05);

  // Round trip back to light, also durable.
  await useTheme(page, "light");
  await page.reload();
  const back = await waitForTheme(page, "light");
  expect(back.bodyLuminance, "fondo tras recargar en claro").toBeGreaterThan(0.5);
});

test("el tema oscuro alcanza las secciones reales y su texto sigue legible", async ({ page }) => {
  await signIn(page);
  await waitForTheme(page, "light");
  const routes = ["/?section=settings", "/?section=connections", "/?section=hosts", "/?section=work"];
  const measured: { route: string; light: number; dark: number; muted: number }[] = [];
  for (const route of routes) {
    await page.goto(route);
    await expect(page.locator("#wb-content")).toBeVisible();
    measured.push({
      route,
      light: await surfaceLuminance(page, "#wb-content"),
      dark: 0,
      muted: 0,
    });
  }

  await useTheme(page, "dark");
  for (const entry of measured) {
    await page.goto(entry.route);
    await expect(page.locator("#wb-content")).toBeVisible();
    entry.dark = await surfaceLuminance(page, "#wb-content");
    const muted = await contrastRows(page, "#wb-content .text-muted-foreground");
    entry.muted = muted.length;
    expect(legible(muted), `contraste AA del texto secundario en oscuro (${entry.route})`).toEqual([]);
  }

  // Screens no longer carry captions, so a route may have no secondary text at all; the
  // contrast gate still has to measure real text somewhere in the installation.
  expect(
    measured.reduce((sum, entry) => sum + entry.muted, 0),
    "texto secundario medido en alguna sección",
  ).toBeGreaterThan(0);

  // The same routes flipped for real: every section surface went from light to dark.
  const wrong = measured.filter((entry) => !(entry.light > 0.5 && entry.dark < 0.06));
  expect(
    wrong.map((entry) => `${entry.route} claro=${entry.light.toFixed(3)} oscuro=${entry.dark.toFixed(3)}`),
    "cada sección cambió de superficie con el tema",
  ).toEqual([]);
});

test("los portales React del shell y de los módulos siguen el tema oscuro", async ({ page }) => {
  await signIn(page);
  await useTheme(page, "dark");

  // Shell palette: a React portal outside #root, on the dark popover surface.
  await page.getByRole("button", { name: "Ir a…" }).click();
  await expect(page.getByRole("dialog")).toBeVisible();
  const palette = await contrastRows(page, '[data-slot="dialog-content"]');
  expect(palette.length, "diálogo de la paleta").toBe(1);
  expect(palette[0].inRoot, "la paleta es un portal fuera de #root").toBe(false);
  expect(palette[0].luminance, `superficie del diálogo (${palette[0].surface})`).toBeLessThan(0.05);
  expect(legible(palette), "contraste del diálogo de la paleta").toEqual([]);
  const options = await contrastRows(page, '[role="option"]');
  expect(options.length, "resultados de la paleta").toBeGreaterThan(0);
  expect(legible(options), "contraste de los resultados de la paleta").toEqual([]);
  const overlay = await page.evaluate(
    () => getComputedStyle(document.querySelector('[data-slot="dialog-overlay"]')!).backgroundColor,
  );
  const overlayAlpha = Number((overlay.match(/[\d.]+/g) || [])[3] ?? 1);
  const overlayChannels = (overlay.match(/[\d.]+/g) || []).slice(0, 3).map(Number);
  expect(overlayChannels.every((value) => value < 20), `velo oscuro del diálogo (${overlay})`).toBe(true);
  expect(overlayAlpha, `opacidad del velo (${overlay})`).toBeGreaterThanOrEqual(0.4);
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toBeHidden();

  // A module dialog renders through the same portal: ssh_host form in Equipos.
  await page.goto("/?section=hosts");
  await page.getByRole("button", { name: "Añadir equipo" }).click();
  await expect(page.getByRole("dialog")).toBeVisible();
  const hostDialog = await contrastRows(
    page,
    '[data-slot="dialog-content"], [data-slot="dialog-content"] .text-muted-foreground',
  );
  expect(hostDialog.length, "diálogo del módulo").toBeGreaterThan(1);
  expect(hostDialog.every((row) => !row.inRoot), "el diálogo del módulo es un portal").toBe(true);
  expect(hostDialog[0].luminance, `superficie del diálogo del módulo (${hostDialog[0].surface})`).toBeLessThan(0.05);
  expect(legible(hostDialog), "contraste del diálogo del módulo").toEqual([]);
  await page.keyboard.press("Escape");

  // The only select is native: its popup follows the document color-scheme, and its own
  // text keeps AA on the sidebar surface while dark.
  const select = await contrastRows(page, '[data-slot="native-select"]');
  expect(select.length, "selector nativo").toBeGreaterThan(0);
  expect(legible(select), "contraste del selector nativo en oscuro").toEqual([]);
  await expect(page.locator("html")).toHaveCSS("color-scheme", "dark");
});

test("la hoja de navegación móvil también cambia con el tema", async ({ page }) => {
  // Sign in at the desktop layout (the helper waits for the sidebar) and then go mobile:
  // the shell recomputes its layout on resize and moves the navigation into the Sheet.
  await signIn(page);
  await waitForTheme(page, "light");
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole("button", { name: "Abrir navegación" }).click();
  await expect(page.locator('[data-slot="sheet-content"]')).toBeVisible();
  const sheetLight = await settledSurface(page, '[data-slot="sheet-content"]', "light");

  // The theme control lives inside the sheet at this width.
  await useTheme(page, "dark");
  await settledSurface(page, '[data-slot="sheet-content"]', "dark");
  const sheetDark = await contrastRows(page, '[data-slot="sheet-content"]');
  expect(sheetDark.length, "hoja móvil").toBe(1);
  expect(sheetDark[0].inRoot, "la hoja es un portal fuera de #root").toBe(false);
  expect(sheetDark[0].luminance, `superficie de la hoja en oscuro (${sheetDark[0].surface})`).toBeLessThan(0.05);
  expect(legible(sheetDark), "contraste de la hoja en oscuro").toEqual([]);
  const sheetMuted = await contrastRows(page, '[data-slot="sheet-content"] .text-muted-foreground');
  expect(sheetMuted.length, "texto secundario de la hoja").toBeGreaterThan(0);
  expect(legible(sheetMuted), "contraste del texto secundario de la hoja").toEqual([]);
  const overlay = await contrastRows(page, '[data-slot="sheet-overlay"]');
  expect(overlay[0].luminance, "velo oscuro de la hoja").toBeLessThan(0.05);

  // Closing and reopening keeps the chosen theme, and light is legible again.
  await page.keyboard.press("Escape");
  await expect(page.locator('[data-slot="sheet-content"]')).toBeHidden();
  await page.getByRole("button", { name: "Abrir navegación" }).click();
  await expect(page.locator('[data-slot="sheet-content"]')).toBeVisible();
  expect(
    await settledSurface(page, '[data-slot="sheet-content"]', "dark"),
    `hoja en oscuro frente a claro (${sheetLight.toFixed(3)})`,
  ).toBeLessThan(0.05);
  await useTheme(page, "light");
  expect(await settledSurface(page, '[data-slot="sheet-content"]', "light")).toBeGreaterThan(0.5);
});

test("el tema del shell no invade la app nativa embebida", async ({ page }) => {
  // Magpie keeps its own theme authority: the shell's class and tokens must not reach its
  // document (a separate origin in an iframe), and its own screen must still render.
  await signIn(page);
  await useTheme(page, "dark");
  await page.goto("/?section=models");
  const native = page.frameLocator('iframe[title^="Magpie"]');
  await expect(native.locator("body")).toBeVisible();
  const state = await native.locator("html").evaluate((element) => ({
    shellDark: element.classList.contains("dark"),
    shellTokens: ["--muted-foreground", "--background", "--primary"].filter(
      (name) => getComputedStyle(element).getPropertyValue(name).trim() !== "",
    ),
    text: (element.ownerDocument.body.textContent || "").trim().length,
  }));
  expect(state.shellDark, "la clase .dark del shell no cruza al panel nativo").toBe(false);
  expect(state.shellTokens, "los tokens del shell no se aplican a la app nativa").toEqual([]);
  expect(state.text, "el panel nativo tiene contenido propio, no un iframe vacío").toBeGreaterThan(20);
});

test("una preferencia inválida cae al sistema y se recupera", async ({ page }) => {
  // Both directions: the invalid value is never treated as a theme name.
  for (const [system, expected] of [
    ["dark", "dark"],
    ["light", "light"],
  ] as const) {
    await page.emulateMedia({ colorScheme: system });
    await signIn(page);
    await page.evaluate(() => localStorage.setItem("workbench-theme", "sepia"));
    await page.reload();
    await expect(page.getByRole("navigation", { name: "Navegación principal" })).toBeVisible();
    const state = await waitForTheme(page, expected);
    // The invalid value is replaced by the resolved one, so the next load is stable.
    expect(state.stored, `preferencia saneada con sistema ${system}`).toBe(expected);
    expect(legible(await contrastRows(page, "#wb-content .text-muted-foreground, body"))).toEqual([]);
  }

  // Recovery: the control writes a valid preference that survives the reload.
  await useTheme(page, "dark");
  await page.reload();
  await waitForTheme(page, "dark");
});

test("el aviso de error real conserva contraste AA en oscuro y al volver a claro", async ({ page }) => {
  await signIn(page);
  await useTheme(page, "dark");
  await page.goto("/?section=settings");
  const card = page
    .locator('[data-slot="card"]')
    .filter({ has: page.getByRole("heading", { name: "Proyectos", exact: true }) });
  consoleGuard.expected.push("/api/settings");
  await card.getByLabel("Ruta de la carpeta").fill("/ruta/ui03-que-no-existe-19056");
  await card.getByRole("button", { name: "Añadir", exact: true }).click();
  await expect(page.getByRole("alert")).toBeVisible();

  const dark = await contrastRows(page, '[role="alert"]');
  expect(dark.length, "aviso de error en oscuro").toBe(1);
  expect(dark[0].luminance, `superficie del aviso en oscuro (${dark[0].surface})`).toBeLessThan(0.1);
  expect(legible(dark), "contraste del aviso en oscuro").toEqual([]);

  await useTheme(page, "light");
  const light = await contrastRows(page, '[role="alert"]');
  expect(light.length, "el aviso sigue ahí al cambiar a claro").toBe(1);
  expect(light[0].luminance, `superficie del aviso en claro (${light[0].surface})`).toBeGreaterThan(0.5);
  expect(legible(light), "contraste del aviso en claro").toEqual([]);
});
