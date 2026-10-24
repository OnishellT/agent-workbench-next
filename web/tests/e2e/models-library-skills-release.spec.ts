import { expect, test, type FrameLocator } from "@playwright/test";
import { requireRelease, signIn, watchConsole, type ConsoleGuard } from "./helpers";

// MO-11: Library skills del Magpie ORIGINAL que la instalación enmarca en Modelos.
// Se instala un recurso autorizado desde una carpeta que existe en el servidor bajo
// prueba, se da a un agente y se comprueba el efecto en la página nativa y en
// /api/library, con recarga. Negativos: una ruta que no es carpeta y una carpeta sin
// SKILL.md no cambian la biblioteca y se dicen en la página.
//
// Lo que un navegador remoto no puede leer —el archivo y su hash dentro de la carpeta
// del agente, que el propio agente lo cargue en una tarea posterior y el problema por
// agente cuando el agente ya tiene una skill con ese nombre— se verifica en el corte
// local contra la misma instalación:
//   work/migration-dev/mo11-library-skills/flow.mjs
//
//   E2E_BASE_URL=http://127.0.0.1:19140 E2E_ACCESS_TOKEN_FILE=.../state/access.token \
//   E2E_SKILL_SOURCE_DIR=.../fixture/skills E2E_SKILL_EMPTY_DIR=.../fixture/empty-folder \
//     bun run test tests/e2e/models-library-skills-release.spec.ts

const SOURCE = process.env.E2E_SKILL_SOURCE_DIR ?? "";
const EMPTY = process.env.E2E_SKILL_EMPTY_DIR ?? "";
const SKILL = process.env.E2E_SKILL_NAME ?? "mo11-fixture-skill";

let guard: ConsoleGuard | null = null;

test.beforeEach(({ page }) => {
  requireRelease();
  guard = watchConsole(page);
  // Los dos negativos de esta prueba piden a propósito una fuente inválida.
  guard.expected.push("/api/library/skills/probe");
});

test.afterEach(() => {
  expect(guard?.errors ?? [], "consola del navegador").toEqual([]);
});

test("MO-11: instalar y activar una skill de la Library para un agente", async ({ page }) => {
  test.skip(
    !SOURCE || !EMPTY,
    "E2E_SKILL_SOURCE_DIR y E2E_SKILL_EMPTY_DIR apuntan a carpetas preparadas en el servidor bajo prueba (sin valores por defecto).",
  );
  await signIn(page, "/?section=models");
  const panel = page.frameLocator('iframe[title^="Magpie ·"]');
  await panel.locator("#nav").waitFor({ timeout: 30_000 });
  await libraryOn(panel);

  // Página de la Library del Magpie nativo, en su pestaña de skills.
  await panel.locator("#nav button", { hasText: "Library" }).first().click();
  await openTab(panel, "Skills");
  await expect(panel.locator('input[data-lib="source"]')).toBeVisible({ timeout: 30_000 });

  const before = await library(panel);
  expect(before.skills, "la biblioteca empieza sin skills de esta prueba").toEqual([]);
  const agent = before.agents.filter((a) => a.skills)[0];
  expect(agent?.skills, "la instalación reporta la carpeta de skills del agente").toBeTruthy();

  // ── negativo: una ruta que no es una carpeta
  await findSkills(panel, `${SOURCE}/does-not-exist`);
  await expect(panel.locator(".lib-card.lib-install .lib-err")).toContainText("isn't a folder");
  expect((await library(panel)).skills).toEqual([]);

  // ── negativo: una carpeta sin SKILL.md dentro
  await findSkills(panel, EMPTY);
  await expect(panel.locator("#status")).toContainText("No SKILL.md found there");
  await expect(panel.locator(".lib-probe")).toContainText("No skills there");
  expect((await library(panel)).skills).toEqual([]);

  // ── la skill de la carpeta autorizada, para el agente de la prueba
  await findSkills(panel, SOURCE);
  const pick = panel.locator(".lib-probe .lib-pick").filter({ hasText: SKILL }).first();
  await expect(pick).toBeVisible();
  await expect(pick.locator("input")).toBeChecked();
  for (const chip of await panel.locator(".lib-probe button.lib-ag[data-agent]").all()) {
    const wanted = (await chip.getAttribute("data-agent")) === agent.id;
    if (wanted !== ((await chip.getAttribute("aria-pressed")) === "true")) await chip.click();
  }
  await panel.locator(".lib-probefoot button.primary").click();

  const installed = (await library(panel)).skills.find((s) => s.name === SKILL);
  expect(installed, "la skill queda en la biblioteca").toBeTruthy();
  expect(installed?.agents).toEqual([agent.id]);
  expect(installed?.problems ?? {}).toEqual({});
  await expect(panel.locator("#status")).not.toContainText("skipped");
  const row = panel.locator(".lib-row.lib-skill").filter({ hasText: SKILL });
  await expect(row).toBeVisible();
  await expect(row.locator(`button.lib-ag[data-agent="${agent.id}"]`)).toHaveAttribute("aria-pressed", "true");

  // ── recargar conserva la skill y su agente
  await page.reload();
  const reloadedPanel = page.frameLocator('iframe[title^="Magpie ·"]');
  await reloadedPanel.locator("#nav").waitFor({ timeout: 30_000 });
  const reloaded = (await library(reloadedPanel)).skills.find((s) => s.name === SKILL);
  expect(reloaded?.agents).toEqual([agent.id]);
});

/** La tira de pestañas de la página nativa: el panel la pone en la barra lateral o
 *  dentro de la página según el ancho, y sólo una de las dos está visible. */
async function openTab(panel: FrameLocator, label: string) {
  const side = panel.locator(".nav-sub .nav-s", { hasText: label }).first();
  const inside = panel.locator(".lib-tabs button.opt", { hasText: label }).first();
  await ((await side.isVisible().catch(() => false)) ? side : inside).click();
}

/** El built-in de la Library viene apagado en una instalación nueva. */
async function libraryOn(panel: FrameLocator) {
  const builtinOn = async () =>
    (await panel.locator("body").evaluate(async () =>
      (await fetch("/api/plugins/builtin", { headers: { accept: "application/json" } })).json(),
    ) as { id: string; on: boolean }[]).find((f) => f.id === "library")?.on === true;
  if (await builtinOn()) return;
  await panel.locator("#nav button", { hasText: "Plugins" }).first().click();
  await openTab(panel, "Built-in");
  const row = panel.locator('.feat-row[data-id="library"]');
  await expect(row).toBeVisible({ timeout: 30_000 });
  if ((await row.locator('button[role="switch"]').getAttribute("aria-checked")) !== "true") {
    await row.locator('button[role="switch"]').click();
  }
  await expect(panel.locator("#nav button", { hasText: "Library" }).first()).toBeVisible();
}

async function findSkills(panel: FrameLocator, source: string) {
  await panel.locator('input[data-lib="source"]').fill(source);
  const find = panel.locator("button.action", { hasText: "Find skills" }).first();
  await find.click();
  await expect(find).toBeEnabled({ timeout: 30_000 });
}

interface LibraryView {
  agents: { id: string; name: string; skills?: string }[];
  skills: { name: string; agents: string[]; problems?: Record<string, string> }[];
}

/** La autoridad del corte: el mismo cuerpo que la página nativa dibuja. */
async function library(panel: FrameLocator): Promise<LibraryView> {
  return (await panel.locator("body").evaluate(async () =>
    (await fetch("/api/library", { headers: { accept: "application/json" } })).json(),
  )) as LibraryView;
}
