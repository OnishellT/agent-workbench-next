import { expect, test, type Locator, type Page } from "@playwright/test";
import { readFileSync, readdirSync, statSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { join } from "node:path";
import { shellDictionary, translateShell } from "../../src/app/i18n";
import { sections } from "../../src/app/navigation";
import {
  conversationViews,
  coverageStatusLabels,
  jobStatusLabels,
  proposalStatusLabels,
  skippedReasonLabels,
  taskFilters,
  taskLabels,
  tasksViews,
} from "../../src/modules/conversations/domain";
import { translate as conversationsTranslate } from "../../src/modules/conversations/i18n";
import { translate as hostsTranslate } from "../../src/modules/hosts/i18n";
import { foundryViews } from "../../src/modules/foundry/domain";
import { translate as foundryTranslate } from "../../src/modules/foundry/i18n";
import { views as modelsViews } from "../../src/modules/models/domain";
import { translate as modelsTranslate } from "../../src/modules/models/i18n";
import { requireRelease, signIn, watchConsole, type ConsoleGuard } from "./helpers";

// UI-04 idioma y formatos: el selector existente, las pantallas y los modales, las fechas,
// los números y los errores visibles, con persistencia y recuperación. Inglés es un
// diccionario explícito por capa (shell y módulo): una clave ausente deja el texto en
// español, así que se ve en revisión y no se inventa ninguna traducción.
//
//   E2E_BASE_URL=http://127.0.0.1:19058 E2E_ACCESS_TOKEN=... E2E_LANGUAGE_DIR=/tmp/... \
//     bun run test tests/e2e/shell-language-formats.spec.ts

/** Palabras que son iguales en los dos idiomas y nombres propios: no son claves. */
const sameInBoth: Record<string, true> = {
  SIM: true,
  Host: true,
  Agents: true,
  Foundry: true,
  Skill: true,
  General: true,
  // memU es marca, igual en los dos idiomas: no es una etiqueta sin traducir.
  memU: true,
};

function labelTexts(value: unknown): string[] {
  if (typeof value === "string") return [value];
  if (Array.isArray(value))
    return value.flatMap((item) => {
      if (typeof item === "string") return [item];
      if (Array.isArray(item) && typeof item[1] === "string") return [item[1]];
      return [];
    });
  if (value && typeof value === "object")
    return Object.values(value).filter((item): item is string => typeof item === "string");
  return [];
}

function sourceFiles(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) sourceFiles(path, out);
    else if (/\.tsx?$/.test(path)) out.push(path);
  }
  return out;
}

const webRoot = fileURLToPath(new URL("../..", import.meta.url));
const sourceTree = join(webRoot, "src");

test.describe("contratos puros del idioma y los formatos", () => {
  test("cada etiqueta del shell tiene inglés, incluidas las que son ASCII", () => {
    const missing: string[] = [];
    for (const section of sections) {
      for (const text of [section.label, section.group]) {
        if (sameInBoth[text] || /^[a-z]/.test(text)) continue;
        if (translateShell(text, "en") === text) missing.push(text);
      }
      for (const [, label] of section.views)
        if (!sameInBoth[label] && translateShell(label, "en") === label) missing.push(label);
    }
    expect(missing, "etiquetas del shell sin inglés").toEqual([]);
    // Las dos que una lista blanca ASCII dejaba pasar: son palabras españolas sin acentos.
    expect(translateShell("Propuestas", "en")).toBe("Proposals");
    expect(translateShell("Tareas", "en")).toBe("Tasks");
    expect(translateShell("SIM", "en")).toBe("SIM");
    expect(shellDictionary["Conexiones"]).toBe("Connections");
  });

  test("cada mapa de etiquetas que una pantalla muestra con t(variable) tiene inglés", () => {
    const maps: [string, string[], (value: unknown, language: "en") => string][] = [
      ["conversationViews", labelTexts(conversationViews), conversationsTranslate],
      ["tasksViews", labelTexts(tasksViews), conversationsTranslate],
      ["taskFilters", labelTexts(taskFilters), conversationsTranslate],
      ["taskLabels", labelTexts(taskLabels), conversationsTranslate],
      ["proposalStatusLabels", labelTexts(proposalStatusLabels), conversationsTranslate],
      ["skippedReasonLabels", labelTexts(skippedReasonLabels), conversationsTranslate],
      ["coverageStatusLabels", labelTexts(coverageStatusLabels), conversationsTranslate],
      ["jobStatusLabels", labelTexts(jobStatusLabels), conversationsTranslate],
      ["foundryViews", labelTexts(foundryViews), foundryTranslate],
      ["modelsViews", labelTexts(modelsViews), modelsTranslate],
    ];
    const missing: string[] = [];
    for (const [name, texts, translate] of maps) {
      expect(texts.length, `${name} no publicó etiquetas`).toBeGreaterThan(0);
      for (const text of texts) {
        if (!text || sameInBoth[text] || /^[a-z]/.test(text)) continue;
        if (translate(text, "en") === text) missing.push(`${name}: ${text}`);
      }
    }
    expect(missing, "etiquetas de módulo sin inglés").toEqual([]);
    // Estados de tarea: los botones de filtro y la valoración, que se pintan desde el mapa.
    expect(conversationsTranslate("Todas", "en")).toBe("All");
    expect(conversationsTranslate("Sin valorar", "en")).toBe("Not valued");
    expect(conversationsTranslate("Pendiente de valoración", "en")).toBe("Pending valuation");
    expect(conversationsTranslate("Todas", "es")).toBe("Todas");
    // Una clave nueva y corta no puede comerse una frase más larga del diccionario.
    expect(conversationsTranslate("Todas las fuentes", "en")).toBe("All sources");
    expect(conversationsTranslate("Propuestas de tareas", "en")).toBe("Task proposals");
    expect(conversationsTranslate("Tareas de esta revisión", "en")).toBe("Tasks of this revision");
  });

  test("ningún argumento traducido se interpola: el diccionario casa el texto completo", () => {
    const offenders: string[] = [];
    for (const file of sourceFiles(sourceTree)) {
      if (file.endsWith("i18n.ts")) continue;
      const source = readFileSync(file, "utf8");
      for (const match of source.matchAll(/\b(?:t|translate)\(\s*(`[^`]*`|"[^"\n]*"\s*\+)/g))
        offenders.push(`${file.slice(webRoot.length)}: ${match[1].replace(/\s+/g, " ")}`);
    }
    expect(
      offenders,
      "una clave interpolada nunca casa: compón la frase con claves completas",
    ).toEqual([]);
    // Las frases interpoladas de las pantallas se arman con claves completas.
    expect(
      `${hostsTranslate("Ruta por ", "en")}${"eno1"} · MTU ${"1420"}. ${hostsTranslate("Configuración de red conservada.", "en")}`,
    ).toBe("Route through eno1 · MTU 1420. Network configuration left unchanged.");
    expect(
      `${hostsTranslate("La conexión SSH usa ", "en")}${"eno1"}${hostsTranslate("; revisa la IP de WireGuard del host.", "en")}`,
    ).toBe("The SSH connection uses eno1; check the host's WireGuard address.");
    expect(
      `${hostsTranslate("Identidad SSH de", "en")} ${"equipo-a"} ${hostsTranslate("confirmada", "en")} (${"SHA256:abc"}).`,
    ).toBe("SSH identity of equipo-a confirmed (SHA256:abc).");
    expect(
      `${hostsTranslate("Historial sincronizado:", "en")} ${3} ${hostsTranslate("conversaciones nuevas o actualizadas.", "en")}`,
    ).toBe("History synchronized: 3 new or updated conversations.");
    expect(`${3} ${hostsTranslate("archivos importados", "en")}`).toBe("3 files imported");
    expect(hostsTranslate("Historial sincronizado; no hay cambios nuevos.", "en")).toBe(
      "History synchronized; there are no new changes.",
    );
    expect(
      hostsTranslate("Consentimiento revocado; las conversaciones aceptadas se conservan.", "en"),
    ).toBe("Consent revoked; the accepted conversations are kept.");
    expect(hostsTranslate("Autorizar historial remoto", "en")).toBe("Authorize remote history");
    expect(hostsTranslate("Revocar consentimiento", "en")).toBe("Revoke consent");
    // El español no se mueve: la misma frase que se veía antes de partirla en claves.
    expect(
      `${hostsTranslate("Historial sincronizado:", "es")} ${3} ${hostsTranslate("conversaciones nuevas o actualizadas.", "es")}`,
    ).toBe("Historial sincronizado: 3 conversaciones nuevas o actualizadas.");
    expect(
      `${hostsTranslate("Identidad SSH de", "es")} ${"equipo-a"} ${hostsTranslate("confirmada", "es")} (${"SHA256:abc"}).`,
    ).toBe("Identidad SSH de equipo-a confirmada (SHA256:abc).");
    expect(
      `${hostsTranslate("Ruta por ", "es")}${"eno1"} · MTU ${"1420"}. ${hostsTranslate("Configuración de red conservada.", "es")}`,
    ).toBe("Ruta por eno1 · MTU 1420. Configuración de red conservada.");
  });

  test("una clave ausente se queda legible y los números siguen el idioma", () => {
    expect(translateShell("una cadena sin diccionario", "en")).toBe("una cadena sin diccionario");
    expect(conversationsTranslate("Eventos sin clave", "en")).toBe("Events sin clave");
    expect(conversationsTranslate("Eventos", "es")).toBe("Eventos");
  });
});

test.describe("flujo real del selector, la persistencia y las pantallas", () => {
  test.beforeAll(requireRelease);

  let guard: ConsoleGuard;

  test.beforeEach(({ page }) => {
    guard = watchConsole(page);
    // Esta entrega no sirve la ruta de propuestas de tareas: su 404 es el estado
    // publicado, no un fallo del idioma.
    guard.expected.push("/api/tasks/proposals/");
  });

  test.afterEach(() => {
    expect(guard.errors, "consola del navegador").toEqual([]);
  });

  const languageBox = (page: Page) =>
    page.locator('select[aria-label="Idioma"], select[aria-label="Language"]');

  async function storedLanguage(page: Page) {
    return page.evaluate(() => localStorage.getItem("workbench-language"));
  }

  async function shellLanguage(page: Page) {
    return page.evaluate(() => document.documentElement.lang);
  }

  test("el selector cambia el shell, la paleta y el menú móvil, y vuelve al español", async ({
    page,
  }) => {
    await signIn(page);
    await page.goto("/?section=settings");
    await expect(page.getByRole("heading", { level: 1, name: "Ajustes" })).toBeVisible();
    expect(await shellLanguage(page)).toBe("es");

    await languageBox(page).selectOption("en");
    await expect(page.getByRole("heading", { level: 1, name: "Settings" })).toBeVisible();
    expect(await shellLanguage(page)).toBe("en");
    expect(await storedLanguage(page)).toBe("en");
    await expect(page).toHaveTitle("Settings · Agent Workbench");
    await expect(page.getByRole("navigation", { name: "Main navigation" })).toBeVisible();

    // La paleta es un modal real: sus etiquetas salen del diccionario del shell.
    await page.keyboard.press("Control+k");
    const palette = page.getByRole("dialog");
    await expect(palette).toBeVisible();
    await expect(palette.getByRole("combobox", { name: "Search section or setting" })).toBeVisible();
    await expect(palette.getByRole("option", { name: /Proposals/ })).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(palette).toBeHidden();

    // El menú móvil es otro modal del shell, con el selector dentro.
    await page.setViewportSize({ width: 420, height: 800 });
    await page.getByRole("button", { name: "Open navigation" }).click();
    const sheet = page.getByRole("dialog");
    await expect(sheet).toBeVisible();
    await expect(sheet.getByRole("button", { name: "Settings", exact: true })).toBeVisible();
    await expect(sheet.getByLabel("Language")).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(sheet).toBeHidden();
    await page.setViewportSize({ width: 1280, height: 720 });

    await languageBox(page).selectOption("es");
    await expect(page.getByRole("heading", { level: 1, name: "Ajustes" })).toBeVisible();
    expect(await storedLanguage(page)).toBe("es");
  });

  test("la elección sobrevive a la recarga, ?lang= manda y un valor inválido se repara", async ({
    page,
  }) => {
    await signIn(page);
    await page.goto("/?section=settings");
    await languageBox(page).selectOption("en");
    await expect(page.getByRole("heading", { level: 1, name: "Settings" })).toBeVisible();

    await page.reload();
    await expect(page.getByRole("heading", { level: 1, name: "Settings" })).toBeVisible();
    expect(await shellLanguage(page)).toBe("en");

    await page.goto("/?section=settings&lang=es");
    await expect(page.getByRole("heading", { level: 1, name: "Ajustes" })).toBeVisible();
    await page.reload();
    await expect(page.getByRole("heading", { level: 1, name: "Ajustes" })).toBeVisible();

    // Un idioma guardado que no existe no deja la interfaz a medias: vuelve al español
    // y el valor guardado queda reparado.
    for (const invalid of ["fr", "EN", ""]) {
      await page.evaluate((value) => localStorage.setItem("workbench-language", value), invalid);
      await page.goto("/?section=settings");
      await expect(page.getByRole("heading", { level: 1, name: "Ajustes" })).toBeVisible();
      expect(await shellLanguage(page), `idioma inválido ${JSON.stringify(invalid)}`).toBe("es");
      expect(await storedLanguage(page)).toBe("es");
    }
  });

  test("una sección desconocida se nombra y ofrece las secciones en el idioma elegido", async ({ page }) => {
    await signIn(page);
    await page.goto("/?section=no-existe&lang=en");
    await expect(page.getByRole("heading", { level: 1, name: "Section not found" })).toBeVisible();
    await expect(
      page.locator("#wb-content").getByRole("button", { name: "Connections", exact: true }),
    ).toBeVisible();
    await page.goto("/?section=no-existe&lang=es");
    await expect(page.getByRole("heading", { level: 1, name: "Sección no encontrada" })).toBeVisible();
  });

  test("las etiquetas de las pantallas y de los paneles salen en el idioma elegido", async ({
    page,
  }) => {
    guard.expected.push("magpie");
    await signIn(page);
    await page.goto("/?section=tasks&lang=en");
    await expect(page.getByRole("button", { name: "Proposals", exact: true })).toBeVisible();
    for (const filter of ["All", "Incomplete", "Not valued", "Correct", "Incorrect"])
      await expect(page.getByRole("button", { name: filter, exact: true })).toBeVisible();
    await page.goto("/?section=conversations&view=sources&lang=en");
    await expect(page.getByRole("button", { name: "Sources", exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: "Guided example", exact: true })).toBeVisible();
    await page.goto("/?section=models&lang=en");
    await expect(
      page.getByRole("heading", { level: 1, name: "Agent configuration" }),
    ).toBeVisible();
    await expect(page.getByLabel("Models host")).toBeVisible();
    // ?section=usage es un marcador retirado: cae en Modelos → Uso sin reescribir la
    // dirección. La pantalla propia de Uso global ya no existe.
    await page.goto("/?section=usage&lang=en");
    await expect(
      page.getByRole("heading", { level: 1, name: "Agent configuration" }),
    ).toBeVisible();
    await expect(page).toHaveURL(/section=usage/);
    await expect(page.locator("#wb-content")).toHaveAttribute("data-section", "models");
    await page.goto("/?section=usage&lang=es");
    await expect(
      page.getByRole("heading", { level: 1, name: "Configuración de agentes" }),
    ).toBeVisible();
    await expect(page.locator("#wb-content")).toHaveAttribute("data-section", "models");
  });

  test("una pantalla con datos reales localiza fechas y tamaños sin mover los contadores", async ({
    browser,
  }) => {
    test.setTimeout(180_000);
    const directory = process.env.E2E_LANGUAGE_DIR;
    if (!directory || !directory.startsWith("/"))
      throw new Error(
        "Este caso escribe su propia fixture: define E2E_LANGUAGE_DIR con una carpeta escribible en el servidor bajo prueba. Sin valor por defecto.",
      );
    const title = `ui04-idioma-${Date.now().toString(36)}`;
    const fixture = join(directory, "ui04-language.jsonl");
    const content =
      [
        { session_id: title, role: "user", kind: "message", text: title },
        {
          session_id: title,
          role: "assistant",
          kind: "message",
          text: `respuesta ${"relleno-para-tamano ".repeat(160)}`,
          channel: "final",
        },
      ]
        .map((record) => JSON.stringify(record))
        .join("\n") + "\n";
    writeFileSync(fixture, content, "utf8");
    // El tamaño que el servidor reporta para su propia captura: el mismo número en los
    // dos idiomas (es-DO y en-US comparten el separador decimal), sin inventar ninguno.
    const size = (Buffer.byteLength(content, "utf8") / 1024).toFixed(1);

    const context = await browser.newContext({ timezoneId: "UTC" });
    const page = await context.newPage();
    guard = watchConsole(page);
    guard.expected.push("/api/tasks/proposals/");
    try {
      await signIn(page);
      await page.goto("/?section=conversations&view=sources&lang=es");
      // El registro es idempotente: la instalación manda qué fuentes tiene, no el DOM
      // recién cargado (una segunda corrida no vuelve a registrar la misma ruta).
      const registered = await page.request.get("/api/conversations/sources");
      expect(registered.status()).toBe(200);
      const list = (await registered.json()) as { sources?: { path?: string }[] };
      if (!(list.sources ?? []).some((source) => source.path === fixture))
        await registerSource(page, fixture);
      await expect(sourceRow(page, fixture)).toBeVisible({ timeout: 30_000 });
      await capture(page, fixture);
      await expect(page.getByText("Terminada", { exact: true })).toBeVisible({ timeout: 60_000 });

      const spanish = await conversationRowText(page, title, "es");
      expect(spanish, "fecha y estado en español").toMatch(
        /\d{1,2}\s(ene|feb|mar|abr|may|jun|jul|ago|sep|oct|nov|dic)\s\d{4}/u,
      );
      expect(spanish).toMatch(/p\.\s?m\.|a\.\s?m\./);
      expect(spanish).toContain("completa");
      expect(spanish).toContain("2 eventos");

      const english = await conversationRowText(page, title, "en");
      expect(english, "fecha y estado en inglés").toMatch(
        /(Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)\s\d{1,2},\s\d{4}/,
      );
      expect(english).toMatch(/\d{1,2}:\d{2}\s?(AM|PM)/);
      expect(english).toContain("complete");
      expect(english).toContain("2 events");
      // El mismo dato: ni el idioma ni la zona horaria cambian el contador de la lectura.
      const counters = (await page.locator("main").innerText()).match(/\d+ \/ \d+/g) ?? [];
      expect(counters.length, "el contador de la lectura se muestra").toBeGreaterThan(0);
      expect(counters, "el contador no depende del idioma").toContain("1 / 1");

      // El detalle muestra la evidencia real de la captura: dos eventos y el tamaño que
      // el servidor reportó, con las palabras del idioma elegido.
      const detailEn = await openConversation(page, title, "en");
      expect(detailEn).toContain(`2 events · ${size} KiB`);
      const detailEs = await openConversation(page, title, "es");
      expect(detailEs).toContain(`2 eventos · ${size} KiB`);

      // Otra zona horaria: misma lectura y mismos contadores, reloj local distinto.
      const far = await browser.newContext({ timezoneId: "Pacific/Kiritimati" });
      const farPage = await far.newPage();
      const farGuard = watchConsole(farPage);
      farGuard.expected.push("/api/tasks/proposals/");
      try {
        await signIn(farPage);
        await farPage.goto("/?section=conversations&view=conversations&lang=en");
        const farText = await conversationRowText(farPage, title, "en");
        expect(farText).toContain("complete");
        expect(farText.match(/\d{1,2}:\d{2}\s?(AM|PM)/)?.[0], "el reloj local cambia con la zona").not.toBe(
          english.match(/\d{1,2}:\d{2}\s?(AM|PM)/)?.[0],
        );
        const farCounters = (await farPage.locator("main").innerText()).match(/\d+ \/ \d+/g) ?? [];
        expect(farCounters).toEqual(counters);
        // La lectura que publica la instalación es la misma en las dos zonas. El
        // resumen de Uso global está retirado (404): se compara el registro de
        // fuentes, que es lo que esta pantalla ya usa.
        const [here, far] = await Promise.all(
          [page, farPage].map((target) =>
            target.evaluate(async () => {
              const response = await fetch("/api/conversations/sources");
              const body = await response.json();
              return { status: response.status, body };
            }),
          ),
        );
        expect(here).toEqual(far);
      } finally {
        expect(farGuard.errors, "consola de la zona +14").toEqual([]);
        await far.close();
      }
      expect(guard.errors, "consola del navegador").toEqual([]);
    } finally {
      await context.close();
    }
  });
});

/** Fila de la fuente del caso: la que declara su propia ruta y su control de escaneo. */
function sourceRow(page: Page, path: string): Locator {
  return page
    .locator("div")
    .filter({ hasText: path })
    .filter({ has: page.getByRole("button", { name: "Escanear", exact: true }) })
    .last();
}

async function registerSource(page: Page, path: string) {
  await page.getByRole("button", { name: "Añadir fuente", exact: true }).first().click();
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("Ruta o archivo de la fuente").fill(path);
  await dialog.getByLabel("Adaptador").selectOption("canonical");
  await dialog.getByRole("button", { name: "Registrar fuente", exact: true }).click();
  await expect(dialog).toBeHidden();
}

async function capture(page: Page, path: string) {
  const scan = page.waitForResponse(
    (response) => response.request().method() === "POST" && response.url().endsWith("/scan"),
  );
  await sourceRow(page, path).getByRole("button", { name: "Escanear", exact: true }).click();
  expect((await scan).status()).toBe(202);
}

/** La conversación de la fixture, buscada por su propio título en el idioma pedido. */
async function conversationRowText(page: Page, title: string, language: "es" | "en") {
  const row = await findConversation(page, title, language);
  return (await row.innerText()).replace(/\s+/g, " ");
}

/** El detalle de esa misma fila: la evidencia que la instalación guardó de la captura. */
async function openConversation(page: Page, title: string, language: "es" | "en") {
  const row = await findConversation(page, title, language);
  await row.getByRole("button", { name: language === "es" ? "Abrir" : "Open", exact: true }).click();
  const detail = page.locator("main");
  await expect(detail.getByText(/KiB/)).toBeVisible();
  return (await detail.innerText()).replace(/\s+/g, " ");
}

async function findConversation(page: Page, title: string, language: "es" | "en") {
  await page.goto(`/?section=conversations&view=conversations&lang=${language}`);
  const search = page.getByLabel(language === "es" ? "Buscar conversación" : "Search conversation");
  await search.fill(title);
  const row = page.locator("tbody tr").filter({ hasText: title }).first();
  await expect(row).toBeVisible();
  return row;
}
