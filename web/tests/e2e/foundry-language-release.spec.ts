import { expect, test, type Frame, type Page } from "@playwright/test";
import { requireRelease, signIn, watchConsole, type ConsoleGuard } from "./helpers";

// Foundry follows the language selected in Workbench: at load (?lang=), live from the shell's
// selector, and on every page and dialog, for its own screens and for server messages. Prepares
// Foundry's guided example in the instance under test (no model is called): use a trial instance.
//
//   E2E_BASE_URL=http://127.0.0.1:19177 E2E_ACCESS_TOKEN_FILE=... bun run test:e2e foundry-language-release.spec.ts

let consoleGuard: ConsoleGuard;

test.beforeAll(requireRelease);

test.beforeEach(async ({ page }) => {
  consoleGuard = watchConsole(page);
});

test.afterEach(() => {
  expect(consoleGuard.errors, "consola del navegador").toEqual([]);
});

const frameOf = (page: Page) => page.frameLocator('iframe[title="Foundry"]');
const foundryFrame = (page: Page): Frame => page.frames().find((frame) => frame.url().includes("/foundry/"))!;
const languageBox = (page: Page) => page.locator('select[aria-label="Idioma"], select[aria-label="Language"]');
const tabs = (page: Page) => page.getByRole("navigation", { name: /Secciones de Foundry|Sections of Foundry/ });

/** Visible text and attributes in the frame that still look Spanish: accents, or common Spanish
 *  words. Text the user's own data supplies (transcripts, code) is excluded as the app does. */
async function spanishLeft(page: Page): Promise<string[]> {
  return foundryFrame(page).evaluate(() => {
    const words =
      /(^|[\s(¿¡·])(de|la|el|los|las|que|para|con|una|un|sin|por|del|al|tu|tus|su|este|esta|ya|más|todas|todos|nueva|nuevo|ver|conversaciones|conversación|tareas|tarea|fuentes|fuente|archivos|cambios|errores|guardar|cancelar|cerrar|crear|estado|casos|resumen|actividad|aprendizajes|aprendizaje|experimentos|configuración|alcance|dominio|proyecto|regla|herramienta|origen|transferencia|valoración|correcta|incorrecta|pendiente|activo|pausada|inicio|fin|eventos|revisión|modelo|agente|hoy|ayer|preparar|actualizar|conectar|reintentar|restaurar|inspeccionar|propuestas|completado|cancelado|adoptado|captura|capturar|añadir|detectar|escanear|buscar|filtros|quitar)([\s.,:;)·…]|$)/i;
    const accents = /[áéíóúñ¿¡]/i;
    const skip = "pre, code, textarea, script, style, .event-box, .code-block, [data-notranslate]";
    const out = new Set<string>();
    const looksSpanish = (text: string) => accents.test(text) || words.test(text);
    const tree = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    for (let node = tree.nextNode(); node; node = tree.nextNode()) {
      const parent = node.parentElement;
      const text = (node.nodeValue ?? "").replace(/\s+/g, " ").trim();
      if (!text || !parent || parent.closest(skip) || getComputedStyle(parent).display === "none") continue;
      if (looksSpanish(text)) out.add(text);
    }
    for (const element of document.querySelectorAll("[placeholder],[title],[aria-label],[alt]")) {
      if (element.closest(skip)) continue;
      for (const name of ["placeholder", "title", "aria-label", "alt"]) {
        const value = element.getAttribute(name)?.replace(/\s+/g, " ").trim();
        if (value && looksSpanish(value)) out.add(`[${name}] ${value}`);
      }
    }
    return [...out];
  });
}

test("Foundry arranca en el idioma del shell y lo sigue en vivo, sin recargar", async ({ page }) => {
  await signIn(page, "/");
  await page.goto("/?section=foundry&lang=en");
  await expect(frameOf(page).getByRole("heading", { name: "Overview", exact: true })).toBeVisible({ timeout: 30_000 });
  expect(await foundryFrame(page).evaluate(() => document.documentElement.lang)).toBe("en");
  await foundryFrame(page).evaluate(() => ((window as unknown as { __kept: number }).__kept = 1));

  await languageBox(page).selectOption("es");
  await expect(frameOf(page).getByRole("heading", { name: "Resumen", exact: true })).toBeVisible();
  expect(await foundryFrame(page).evaluate(() => document.documentElement.lang)).toBe("es");
  expect(await foundryFrame(page).evaluate(() => (window as unknown as { __kept?: number }).__kept), "el marco no se recargó").toBe(1);

  await languageBox(page).selectOption("en");
  await expect(frameOf(page).getByRole("heading", { name: "Overview", exact: true })).toBeVisible();
  // A new load starts in the saved language.
  await page.goto("/?section=foundry");
  await expect(frameOf(page).getByRole("heading", { name: "Overview", exact: true })).toBeVisible({ timeout: 30_000 });
  await languageBox(page).selectOption("es");
});

test("en inglés ninguna página ni diálogo de Foundry deja texto en español", async ({ page }) => {
  test.setTimeout(180_000);
  await signIn(page, "/?section=foundry&lang=es");
  const frame = frameOf(page);
  await expect(frame.getByRole("heading", { name: "Resumen", exact: true })).toBeVisible({ timeout: 30_000 });
  // Data to look at: the guided example's conversation, task and cases (no model is called).
  const prepared = await page.request.post("/foundry/api/action", { data: { action: "example" } });
  expect(prepared.status()).toBe(200);
  await expect
    .poll(async () => (await (await page.request.get("/foundry/api/state?compact=1")).json()).cases.length, { timeout: 60_000 })
    .toBeGreaterThanOrEqual(2);

  await languageBox(page).selectOption("en");
  const left: Record<string, string[]> = {};
  const check = async (label: string) => {
    await page.waitForTimeout(400);
    const text = await spanishLeft(page);
    if (text.length) left[label] = text;
  };
  const closeDialog = async () => {
    await page.keyboard.press("Escape");
    await page.waitForTimeout(250);
  };

  for (const [view, heading] of [
    ["Overview", "Overview"], ["Sources", "Connected sources"], ["Conversations", "Conversations and tasks"],
    ["Learning", "Learnings"], ["Experiments", "Experiments"], ["Configuration", "Settings"],
  ]) {
    await tabs(page).getByRole("button", { name: view, exact: true }).click();
    await expect(frame.getByRole("heading", { level: 1, name: heading, exact: true })).toBeVisible();
    await check(`page ${view}`);
  }

  // Dialogs and the task editor.
  await tabs(page).getByRole("button", { name: "Sources", exact: true }).click();
  await frame.getByRole("button", { name: "Add source" }).first().click();
  await expect(frame.getByRole("dialog")).toBeVisible();
  await check("dialog: connect a source");
  await closeDialog();

  await tabs(page).getByRole("button", { name: "Conversations", exact: true }).click();
  await frame.getByRole("button", { name: "Organize tasks" }).first().click();
  await expect(frame.getByRole("dialog")).toBeVisible();
  await check("dialog: organize tasks");
  await closeDialog();

  await frame.getByRole("tab", { name: /^Tasks/ }).first().click();
  await frame.getByRole("button", { name: "Review task" }).first().click();
  await expect(frame.getByRole("dialog")).toBeVisible();
  await check("dialog: task editor");
  // Its collapsed sections hold the case forms.
  for (const summary of await frame.locator("details > summary").all()) await summary.click();
  await check("dialog: task editor, sections open");
  await closeDialog();

  expect(left, "Spanish text still on screen in English").toEqual({});
});

test("un error del servidor de Foundry llega en el idioma elegido", async ({ page }) => {
  consoleGuard.expected.push("/foundry/api/action"); // the rejected source is the point of the case
  await signIn(page, "/");
  const frame = frameOf(page);
  for (const [lang, expected] of [
    ["en", "The source must be an existing file or directory, without symbolic links."],
    ["es", "La fuente debe ser un archivo o directorio existente, sin enlaces simbólicos."],
  ] as const) {
    await page.goto(`/?section=foundry&view=sources&lang=${lang}`);
    await expect(frame.getByRole("heading", { level: 1 })).toBeVisible({ timeout: 30_000 });
    await frame.getByRole("button", { name: lang === "en" ? "Add source" : "Añadir fuente" }).first().click();
    await frame.getByRole("textbox", { name: lang === "en" ? /Local path/ : /Ruta local/ }).fill("/ruta/que/no/existe/en/este/servidor");
    await frame.getByRole("button", { name: lang === "en" ? "Connect source" : "Conectar fuente" }).click();
    await expect(frame.getByText(expected)).toBeVisible();
  }
});
