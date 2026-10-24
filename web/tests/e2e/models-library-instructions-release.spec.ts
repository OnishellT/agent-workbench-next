import { expect, test, type FrameLocator } from "@playwright/test";
import { requireRelease, signIn, watchConsole, type ConsoleGuard } from "./helpers";

// MO-13: Library instructions del Magpie ORIGINAL que la instalación enmarca en Modelos.
// Se crea un conjunto de instrucciones nuevo, se elige cuál leen los agentes, se enciende
// UN agente y se apaga, con recarga. Negativos: el conjunto que los agentes leen no se
// puede quitar, y un conjunto no puede guardarse en un archivo en el que magpie escribe.
//
// Lo que un navegador remoto no puede leer —el bloque escrito en el archivo de
// instrucciones del agente, las líneas propias del archivo conservadas y que la tarea
// REAL siguiente del agente (codex/omp del pin) lleve ese texto en su contexto— se
// verifica en el corte local contra la misma instalación:
//   work/migration-dev/mo13-library-instructions/flow.mjs
//
//   E2E_BASE_URL=http://127.0.0.1:19160 E2E_ACCESS_TOKEN_FILE=.../state/access.token \
//     bun run test tests/e2e/models-library-instructions-release.spec.ts

const AGENT = process.env.E2E_LIBRARY_AGENT ?? "codex";
const OTHER = process.env.E2E_LIBRARY_AGENT_OFF ?? "omp";
const NAME = `E2E instrucciones ${Date.now().toString(36)}`;

let guard: ConsoleGuard | null = null;

test.beforeEach(({ page }) => {
  requireRelease();
  guard = watchConsole(page);
});

test.afterEach(() => {
  expect(guard?.errors ?? [], "consola del navegador").toEqual([]);
});

test("MO-13: instrucciones nuevas de la Library para un agente", async ({ page }) => {
  await signIn(page, "/?section=models");
  const panel = page.frameLocator('iframe[title^="Magpie ·"]');
  await panel.locator("#nav").waitFor({ timeout: 30_000 });
  await libraryOn(panel);
  await openInstructions(panel);

  const before = await instructions(panel);
  const agent = before.agents.find((a) => a.agent === AGENT);
  const other = before.agents.find((a) => a.agent === OTHER);
  expect(agent, `la instalación reporta el archivo de instrucciones de ${AGENT}`).toBeTruthy();
  expect(other, `la instalación reporta el archivo de instrucciones de ${OTHER}`).toBeTruthy();
  const wasActive = before.sets.find((s) => s.active);
  const wasOn = { [AGENT]: agent?.on === true, [OTHER]: other?.on === true };

  // ── un conjunto nuevo, con su texto, y los agentes leen ése
  await panel.locator(".lib-setadd button.action", { hasText: "New set" }).click();
  await panel.locator('input[data-lib="newset"]').fill(NAME);
  await panel.locator(".lib-setnew button.primary", { hasText: "Add" }).click();
  const made = await setOf(panel, NAME);
  expect(made, "el conjunto nuevo queda en la biblioteca").toBeTruthy();
  await setRow(panel, NAME).locator("button.lib-radio").click();
  await expect.poll(async () => (await setOf(panel, NAME))?.active).toBe(true);
  await panel.locator(`textarea[data-lib="set:${made?.id}"]`).fill(`# ${NAME}\nEscrito por la prueba desde el panel nativo.`);
  await save(panel);
  expect((await setOf(panel, NAME))?.text).toContain(NAME);

  // ── negativo: el conjunto que los agentes leen no se puede quitar, y la página dice por qué
  const remove = (await setDetail(panel, NAME, made?.id ?? "")).locator("button", { hasText: "Remove" }).first();
  await expect(remove).toBeDisabled();
  await expect(remove).toHaveAttribute("title", /switch the agents to another set/i);

  // ── por agente: se enciende UNO, el otro queda como estaba. El switch es un
  // estado, no un clic: se pide el que se quiere para no invertir el que ya había.
  const switchTo = async (want: boolean) => {
    if ((await instructions(panel)).agents.find((a) => a.agent === AGENT)?.on !== want) {
      await agentRow(panel, AGENT, agent?.path ?? "").locator('button[role="switch"]').click();
    }
    await expect.poll(async () => (await instructions(panel)).agents.find((a) => a.agent === AGENT)?.on).toBe(want);
  };
  await switchTo(true);
  expect((await instructions(panel)).agents.find((a) => a.agent === OTHER)?.on).toBe(wasOn[OTHER]);
  await switchTo(false);

  // ── negativo: un conjunto no puede guardarse en el archivo del propio agente
  guard?.expected.push("/api/library/instructions/save"); // el 400 de este negativo es del caso
  await panel.locator(".lib-setadd button.action", { hasText: "New set" }).click();
  await panel.locator('input[data-lib="newset"]').fill(`${NAME} malo`);
  await panel.locator(".lib-setnew button.action", { hasText: "Keep in a file" }).click();
  await panel.locator('input[data-lib="newsetfile"]').fill(agent?.path ?? "");
  await panel.locator(".lib-setnew button.primary", { hasText: "Add" }).click();
  await expect(panel.locator("#status")).toContainText(/magpie writes its instructions into|own instructions/i);
  expect(await setOf(panel, `${NAME} malo`), "el conjunto inválido no se crea").toBeFalsy();
  await panel.locator(".lib-setnew button", { hasText: "Cancel" }).click();

  // ── un segundo conjunto no lo lee nadie hasta que los agentes se cambien; se quita
  await panel.locator(".lib-setadd button.action", { hasText: "New set" }).click();
  await panel.locator('input[data-lib="newset"]').fill(`${NAME} segundo`);
  await panel.locator(".lib-setnew button.primary", { hasText: "Add" }).click();
  const second = await setOf(panel, `${NAME} segundo`);
  expect(second?.active).toBeFalsy();
  expect((await setOf(panel, NAME))?.active).toBe(true);
  const secondDetail = await setDetail(panel, `${NAME} segundo`, second?.id ?? "");
  await secondDetail.locator("button", { hasText: "Remove" }).first().click();
  await secondDetail.locator("button", { hasText: "Remove" }).first().click();
  await expect.poll(async () => setOf(panel, `${NAME} segundo`)).toBeFalsy();

  // ── recargar conserva el conjunto y los agentes que lo leen
  await page.reload();
  const reloadedPanel = page.frameLocator('iframe[title^="Magpie ·"]');
  await reloadedPanel.locator("#nav").waitFor({ timeout: 30_000 });
  await openInstructions(reloadedPanel);
  expect((await setOf(reloadedPanel, NAME))?.active).toBe(true);

  // ── la instalación queda como estaba: vuelve el conjunto que estaba en uso, se quita
  // el de la prueba y los agentes recuperan su estado
  if (wasActive && wasActive.id !== made?.id) {
    await setRow(reloadedPanel, wasActive.name || "Default").locator("button.lib-radio").click();
    await expect.poll(async () => (await instructions(reloadedPanel)).sets.find((s) => s.active)?.id).toBe(wasActive.id);
  }
  for (const [id, on] of Object.entries(wasOn)) {
    const row = (await instructions(reloadedPanel)).agents.find((a) => a.agent === id);
    if (row?.on !== on) {
      await agentRow(reloadedPanel, id, row?.path ?? "").locator('button[role="switch"]').click();
      await expect.poll(async () => (await instructions(reloadedPanel)).agents.find((a) => a.agent === id)?.on).toBe(on);
    }
  }
  const mine = await setDetail(reloadedPanel, NAME, made?.id ?? "");
  await mine.locator("button", { hasText: "Remove" }).first().click();
  await mine.locator("button", { hasText: "Remove" }).first().click();
  await expect.poll(async () => setOf(reloadedPanel, NAME)).toBeFalsy();
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
  await row.locator('button[role="switch"]').click();
  await expect(panel.locator("#nav button", { hasText: "Library" }).first()).toBeVisible();
}

async function openInstructions(panel: FrameLocator) {
  await panel.locator("#nav button[data-view=library]").first().click();
  await openTab(panel, "Instructions");
  await expect(panel.locator("#view-library .list.lib-sets")).toBeVisible({ timeout: 30_000 });
}

const setRow = (panel: FrameLocator, name: string) =>
  panel.locator("#view-library .list.lib-sets .lib-set", { hasText: name }).first();

/** El detalle de un conjunto es el hermano siguiente de su fila, y queda abierto al
 *  crearlo: se busca primero y sólo si no está se abre con un clic. */
async function setDetail(panel: FrameLocator, name: string, id: string) {
  const detail = panel.locator(`.lib-detail:has(input[data-lib="setname:${id}"])`).first();
  if ((await detail.count()) === 0) await setRow(panel, name).click();
  await expect(detail).toBeVisible({ timeout: 10_000 });
  return detail;
}

/** La fila del agente se identifica por el archivo que la página muestra en ella. */
const agentRow = (panel: FrameLocator, id: string, path: string) =>
  panel.locator("#view-library .list.lib-list .lib-row", { hasText: path.split("/").slice(-2).join("/") || id }).first();

async function save(panel: FrameLocator) {
  await panel.locator("#view-library .lib-savebar:visible button.primary", { hasText: "Save" }).click();
  await expect(panel.locator("#view-library .lib-savebar:visible")).toHaveCount(0, { timeout: 30_000 });
}

interface AgentInstructions {
  agent: string;
  name: string;
  path: string;
  on: boolean;
  extra: string;
  own: number;
  edited?: boolean;
}
interface InstructionsView {
  shared: string;
  sets: { id: string; name: string; text: string; active?: boolean }[];
  agents: AgentInstructions[];
}

/** La autoridad del corte: el mismo cuerpo que la página nativa dibuja. */
async function instructions(panel: FrameLocator): Promise<InstructionsView> {
  return (await panel.locator("body").evaluate(async () =>
    (await fetch("/api/library", { headers: { accept: "application/json" } })).json(),
  )).instructions as InstructionsView;
}

const setOf = async (panel: FrameLocator, name: string) =>
  (await instructions(panel)).sets.find((s) => s.name === name);
