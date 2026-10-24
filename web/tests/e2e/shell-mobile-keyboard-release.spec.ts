import { expect, test, type Page } from "@playwright/test";
import { requireRelease, TOKEN, watchConsole, type ConsoleGuard } from "./helpers";

// UI-07: the shell at 375px and at 200% zoom, plus keyboard-only operation of the menu,
// navigation, a form, a real module dialog and its native select, and reduced motion.
// Real release only (E2E_BASE_URL + E2E_ACCESS_TOKEN), no mock, no resize of a shared page:
// every case opens its own context with the viewport it needs, so the shell computes its
// mobile layout from the first render like a real device.
//
//   E2E_BASE_URL=http://127.0.0.1:19060 E2E_ACCESS_TOKEN=... \
//     bun run test tests/e2e/shell-mobile-keyboard-release.spec.ts

let guard: ConsoleGuard | null = null;

test.beforeAll(requireRelease);

test.beforeEach(({ page }) => {
  guard = watchConsole(page);
});

test.afterEach(() => {
  expect(guard?.errors ?? [], "consola del navegador").toEqual([]);
});

/** Every visible control must be reachable by scrolling and the document itself must never
 *  scroll sideways: a sideways page means an action ended up off-screen with no way to see it. */
async function reachable(page: Page) {
  return page.evaluate<{ controls: number; width: number; viewport: number; unreachable: string[] }>(`(() => {
    const vw = innerWidth;
    const unreachable = [];
    let controls = 0;
    for (const el of document.querySelectorAll('button, a[href], input, select, textarea, [tabindex]:not([tabindex="-1"])')) {
      if (el.closest('[inert], [aria-hidden="true"]')) continue;
      const box = el.getBoundingClientRect();
      if (box.width === 0 || box.height === 0) continue;
      const style = getComputedStyle(el);
      if (style.visibility === "hidden" || style.display === "none") continue;
      controls++;
      el.scrollIntoView({ block: "center", inline: "center" });
      const r = el.getBoundingClientRect();
      if (r.left < -1 || r.right > vw + 1 || r.top < -1 || r.bottom > innerHeight + 1)
        unreachable.push((el.getAttribute("aria-label") || el.textContent || el.id || el.tagName).trim().slice(0, 32));
    }
    return { controls, width: document.documentElement.scrollWidth, viewport: vw, unreachable };
  })()`);
}

async function expectNothingHidden(page: Page, where: string) {
  // Expand through the same disclosure control a user operates before measuring its actions.
  for (const detail of await page.locator("details").all()) {
    const summary = detail.locator(":scope > summary");
    const blocked = await summary.evaluate((el) => !!el.closest('[inert], [aria-hidden="true"]'));
    if (!blocked && await detail.getAttribute("open") === null && await summary.isVisible()) await summary.click();
  }
  const box = await reachable(page);
  expect(box.controls, `${where}: controles visibles`).toBeGreaterThan(0);
  expect(box.unreachable, `${where}: acciones fuera del viewport`).toEqual([]);
  expect(box.width, `${where}: el documento no se desplaza en horizontal`).toBeLessThanOrEqual(
    box.viewport + 1,
  );
}

// Las siete secciones vigentes, cada una con sus controles desde que el chunk monta:
// medir antes (por ejemplo, esperando una tabla de la pantalla retirada de Uso
// global) reportaria un layout limpio que el usuario nunca ve.
const SECTIONS: [string][] = [
  ["/?section=conversations"],
  ["/?section=tasks"],
  ["/?section=foundry"],
  ["/?section=sim"],
  ["/?section=models"],
  ["/?section=models&view=usage"],
  ["/?section=connections"],
  ["/?section=settings"],
  ["/?section=ninguna"], // the shell's own "not found" screen keeps its exits visible too
];

/** Opens a section and waits until it really rendered: measuring before the chunk (or the
 *  first control) is in the page would report a clean layout the user never sees. */
async function openSection(page: Page, route: string) {
  await page.goto(route);
  await expect(page.locator("#wb-content")).toBeVisible();
  await expect(
    page.locator("#wb-content button, #wb-content input, #wb-content select").first(),
  ).toBeVisible();
}

/** The release under test asks for a token: the form itself is part of the 375px case. */
async function signIn(page: Page) {
  if (!TOKEN) throw new Error("E2E_ACCESS_TOKEN is required for the mobile shell cases.");
  await page.locator("#access-token").fill(TOKEN);
  await page.getByRole("button", { name: "Entrar", exact: true }).click();
  await page.getByRole("button", { name: "Abrir navegación" }).waitFor();
}

/** Tabs until the focused control carries this name (or the bound is hit): keyboard only,
 *  no element is focused by position from the test. */
async function tabUntil(page: Page, name: string) {
  // Conexiones contains a variable number of host controls; inspect a whole focus cycle.
  const max = await page.locator('button, a[href], input, select, textarea, [tabindex]').count() + 2;
  for (let i = 0; i < max; i++) {
    await page.keyboard.press("Tab");
    await page.waitForTimeout(60); // the shell moves focus on its own frame (guards, portals)
    const focused = await page.evaluate<string>(
      `(() => { const el = document.activeElement; return el ? (el.getAttribute("aria-label") || el.textContent || "").trim() : ""; })()`,
    );
    if (focused.startsWith(name)) return;
  }
  throw new Error(`no keyboard stop carried "${name}"`);
}

/** Tabs forward and reports where focus landed. The settle frame matters: the modal focus
 *  guard redirects on the next animation frame, so reading immediately races that redirect. */
async function tabStops(page: Page, count: number) {
  const stops: string[] = [];
  for (let i = 0; i < count; i++) {
    await page.keyboard.press("Tab");
    await page.waitForTimeout(60);
    stops.push(
      await page.evaluate<string>(
        `(() => {
          const el = document.activeElement;
          if (!el) return "none";
          const popup = el.closest('[data-slot="dialog-content"],[data-slot="sheet-content"]');
          return (popup ? "in:" : "out:") + (el.getAttribute("aria-label") || el.textContent || el.tagName).trim().slice(0, 24);
        })()`,
      ),
    );
  }
  return stops;
}

test("375px: la pantalla de acceso y cada sección dejan todas sus acciones a la vista", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 667 });
  await page.goto("/?section=conversations");
  await expect(page.locator("#access-token")).toBeVisible();
  await expectNothingHidden(page, "acceso a 375px");

  await signIn(page);
  for (const [route] of SECTIONS) {
    await openSection(page, route);
    await expect(page.getByRole("button", { name: "Abrir navegación" })).toBeVisible();
    await expectNothingHidden(page, `${route} a 375px`);
  }
});

test.describe("zoom 200%", () => {
  // Playwright 1.55 cannot set Chromium's own zoom level (no CDP/API for the browser zoom),
  // so 200% is reproduced the way it really behaves: the same 1280x720 window with a CSS
  // viewport of half the width and DPR 2. Layout, media queries and rasterization therefore
  // see exactly what a 200% zoom sees.
  test.use({ viewport: { width: 640, height: 360 }, deviceScaleFactor: 2 });

  test("el shell sigue usable a 200% de zoom y su diálogo real conserva sus acciones", async ({ page }) => {
    await page.goto("/?section=conversations");
    expect(await page.evaluate<number>(`devicePixelRatio`)).toBe(2);
    await expect(page.locator("#access-token")).toBeVisible();
    await expectNothingHidden(page, "acceso a 200% de zoom");

    await signIn(page);
    // 640 CSS px wide is inside the mobile media query, so the shell must serve its sheet layout.
    await expect(page.getByRole("button", { name: "Abrir navegación" })).toBeVisible();
    for (const [route] of SECTIONS) {
      await openSection(page, route);
      await expectNothingHidden(page, `${route} a 200% de zoom`);
    }

    // The tall form of a real module dialog must stay operable in a 360px tall viewport.
    await openSection(page, "/?section=connections");
    await page.getByRole("button", { name: "Añadir equipo" }).click();
    await expect(page.locator('[data-slot="dialog-content"]')).toBeVisible();
    await expect(page.getByRole("button", { name: "Guardar y conectar" })).toBeVisible();
    await page.getByRole("button", { name: "Guardar y conectar" }).scrollIntoViewIfNeeded();
    await expect(page.getByRole("button", { name: "Guardar y conectar" })).toBeInViewport();
    await expect(page.getByRole("button", { name: "Cancelar" })).toBeInViewport();
    await expectNothingHidden(page, "diálogo del módulo a 200% de zoom");
    await page.keyboard.press("Escape");
    await expect(page.locator('[data-slot="dialog-content"]')).toBeHidden();
  });
});

test("menú, navegación, formulario, diálogo y select sólo con teclado", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 667 });
  await page.goto("/?section=connections");
  if (!TOKEN) throw new Error("E2E_ACCESS_TOKEN is required for the keyboard-only case.");

  // Form: focus, type and submit without the mouse.
  await page.locator("#access-token").focus();
  await page.keyboard.type(TOKEN);
  await page.keyboard.press("Tab");
  await expect(page.getByRole("button", { name: "Entrar", exact: true })).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("button", { name: "Añadir equipo" })).toBeVisible();

  // The skip link is the first stop and hands the content region the focus.
  await page.evaluate(`document.activeElement.blur()`);
  await page.keyboard.press("Tab");
  await expect(page.getByRole("link", { name: "Salta al contenido" })).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page.locator("#wb-content")).toBeFocused();

  // Menu: Enter on its button opens the sheet with the focus already inside.
  await page.evaluate(`document.activeElement.blur()`);
  await tabUntil(page, "Abrir navegación");
  await page.keyboard.press("Enter");
  await expect(page.locator('[data-slot="sheet-content"]')).toBeVisible();
  expect(
    await page.evaluate<boolean>(`!!document.activeElement?.closest('[data-slot="sheet-content"]')`),
    "al abrir la hoja el foco entra en ella",
  ).toBe(true);

  // Focus trap: a full loop of the sheet's controls never leaves it.
  const loop = await tabStops(page, 20);
  expect(loop.filter((stop) => stop.startsWith("out:")), "el foco sale de la hoja").toEqual([]);

  // Navigation without the mouse: the chosen section opens and the sheet closes.
  await tabUntil(page, "Conexiones");
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/section=connections/);
  await expect(page.locator("#wb-content h1")).toHaveText("Conexiones");
  await expect(page.locator('[data-slot="sheet-content"]')).toBeHidden();

  // Escape closes the sheet and returns the focus to the button that opened it.
  await page.evaluate(`document.activeElement.blur()`);
  await tabUntil(page, "Abrir navegación");
  await page.keyboard.press("Enter");
  await expect(page.locator('[data-slot="sheet-content"]')).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.locator('[data-slot="sheet-content"]')).toBeHidden();
  await expect(page.getByRole("button", { name: "Abrir navegación" })).toBeFocused();

  // Real module dialog: Enter opens it, the focus enters it and a full loop never leaves it.
  await tabUntil(page, "Añadir equipo");
  await page.keyboard.press("Enter");
  await expect(page.locator('[data-slot="dialog-content"]')).toBeVisible();
  await expect(page.locator("#host-name")).toBeFocused();
  const dialogLoop = await tabStops(page, 16);
  expect(dialogLoop.filter((stop) => stop.startsWith("out:")), "el foco sale del diálogo").toEqual([]);

  // Native select operated with the arrows: choosing the key file reveals its field.
  await page.locator("#host-auth").focus();
  await page.keyboard.press("ArrowDown");
  await expect(page.locator("#host-auth")).toHaveValue("key");
  await expect(page.locator("#host-key")).toBeVisible();
  await page.keyboard.press("ArrowUp"); // and back: the conditional field recovers
  await expect(page.locator("#host-auth")).toHaveValue("password");
  await expect(page.locator("#host-password")).toBeVisible();
  await page.locator("#host-name").focus();
  await page.keyboard.type("equipo de prueba");
  await expect(page.locator("#host-name")).toHaveValue("equipo de prueba");

  // Escape closes it and restores the focus to the trigger.
  await page.keyboard.press("Escape");
  await expect(page.locator('[data-slot="dialog-content"]')).toBeHidden();
  await expect(page.getByRole("button", { name: "Añadir equipo" })).toBeFocused();
});

test.describe("movimiento reducido", () => {
  test.use({ viewport: { width: 375, height: 667 } });

  test("los popups existentes abren y cierran sin animación", async ({ page }) => {
    // The context option of this runner does not reach this browser build, so the media
    // emulation is set on the page and asserted below before anything is measured.
    await page.emulateMedia({ reducedMotion: "reduce" });
    await page.goto("/?section=connections");
    expect(await page.evaluate<boolean>(`matchMedia("(prefers-reduced-motion: reduce)").matches`)).toBe(true);
    await signIn(page);
    await page.waitForTimeout(200);

    const durations = async (selector: string) =>
      page.evaluate<number[]>(`(() => {
        const s = getComputedStyle(document.querySelector(${JSON.stringify(selector)}));
        return [...s.transitionDuration.split(","), ...s.animationDuration.split(",")].map((v) => parseFloat(v));
      })()`);

    // Sheet.
    await page.getByRole("button", { name: "Abrir navegación" }).click();
    await expect(page.locator('[data-slot="sheet-content"]')).toBeVisible();
    expect(
      (await durations('[data-slot="sheet-content"]')).filter((value) => value > 0.001),
      "transición de la hoja",
    ).toEqual([]);
    expect(
      (await durations('[data-slot="sheet-overlay"]')).filter((value) => value > 0.001),
      "transición del velo",
    ).toEqual([]);
    await page.keyboard.press("Escape");
    await expect(page.locator('[data-slot="sheet-content"]')).toBeHidden();
    await expect(page.locator('[data-slot="sheet-overlay"]')).toHaveCount(0);

    // Real module dialog.
    await page.getByRole("button", { name: "Añadir equipo" }).click();
    await expect(page.locator('[data-slot="dialog-content"]')).toBeVisible();
    expect(
      (await durations('[data-slot="dialog-content"]')).filter((value) => value > 0.001),
      "transición del diálogo",
    ).toEqual([]);
    expect(
      (await durations('[data-slot="dialog-overlay"]')).filter((value) => value > 0.001),
      "transición del velo del diálogo",
    ).toEqual([]);
    await expect(page.locator("#host-name")).toBeFocused();
    await page.keyboard.press("Escape");
    await expect(page.locator('[data-slot="dialog-content"]')).toBeHidden();
    await expect(page.locator('[data-slot="dialog-content"]')).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Añadir equipo" })).toBeFocused();
  });
});
