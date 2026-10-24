import { expect, test, type Locator, type Page, type Request } from "@playwright/test";
import { mkdir, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { requireRelease, signIn, watchConsole, type ConsoleGuard } from "./helpers";

// Persistent browser flows of M05 (Conversaciones, Fuentes, Tareas y ejemplo guiado)
// against the real release: no route interception, no stub and no skip. Every case talks
// to the actual Go server and fails when the flow it describes does not happen.
//
// Required mounts of the shell (coordinator owns App/nav):
//   ?section=conversations   → ConversationsSection (views: conversations, sources,
//                              example; the section renders its own view tabs too)
//   ?section=tasks           → TasksSection
// Override with E2E_CONVERSATIONS_ROUTE / E2E_TASKS_ROUTE when the shell mounts them
// elsewhere.
//
// Preconditions prepared by the coordinator on the instance under test (never defaults):
//   E2E_SOURCE_PATH      new fixture file/folder to register as a source
//   E2E_SOURCE_ALT_PATH  another copy of that fixture, for the relink case
//   E2E_SOURCE_ROOT      permitted test root that contains the fixture (discovery)
//   E2E_SOURCE_TITLE     substring of the fixture conversation title
//   E2E_SOURCE_ADAPTER   optional; defaults to canonical
// CV-05 limits of the capture (the case writes its own broken file and corrects it):
//   E2E_CV05_FIX_DIR     writable directory the case owns; it writes cv05-partial.jsonl
//   E2E_CV05_LIMIT_PATH  file bigger than the published 64 MiB per-file capture limit
//   E2E_CV05_VALID_PATH  small canonical file that captures completely
//   E2E_CV05_VALID_TITLE substring of that file's conversation title
// CV-09 adapter samples (one new file per adapter, all prepared by the coordinator):
//   E2E_CV09_DIR         directory holding <adapter>.jsonl for canonical/codex/claude/pi/omp
//   E2E_CV09_EDIT_DIR    writable directory the invalidation case owns (writes codex.jsonl)
//   E2E_CV07_EDIT_DIR    writable directory the pagination case owns (writes cv07-paged.jsonl)
//   E2E_CV06_DIR         writable directory the list cases own: two files (cv06-complete.jsonl
//                        canonical, cv06-partial.jsonl codex broken on purpose) and page/, the
//                        folder of 51 captures the pagination case registers and scans
//   E2E_TK03_DIR         writable directory the history case owns (writes tk03-anchor.jsonl
//                        and tk03-volume.jsonl canonical, both registered and scanned by it)
//   E2E_CV01_DIR         writable directory the boundary case owns: it writes a mixed tree with
//                        auth/, credentials/, .ssh/ and .opencode/ plus the allowed captures
//   E2E_FO08_PROJECT_DIR  absolute project directory of the case fixture (a git tree that
//   E2E_FO08_VERIFIER_DIR its external verifier accepts unchanged and rejects after the fault)
//   E2E_FO08_DOMAIN       domain of the case
//   E2E_FO08_SOURCE_DIR   writable directory the FO08 case owns (its own capture to task)
//   E2E_FO07_DIR          writable directory the FO07 case owns: it writes there its own
//                         capture, project and external oracles (a verifier that repairs,
//                         one that injects the fault and one that never detects it)
// The fixture conversation must contain a literal markup-looking line and be captured
// twice (append + rescan) so at least two immutable revisions exist.
// A missing precondition throws: it is never silently skipped.
//
//   E2E_BASE_URL=https://workbench.example E2E_ACCESS_TOKEN=... \
//     bun run test tests/e2e/conversations-tasks-release.spec.ts

const CONVERSATIONS_ROUTE = process.env.E2E_CONVERSATIONS_ROUTE ?? "/?section=conversations";
const TASKS_ROUTE = process.env.E2E_TASKS_ROUTE ?? "/?section=tasks";

const errors: string[] = [];
let guard: ConsoleGuard | null = null;

test.beforeEach(() => {
  errors.length = 0;
  guard = null;
});

test.afterEach(() => {
  expect(guard?.errors ?? [], "consola del navegador").toEqual([]);
});

/** Deep link of one screen: the section's views live in ?view= of the shared route. */
function route(base: string, view?: string, params: Record<string, string> = {}) {
  const url = new URL(base, "http://workbench.invalid");
  if (view) url.searchParams.set("view", view);
  for (const [key, value] of Object.entries(params)) url.searchParams.set(key, value);
  return url.pathname + url.search;
}

function query(url: string, key: string) {
  return new URL(url).searchParams.get(key);
}

function required(name: string) {
  const value = process.env[name];
  if (!value)
    throw new Error(
      `${name} is not set: prepare that fixture on the instance under test (no default, no fallback).`,
    );
  if (!name.endsWith("_TITLE") && !value.startsWith("/"))
    throw new Error(`${name} must be an absolute server path, got "${value}".`);
  return value;
}

interface SourceRow {
  id: string;
  path: string;
  adapter: string;
  enabled: boolean;
  coverage?: SourceCoverage;
  last_scan?: { status?: string; files?: number; errors?: number; limited?: boolean };
  error?: string;
}

/** One capture issue as the installation persists it: file, line and reason. */
interface CaptureIssueRow {
  line?: number;
  file_name?: string;
  reason?: string;
  severity?: string;
  retained?: boolean;
}

interface SourceCoverage {
  status?: string;
  event_count?: number;
  issue_count?: number;
  blocking_issue_count?: number;
  semantic_issue_count?: number;
  issues?: CaptureIssueRow[];
}

interface ConversationRow {
  id: string;
  source_id?: string;
  path?: string;
  title?: string;
  revision?: string;
  coverage?: { status?: string; event_count?: number };
}

/** Receipt of one task proposal, as the routes publish it (frozen 003 contract). */
interface ProposalReceipt {
  job_id?: string;
  status?: string;
  suggestions?: {
    title: string;
    first_event: number;
    last_event: number;
    evidence?: { event_index: number; quote: string }[];
  }[];
  skipped?: { reason?: string }[];
  inference?: {
    runs?: {
      provider?: string;
      model?: string;
      real_inference?: boolean;
      tool_calls?: number;
    }[];
  };
  progress?: { completed_chunks?: number; total_chunks?: number };
}

interface TaskRow {
  id: string;
  title?: string;
  session_id: string;
  revision: string;
  status?: string;
  first_event: number;
  last_event: number;
  citations?: { event_index: number; quote: string }[];
  completed?: boolean;
  label?: string;
  completion_generation?: number;
  completion_confirmed_at?: number | string;
  created?: number | string;
  updated?: number | string;
  feedback?: string;
  /** Casos de evaluación que el detalle publica como selección vigente de la tarea. */
  cases?: { id: string; title?: string; domain?: string; split?: string }[];
  cases_generation?: number;
}

interface RevisionHeader {
  id?: string;
  revision?: string;
  is_latest?: boolean;
  event_count: number;
}

/** One read of a route of this release. The body is JSON of an external service, so the
 *  shape is asserted by the checks that follow and never trusted member by member. */
async function read<T extends object>(page: Page, path: string) {
  const response = await page.request.get(path);
  const body: unknown = await response.json();
  // Boundary cast of this spec: the release under test answers the frozen contract, and
  // every field read below is an assertion target of the case that reads it.
  const parsed = (body && typeof body === "object" ? body : {}) as T;
  return { ...parsed, httpStatus: response.status() };
}

async function sources(page: Page) {
  const reply = await read<{ sources?: SourceRow[] }>(page, "/api/conversations/sources");
  expect(reply.httpStatus, "GET /api/conversations/sources").toBe(200);
  return reply.sources ?? [];
}

async function conversations(page: Page, queryString: string) {
  const reply = await read<{ sessions?: ConversationRow[] }>(
    page,
    `/api/conversations?${queryString}`,
  );
  expect(reply.httpStatus, `GET /api/conversations?${queryString}`).toBe(200);
  return reply.sessions ?? [];
}

async function tasks(page: Page, queryString = "") {
  const reply = await read<{ tasks?: TaskRow[] }>(page, `/api/tasks?${queryString}`);
  expect(reply.httpStatus, `GET /api/tasks?${queryString}`).toBe(200);
  return reply.tasks ?? [];
}

async function proposalOf(page: Page, sessionId: string, revision: string) {
  const response = await page.request.get(
    `/api/tasks/proposals/${encodeURIComponent(sessionId)}?revision=${encodeURIComponent(revision)}`,
  );
  expect([200, 404], "GET /api/tasks/proposals").toContain(response.status());
  const body: unknown = await response.json();
  // Boundary of this read: the route answers the frozen receipt shape of revision 003.
  return (body && typeof body === "object" ? body : {}) as ProposalReceipt;
}

async function taskById(page: Page, id: string) {
  const reply = await read<TaskRow>(page, `/api/tasks/${encodeURIComponent(id)}`);
  expect(reply.httpStatus, `GET /api/tasks/${id}`).toBe(200);
  return reply;
}

/** Registering a fixture is a real UI flow; on a rerun the row may already exist, so
 *  the assertion is the row itself and not the success notice. */
async function registerSourcePath(page: Page, path: string, adapter?: string) {
  if ((await sources(page)).some((source) => source.path === path)) return;
  // A fresh installation shows the header action and the empty-state action with the same
  // label; both open the same dialog, so the first one is the stable target.
  await page.getByRole("button", { name: "Añadir fuente", exact: true }).first().click();
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("Ruta o archivo de la fuente").fill(path);
  await dialog.getByLabel("Adaptador").selectOption(adapter ?? process.env.E2E_SOURCE_ADAPTER ?? "canonical");
  await dialog.getByRole("button", { name: "Registrar fuente", exact: true }).click();
  await expect(dialog).toBeHidden();
}

async function registerFixture(page: Page) {
  await registerSourcePath(page, required("E2E_SOURCE_PATH"));
}

/** One manual scan of a source, followed to its terminal state as the screen reports it. */
async function scanSource(page: Page, row: Locator, terminal: "Terminada" | "Falló") {
  const request = page.waitForResponse(
    (response) => response.request().method() === "POST" && response.url().endsWith("/scan"),
  );
  await row.getByRole("button", { name: "Escanear", exact: true }).click();
  const response = await request;
  expect(response.status()).toBe(202);
  const { job_id } = (await response.json()) as { job_id: string };
  const job = page.locator('[data-slot="card"]').filter({ hasText: "Captura en curso" });
  await expect(job.getByText(terminal, { exact: true })).toBeVisible({ timeout: 60_000 });
  return { job, jobId: job_id };
}

interface JobReceipt {
  status?: string;
  result?: { files?: number; changed?: number; errors?: number; sessions?: number };
}

/** Receipt of one capture job as the installation persists it. */
async function jobReceipt(page: Page, jobId: string) {
  const reply = await read<JobReceipt>(page, `/api/foundry/jobs/${encodeURIComponent(jobId)}`);
  expect(reply.httpStatus, `GET /api/foundry/jobs/${jobId}`).toBe(200);
  return reply;
}

/** One adapter change through the screen: the relink dialog keeps the path and only
 *  switches the adapter of the source. */
async function changeAdapter(page: Page, path: string, adapter: string) {
  await sourceRow(page, path).getByRole("button", { name: "Cambiar ruta", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("Adaptador").selectOption(adapter);
  await dialog.getByRole("button", { name: "Guardar cambio", exact: true }).click();
  await expect(dialog).toBeHidden();
  await expect.poll(async () => (await storedSource(page, path)).adapter).toBe(adapter);
}

/** The persisted row of one source: the screen must reflect this body, never its own copy. */
async function storedSource(page: Page, path: string) {
  const found = (await sources(page)).find((source) => source.path === path);
  if (!found) throw new Error(`the installation has no source at ${path}`);
  return found;
}

/** One source row of the panel: the deepest container of its own path text that also owns
 *  its scan control, so a second source of the installation cannot answer for the first. */
function sourceRow(page: Page, path: string) {
  return page
    .locator("div")
    .filter({ hasText: path })
    .filter({ has: page.getByRole("button", { name: "Escanear", exact: true }) })
    .last();
}

/** The row of one source: the panel card holds every row, so the row itself is the scope. */
function sourceCard(page: Page, path: string) {
  return sourceRow(page, path);
}

/** The conversation of the fixture, found by the title the coordinator prepared. */
function fixtureRow(page: Page) {
  return page.locator("tbody tr").filter({ hasText: required("E2E_SOURCE_TITLE") }).first();
}

test("CV-01/03/05: una fuente nueva se registra, se escanea por cola y se pausa sin saltarse", async ({
  page,
}) => {
  requireRelease();
  guard = watchConsole(page);
  await signIn(page);
  await page.goto(route(CONVERSATIONS_ROUTE, "sources"));

  await registerFixture(page);
  const row = sourceCard(page, required("E2E_SOURCE_PATH"));
  await expect(row.getByText("activa", { exact: true }).first()).toBeVisible();

  // A scan is queued: the screen follows the job until a terminal state.
  const scanRequest = page.waitForResponse(
    (response) => response.request().method() === "POST" && response.url().endsWith("/scan"),
  );
  await row.getByRole("button", { name: "Escanear", exact: true }).click();
  const scanResponse = await scanRequest;
  expect(scanResponse.status()).toBe(202);
  const scan = (await scanResponse.json()) as { job_id: string };
  const job = page.locator('[data-slot="card"]').filter({ hasText: "Captura en curso" });
  await expect(job).toBeVisible();
  await expect(
    job.getByText(/^(En cola|En curso|Terminada|Falló|Cancelada|Interrumpida)$/).first(),
  ).toBeVisible();
  await expect(job.getByText("Terminada", { exact: true })).toBeVisible({ timeout: 45_000 });
  const scanResult = await page.request.get("/api/foundry/jobs/" + encodeURIComponent(scan.job_id));
  expect(scanResult.status()).toBe(200);
  const receipt = (await scanResult.json()) as {
    status: string;
    result?: { sessions?: number };
  };
  expect(receipt.status).toBe("succeeded");
  // The same paragraph also states the coverage issues of the capture, so the session
  // count is compared as the part it must contain, not as the whole line.
  await expect(job.getByText("Sesiones capturadas:", { exact: false })).toContainText(
    "Sesiones capturadas: " + (receipt.result?.sessions ?? "sin dato"),
  );

  // Pausing persists in the installation and a paused source is not scanned from here:
  // a pause is never bypassed by a manual action of this screen.
  await row.getByRole("button", { name: "Pausar", exact: true }).click();
  await expect(
    sourceCard(page, required("E2E_SOURCE_PATH")).getByText("pausada", { exact: true }).first(),
  ).toBeVisible();
  await page.reload();
  const paused = sourceCard(page, required("E2E_SOURCE_PATH"));
  await expect(paused.getByText("pausada", { exact: true }).first()).toBeVisible();
  await expect(paused.getByRole("button", { name: "Escanear", exact: true })).toBeDisabled();

  await paused.getByRole("button", { name: "Activar", exact: true }).click();
  await expect(
    sourceCard(page, required("E2E_SOURCE_PATH")).getByText("activa", { exact: true }).first(),
  ).toBeVisible();
  const stored = (await sources(page)).find((item) => item.path === required("E2E_SOURCE_PATH"));
  expect(stored?.enabled, "la instalación conserva la fuente activa").toBe(true);
});

test("CV-02: el descubrimiento sólo lee las raíces escritas y no registra nada", async ({
  page,
}) => {
  requireRelease();
  guard = watchConsole(page);
  await signIn(page);
  await page.goto(route(CONVERSATIONS_ROUTE, "sources"));

  // Negative: no root, no scan at all.
  await page.getByRole("button", { name: "Detectar fuentes", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText("no escanea ningún HOME");
  await dialog.getByRole("button", { name: "Detectar", exact: true }).click();
  await expect(dialog.getByRole("alert")).toContainText("al menos una raíz");

  const before = (await sources(page)).map((item) => item.path);
  await dialog.getByLabel("Raíz 1").fill(required("E2E_SOURCE_ROOT"));
  const request = page.waitForRequest(
    (candidate) =>
      candidate.method() === "POST" &&
      candidate.url().includes("/api/conversations/sources/discover"),
  );
  await dialog.getByRole("button", { name: "Detectar", exact: true }).click();
  const sent = (await request).postDataJSON() as { roots?: { path: string }[] };
  expect(sent.roots?.map((root) => root.path), "sólo la raíz escrita").toEqual([
    required("E2E_SOURCE_ROOT"),
  ]);
  await expect(dialog).toBeHidden();

  const panel = page.locator('[data-slot="card"]').filter({ hasText: "Raíces detectadas" });
  await expect(panel).toBeVisible();
  expect(
    (await sources(page)).map((item) => item.path),
    "detectar por sí solo no registra fuentes",
  ).toEqual(before);

  // Registering is a separate, explicit confirmation of one detected candidate.
  const candidate = panel.getByRole("button", { name: "Registrar fuente", exact: true }).first();
  if (await candidate.isVisible()) {
    await candidate.click();
    await expect(sourceCard(page, required("E2E_SOURCE_PATH"))).toBeVisible();
  } else {
    await expect(panel).toContainText("No se encontraron conversaciones");
  }
});

test("CV-04: cambiar la ruta conserva identificadores y no reinterpreta la evidencia", async ({
  page,
}) => {
  requireRelease();
  guard = watchConsole(page);
  await signIn(page);
  await page.goto(route(CONVERSATIONS_ROUTE, "sources"));

  await registerFixture(page);
  const path = required("E2E_SOURCE_PATH");
  const before = (await sources(page)).find((item) => item.path === path);
  expect(before?.id, "la fuente del fixture existe").toBeTruthy();
  const sessionsBefore = await conversations(page, `source_id=${before?.id ?? ""}`);

  // Negative: an unchanged destination is refused by the form and the row keeps its path.
  await sourceCard(page, path).getByRole("button", { name: "Cambiar ruta", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("button", { name: "Guardar cambio", exact: true }).click();
  await expect(dialog.getByRole("alert")).toContainText("ruta nueva");
  await dialog.getByRole("button", { name: "Cancelar", exact: true }).click();
  await expect(sourceCard(page, path)).toBeVisible();
  expect((await sources(page)).find((item) => item.path === path)?.id).toBe(before?.id);

  // The real move: same source id, same captured sessions and their citations.
  await sourceCard(page, path).getByRole("button", { name: "Cambiar ruta", exact: true }).click();
  await page.getByRole("dialog").getByLabel("Nueva ruta").fill(required("E2E_SOURCE_ALT_PATH"));
  await page.getByRole("dialog").getByRole("button", { name: "Guardar cambio", exact: true }).click();
  await expect(page.getByRole("dialog")).toBeHidden();
  const moved = (await sources(page)).find((item) => item.path === required("E2E_SOURCE_ALT_PATH"));
  expect(moved?.id, "el identificador de la fuente se conserva").toBe(before?.id);
  const sessionsAfter = await conversations(page, `source_id=${before?.id ?? ""}`);
  expect(
    sessionsAfter.map((row) => row.id).sort(),
    "las sesiones capturadas siguen siendo las mismas",
  ).toEqual(sessionsBefore.map((row) => row.id).sort());

  // Leave the fixture where the other cases expect it.
  await sourceCard(page, required("E2E_SOURCE_ALT_PATH"))
    .getByRole("button", { name: "Cambiar ruta", exact: true })
    .click();
  await page.getByRole("dialog").getByLabel("Nueva ruta").fill(path);
  await page.getByRole("dialog").getByRole("button", { name: "Guardar cambio", exact: true }).click();
  await expect(sourceCard(page, path)).toBeVisible();
});

test("CV-06/07: la lista filtra, la conversación se abre y el transcript es paginado e inerte", async ({
  page,
}) => {
  requireRelease();
  guard = watchConsole(page);
  guard.expected.push("/api/tasks/proposals/");
  const dialogs: string[] = [];
  page.on("dialog", (dialog) => {
    dialogs.push(dialog.message());
    void dialog.dismiss();
  });
  await signIn(page);
  await page.goto(route(CONVERSATIONS_ROUTE, "conversations"));

  const title = required("E2E_SOURCE_TITLE");
  await page.getByLabel("Buscar conversación").fill(title);
  await expect
    .poll(() => query(page.url(), "q"), { timeout: 15_000 })
    .toBe(title);
  const row = fixtureRow(page);
  await expect(row).toBeVisible();

  await row.getByRole("button", { name: "Abrir", exact: true }).click();
  await expect(page.getByRole("button", { name: "Volver a la lista", exact: true })).toBeVisible();
  await expect.poll(() => query(page.url(), "conversation")).toBeTruthy();
  // The detail comes from the {conversation, revisions} envelope: the section must not
  // fall into its boundary and must show the real title and the captured events.
  await expect(
    page.getByText("No se pudo abrir esta sección", { exact: false }),
    "la sección no cae en el boundary al abrir una conversación",
  ).toHaveCount(0);
  await expect(page.getByRole("heading", { name: title, exact: false }).first()).toBeVisible();

  const events = page.locator('article[id^="event-"]');
  await expect(events.first()).toBeVisible();
  const stored0 = await conversations(page, `q=${encodeURIComponent(title)}`);
  const eventCount = (stored0[0]?.revision ? 4 : 0);
  await expect.poll(async () => events.count(), { timeout: 15_000 }).toBeGreaterThanOrEqual(
    Math.max(1, eventCount),
  );
  const stored = await conversations(page, `q=${encodeURIComponent(title)}`);
  const conversation = stored[0];
  const pageBody = await read<{ events?: { event_index: number; text?: string }[] }>(
    page,
    `/api/conversations/${encodeURIComponent(conversation?.id ?? "")}/revisions/${encodeURIComponent(conversation?.revision ?? "")}?limit=100&compact=1`,
  );
  expect(pageBody.httpStatus).toBe(200);
  expect((pageBody.events ?? []).length).toBeGreaterThan(0);

  // Captured markup stays text: no dialog fired and no node was injected.
  const markup = events.filter({ hasText: "onerror" }).first();
  if (await markup.isVisible()) {
    expect(await markup.locator("img").count()).toBe(0);
    expect(await page.locator('script:has-text("onerror")').count()).toBe(0);
  }
  expect(dialogs, "ningún contenido capturado abrió un diálogo").toEqual([]);

  // The compact read is what the screen shows until full text is asked for.
  const full = page.getByRole("checkbox", { name: "Texto completo" });
  await expect(full).not.toBeChecked();
  await full.check();
  await expect(events.first()).toBeVisible();

  const more = page.getByRole("button", { name: "Cargar más eventos", exact: true });
  if (await more.isEnabled()) {
    const count = await events.count();
    await more.click();
    await expect.poll(async () => events.count(), { timeout: 20_000 }).toBeGreaterThan(count);
  }
});

test("CV-08/10: una revisión histórica se muestra como histórica y no mezcla tareas", async ({
  page,
}) => {
  requireRelease();
  guard = watchConsole(page);
  guard.expected.push("/api/tasks/proposals/");
  await signIn(page);
  await page.goto(route(CONVERSATIONS_ROUTE, "conversations"));

  const stored = await conversations(page, `q=${encodeURIComponent(required("E2E_SOURCE_TITLE"))}`);
  const conversation = stored[0];
  expect(conversation?.id, "el fixture está capturado").toBeTruthy();
  const detail = await read<{ revisions?: { revision: string }[] }>(
    page,
    `/api/conversations/${encodeURIComponent(conversation?.id ?? "")}`,
  );
  expect(detail.httpStatus).toBe(200);
  const revisions = detail.revisions ?? [];
  expect(
    revisions.length,
    "el fixture necesita dos capturas (añade eventos y vuelve a escanear) para esta prueba",
  ).toBeGreaterThan(1);

  await page.goto(
    route(CONVERSATIONS_ROUTE, "conversations", { conversation: conversation?.id ?? "" }),
  );
  const selector = page.getByLabel("Revisión");
  await expect(selector).toBeVisible();
  expect(await selector.locator("option").count()).toBeGreaterThan(1);

  // The newest revision is the current one; an older one is stated as historical and the
  // deep link keeps it, so the same evidence can be reopened later.
  const older = revisions[revisions.length - 1]?.revision ?? "";
  await selector.selectOption(older);
  await expect(page.getByText("Revisión histórica: muestra la captura de esa fecha")).toBeVisible();
  await expect.poll(() => query(page.url(), "revision")).toBe(older);
  await page.reload();
  await expect(page.getByText("Revisión histórica: muestra la captura de esa fecha")).toBeVisible();

  const historical = await read<{ conversation?: RevisionHeader; tasks?: TaskRow[] }>(
    page,
    `/api/conversations/${encodeURIComponent(conversation?.id ?? "")}/revisions/${encodeURIComponent(older)}?limit=100`,
  );
  expect(historical.httpStatus).toBe(200);
  expect(historical.conversation?.revision).toBe(older);
  expect(historical.conversation?.is_latest).toBe(false);
  expect(
    (historical.tasks ?? []).every((task) => task.revision === older),
    "las tareas de una revisión nunca se mezclan con las de otra",
  ).toBe(true);
});

test("CV-08: una revisión ausente da 404 y se recupera seleccionando la actual", async ({ page }) => {
  requireRelease();
  guard = watchConsole(page);
  await signIn(page);
  const conversation = (await conversations(page, `q=${encodeURIComponent(required("E2E_SOURCE_TITLE"))}`))[0];
  const id = conversation?.id ?? "";
  expect(id).not.toBe("");
  const missing = `m05-missing-${Date.now()}`;
  const absent = await read<{ code?: string }>(page, `/api/conversations/${missing}`);
  expect(absent.httpStatus).toBe(404);
  expect(absent.code).toBe("conversation_not_found");
  const detail = await read<{ revisions?: { revision: string }[] }>(page, `/api/conversations/${encodeURIComponent(id)}`);
  expect(detail.httpStatus).toBe(200);
  const latest = detail.revisions?.[0]?.revision ?? "";
  expect(latest).not.toBe("");
  const missingRevision = `m05-missing-revision-${Date.now()}`;
  const badPath = `/api/conversations/${encodeURIComponent(id)}/revisions/${missingRevision}`;
  const invalid = await read<{ code?: string }>(page, `${badPath}?limit=100`);
  expect(invalid.httpStatus).toBe(404);
  expect(invalid.code).toBe("revision_not_found");
  guard.expected.push("/api/tasks/proposals/", badPath);
  await page.goto(route(CONVERSATIONS_ROUTE, "conversations", { conversation: id, revision: missingRevision }));
  await expect(page.getByText("No se pudo leer esta vista", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Reintentar", exact: true })).toBeVisible();
  await page.getByLabel("Revisión").selectOption(latest);
  await expect.poll(() => query(page.url(), "revision")).toBe(latest);
  await expect(page.getByLabel("Revisión")).toHaveValue(latest);
  await expect(page.getByText("No se pudo leer esta vista", { exact: true })).toHaveCount(0);
  await expect(page.locator('article[id^="event-"]').first()).toBeVisible();
});

test("TK-01/02: el rango se guarda con sus citas, se repite sin duplicar y el solape se rechaza", async ({
  page,
}) => {
  requireRelease();
  guard = watchConsole(page);
  guard.expected.push("/api/tasks");
  await signIn(page);
  await page.goto(route(CONVERSATIONS_ROUTE, "conversations"));

  const stored = await conversations(page, `q=${encodeURIComponent(required("E2E_SOURCE_TITLE"))}`);
  const conversation = stored[0];
  expect(conversation?.id).toBeTruthy();
  const revision = conversation?.revision ?? "";
  const sessionQuery = `session_id=${encodeURIComponent(conversation?.id ?? "")}`;
  const revisionBody = await read<{
    conversation?: RevisionHeader;
    events?: { event_index: number; role?: string; text?: string }[];
  }>(
    page,
    `/api/conversations/${encodeURIComponent(conversation?.id ?? "")}/revisions/${encodeURIComponent(revision)}?limit=100&compact=1`,
  );
  const events = revisionBody.events ?? [];
  expect(revisionBody.conversation?.is_latest, "la revisión elegida es la actual").toBe(true);
  expect(events.length, "la revisión más reciente del fixture tiene eventos").toBeGreaterThan(1);
  const eventCount = revisionBody.conversation?.event_count;
  expect(eventCount, "total de la revisión, no tamaño de página").toBeGreaterThanOrEqual(events.length);

  // A task must start with a non-empty human request (backend rule), so every range is
  // taken from the captured events. The authoritative task list decides whether the
  // range is new or already stored, so this case holds on a reused instance too.
  const human = events
    .filter((row) => row.role === "user" && (row.text ?? "").trim().length > 0)
    .map((row) => row.event_index);
  expect(human.length, "el fixture necesita dos peticiones humanas para comprobar solape").toBeGreaterThanOrEqual(2);
  const before = await tasks(page, sessionQuery);
  const covered = (index: number) =>
    before.some((task) => task.first_event <= index && task.last_event >= index);
  const start1 = human[0];

  await page.goto(
    route(CONVERSATIONS_ROUTE, "conversations", {
      conversation: conversation?.id ?? "",
      revision,
    }),
  );

  // Negative: an end beyond the revision cannot be saved.
  await page.getByLabel("Fin", { exact: true }).fill(String(eventCount! + 5));
  await expect(page.getByRole("button", { name: "Guardar tarea", exact: true })).toBeDisabled();
  await expect(page.getByRole("alert")).toContainText("no puede pasar del");

  await page.getByLabel("Inicio", { exact: true }).fill(String(start1 + 1));
  await page.getByLabel("Fin", { exact: true }).fill(String(start1 + 1));
  if (!covered(start1)) {
    await page.getByLabel("Nombre de la tarea").fill(`Prueba M05 ${Date.now()}`);
    await page.getByRole("button", { name: "Guardar tarea", exact: true }).click();
    await expect(page.getByText("Tarea guardada.")).toBeVisible();
  }

  const created = await tasks(page, sessionQuery);
  const task = created.find(
    (row) => row.first_event === start1 && row.last_event === start1,
  );
  expect(task?.id, "la tarea quedó guardada con su rango exacto").toBeTruthy();
  expect(task?.revision).toBe(revision);
  const citation = task?.citations?.[0];
  expect(citation?.event_index, "la cita del rango quedó fijada con su índice").toBe(start1);
  expect((citation?.quote ?? "").length, "la cita conserva su texto").toBeGreaterThan(0);

  // Repeating the same range reuses the task instead of duplicating it, whichever way the
  // first save went; the stored citations are the ones the server answered with.
  await page.getByLabel("Inicio", { exact: true }).fill(String(start1 + 1));
  await page.getByLabel("Fin", { exact: true }).fill(String(start1 + 1));
  await page.getByRole("button", { name: "Guardar tarea", exact: true }).click();
  await expect(page.getByText("La tarea ya existía; se conservó su valoración.")).toBeVisible();
  expect(
    (await tasks(page, sessionQuery)).filter(
      (row) => row.first_event === start1 && row.last_event === start1,
    ).length,
    "el mismo rango no se duplica",
  ).toBe(1);

  const other = human[1];
  if (!covered(other)) {
    await page.getByLabel("Inicio", { exact: true }).fill(String(other + 1));
    await page.getByLabel("Fin", { exact: true }).fill(String(other + 1));
    const count = (await tasks(page, sessionQuery)).length;
    await page.getByRole("button", { name: "Guardar tarea", exact: true }).click();
    await expect(page.getByText("Tarea guardada.")).toBeVisible();
    expect((await tasks(page, sessionQuery)).length, "un rango disjunto se guarda una sola vez").toBe(count + 1);
  }

  const countBefore = (await tasks(page, sessionQuery)).length;
  await page.getByLabel("Inicio", { exact: true }).fill(String(start1 + 1));
  await page.getByLabel("Fin", { exact: true }).fill(String(other + 1));
  const overlap = page.waitForResponse((response) =>
    new URL(response.url()).pathname === "/api/tasks" && response.request().method() === "POST",
  );
  await page.getByRole("button", { name: "Guardar tarea", exact: true }).click();
  expect((await overlap).status(), "el servidor rechaza el solape").toBe(409);
  await expect(page.getByText("El rango se solapa con otra tarea de esta revisión.")).toBeVisible();
  expect((await tasks(page, sessionQuery)).length, "el solape no crea tarea").toBe(countBefore);
});

test("TK-03: el histórico filtra, abre la tarea exacta y sigue la cita hasta su evento", async ({
  page,
}) => {
  requireRelease();
  guard = watchConsole(page);
  guard.expected.push("/api/tasks/proposals/");
  await signIn(page);
  await page.goto(route(TASKS_ROUTE));

  await page.getByRole("button", { name: "Sin completar", exact: true }).click();
  const incomplete = await tasks(page, "status=incomplete");
  expect(incomplete.length, "hay tareas sin completar del fixture").toBeGreaterThan(0);
  const listed = incomplete[0];

  const row = page
    .locator("tbody tr")
    .filter({ hasText: listed.title ?? listed.id })
    .first();
  await expect(row).toBeVisible();
  await row.getByRole("button", { name: "Revisar tarea", exact: true }).click();
  await expect.poll(() => query(page.url(), "task")).toBeTruthy();
  const openedId = query(page.url(), "task") ?? "";
  const stored = await taskById(page, openedId);
  expect(stored.id).toBe(openedId);
  const citation = stored.citations?.[0];
  expect(citation?.event_index, "la tarea conserva sus citas").not.toBeUndefined();
  await expect(page.getByRole("button", { name: "Seguir la cita", exact: true })).toHaveCount(
    stored.citations?.length ?? 0,
  );

  // The citation leads to the exact revision and event, and the task history stays.
  await page.getByRole("button", { name: "Seguir la cita", exact: true }).first().click();
  await expect.poll(() => query(page.url(), "conversation")).toBe(stored.session_id);
  await expect.poll(() => query(page.url(), "revision")).toBe(stored.revision);
  await expect.poll(() => query(page.url(), "event")).toBe(String(citation?.event_index));
  const focused = page.locator(`article#event-${citation?.event_index}`);
  await expect(focused).toBeVisible({ timeout: 20_000 });
  await expect(focused).toHaveAttribute("aria-current", "true");
  await expect(focused).toContainText((citation?.quote ?? "").slice(0, 24));
});

test("TK-04/05: los dos gates humanos, su generación y el rechazo de valorar sin completar", async ({
  page,
}) => {
  requireRelease();
  guard = watchConsole(page);
  await signIn(page);

  const pending = await tasks(page, "status=pending");
  const incomplete = await tasks(page, "status=incomplete");
  const candidate = pending[0] ?? incomplete[0];
  expect(candidate?.id, "hay una tarea del fixture para los gates").toBeTruthy();
  const id = candidate?.id ?? "";

  await page.goto(route(TASKS_ROUTE, undefined, { task: id }));
  await expect(page.getByRole("button", { name: "Volver al histórico", exact: true })).toBeVisible();
  const labelSelect = page.getByLabel("Valoración");

  // Gate 2 cannot run before gate 1.
  if (candidate?.completed !== true) {
    await expect(labelSelect).toBeDisabled();
    await expect(page.getByText("Confirma primero que la tarea está completada.")).toBeVisible();
    await page.getByRole("button", { name: "Marcar tarea completada", exact: true }).click();
    await expect(
      page.getByText("Finalización confirmada. Ahora puedes valorar el resultado."),
    ).toBeVisible();
  }
  const completed = await taskById(page, id);
  expect(completed.completed, "la finalización quedó confirmada").toBe(true);
  const generation = completed.completion_generation;
  await expect(labelSelect).toBeEnabled();

  await labelSelect.selectOption("correct");
  await page.getByLabel("Qué funcionó o qué falló").fill("Comprobación real desde el navegador");
  await page.getByRole("button", { name: "Guardar valoración", exact: true }).click();
  await expect(page.getByText("Valoración guardada.")).toBeVisible();
  const labelled = await taskById(page, id);
  expect(labelled.label).toBe("correct");
  expect(labelled.feedback).toContain("Comprobación real");
  expect(
    labelled.completion_generation,
    "valorar no cambia la generación de finalización",
  ).toBe(generation);
  await expect(labelSelect).toHaveValue("correct");

  // Reopening revokes the valuation, returns the task to pending and is durable.
  await page.getByRole("button", { name: "Reabrir tarea", exact: true }).click();
  await expect(page.getByText(/Tarea reabierta/)).toBeVisible();
  await page.reload();
  await expect(page.getByLabel("Valoración")).toBeDisabled();
  await expect(page.getByLabel("Valoración")).toHaveValue("pending");
  const reopened = await taskById(page, id);
  expect(reopened.completed).toBe(false);
  expect(reopened.label).toBe("pending");
  expect(reopened.feedback, "reabrir retira la valoración y su nota").toBe("");
  expect(
    reopened.completion_generation,
    "reabrir sí avanza la generación",
  ).not.toBe(generation);
});

test("CV-12: el ejemplo guiado ejecuta sus controles y deja artefactos reales de esta instancia", async ({
  page,
}) => {
  requireRelease();
  guard = watchConsole(page);
  // Opening the conversation of the example reads its proposals: a 404 there is the absence
  // of a proposal for that revision, not a failure of this case.
  guard.expected.push("/api/tasks/proposals/");
  await signIn(page);
  await page.goto(route(CONVERSATIONS_ROUTE, "example"));

  const state = await read<{ available?: boolean; reason?: string }>(
    page,
    "/api/foundry/example",
  );
  expect(state.httpStatus, "GET /api/foundry/example").toBe(200);
  const button = page.getByRole("button", { name: "Preparar y ejecutar el ejemplo", exact: true });
  await expect(button).toBeVisible();

  // A resource that is not published is stated and never replaced by an empty demo; with
  // the resource published the rest of the case has to reach the real artifacts.
  if (state.available !== true) {
    await expect(button).toBeDisabled();
    expect(
      (state.reason ?? "").length,
      "un recurso ausente se explica, no se sustituye por una demo vacía",
    ).toBeGreaterThan(0);
    await expect(page.getByRole("status").first()).toContainText(state.reason ?? "");
    return;
  }

  const pending = page.waitForResponse(
    (response) =>
      response.url().endsWith("/api/foundry/example") && response.request().method() === "POST",
  );
  await button.click();
  const response = await pending;
  const receipt = (await response.json()) as {
    status?: string;
    session_id?: string;
    task_id?: string;
    checks?: { name: string; status: string; expected?: string; observed?: string }[];
    error?: string;
  };

  // Negative path first, and it never closes the positive case: a run that did not reach
  // `succeeded` is reported with the server message, and then the case fails loudly
  // because CV-12 is the happy path with real artifacts, not a state renderer.
  if (receipt.status !== "succeeded") {
    await expect(page.getByRole("alert")).toContainText(receipt.error ?? "");
    await expect(
      page.getByRole("button", { name: "Abrir la conversación del ejemplo", exact: true }),
    ).toHaveCount(0);
    throw new Error(
      `CV-12 exige el camino feliz: el ejemplo respondió ${response.status()} status=${receipt.status} error=${receipt.error ?? ""}`,
    );
  }

  expect(response.status(), "el ejemplo ejecutado responde 200").toBe(200);
  const sessionId = receipt.session_id ?? "";
  const taskId = receipt.task_id ?? "";
  expect(sessionId, "el ejemplo publicado nombra su conversación").not.toBe("");
  expect(taskId, "el ejemplo publicado nombra su tarea").not.toBe("");

  // B0 parity: the artifacts exist in this same instance and the links really open them.
  const conversation = await page.request.get(
    `/api/conversations/${encodeURIComponent(sessionId)}`,
  );
  expect(conversation.status(), `GET /api/conversations/${sessionId}`).toBe(200);
  const task = await page.request.get(`/api/tasks/${encodeURIComponent(taskId)}`);
  expect(task.status(), `GET /api/tasks/${taskId}`).toBe(200);
  const storedTask = (await task.json()) as { completed?: boolean; label?: string; status?: string; completion_generation?: number };
  expect(storedTask.completed, "la tarea del ejemplo queda pendiente para el humano").toBe(false);
  expect(storedTask.status, "la tarea del ejemplo queda sin completar").toBe("incomplete");
  expect(storedTask.label).toBe("pending");
  expect(
    storedTask.completion_generation,
    "el ejemplo no gasta generaciones de finalización",
  ).toBe(0);
  const exampleHistory = await read<{ completions?: unknown[]; labels?: unknown[] }>(
    page,
    `/api/tasks/${encodeURIComponent(taskId)}/history`,
  );
  expect(exampleHistory.httpStatus, `GET /api/tasks/${taskId}/history`).toBe(200);
  expect(
    exampleHistory.completions ?? [],
    "el ejemplo no completa ni reabre la tarea por su cuenta",
  ).toEqual([]);
  expect(
    exampleHistory.labels ?? [],
    "el ejemplo no valora la tarea por su cuenta",
  ).toEqual([]);
  const decisionChecks = (receipt.checks ?? []).filter((check) =>
    ["human_completion", "human_label", "reopen_request", "reopen_revokes_completion", "reopen_resets_label", "stale_generation_rejected"].includes(check.name),
  );
  expect(decisionChecks, "los controles del ejemplo no ejecutan los gates humanos").toEqual([]);

  const checks = receipt.checks ?? [];
  expect(checks.length, "el ejemplo publica sus controles reales").toBeGreaterThan(0);
  expect(
    checks.every((check) => check.status === "passed" || check.status === "failed"),
    "cada control trae su estado",
  ).toBe(true);
  await expect(page.getByRole("columnheader", { name: "Control", exact: true })).toBeVisible();
  await expect(page.getByRole("columnheader", { name: "Observado", exact: true })).toBeVisible();
  expect(
    await page.getByText("El ejemplo corre en su propio almacén de prueba aislado").count(),
    "con artefactos servidos no se declara aislamiento",
  ).toBe(0);

  const link = page.getByRole("button", {
    name: "Abrir la conversación del ejemplo",
    exact: true,
  });
  await expect(link).toBeVisible();
  await link.click();
  await expect.poll(() => query(page.url(), "conversation")).toBe(sessionId);
  await expect(
    page.getByText("No se pudo abrir esta sección", { exact: false }),
    "la conversación del ejemplo se abre de verdad",
  ).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Volver a la lista", exact: true })).toBeVisible();

  // Its own screen opens the task it created, so the example runs once more and this second
  // run leaves its own artifacts.
  await page.goto(route(CONVERSATIONS_ROUTE, "example"));
  const again = page.waitForResponse(
    (response) =>
      response.url().endsWith("/api/foundry/example") && response.request().method() === "POST",
  );
  await page.getByRole("button", { name: "Preparar y ejecutar el ejemplo", exact: true }).click();
  const second = (await (await again).json()) as { status?: string; task_id?: string };
  expect(second.status, "la segunda ejecución del ejemplo termina").toBe("succeeded");
  const taskLink = page.getByRole("button", { name: "Abrir la tarea pendiente", exact: true });
  await expect(taskLink).toBeVisible();
  await taskLink.click();
  await expect.poll(() => query(page.url(), "task")).toBe(second.task_id ?? "");
  await expect(page.getByLabel("Valoración")).toBeDisabled();
  await expect(page.getByText("Confirma primero que la tarea está completada.")).toBeVisible();
  const historyPanel = page.locator('[data-slot="card"]').filter({ hasText: "Historial de esta tarea" });
  await expect(historyPanel).toBeVisible();
  await expect(historyPanel.getByText("Finalización", { exact: true })).toHaveCount(0);

  // The example endpoint failing for real: the browser blocks it (CDP, no stubbed answer) and
  // the screen states the failure instead of inventing a demo; unblocking recovers for real.
  await page.goto(route(CONVERSATIONS_ROUTE, "example"));
  await expect(page.getByRole("button", { name: "Preparar y ejecutar el ejemplo", exact: true })).toBeEnabled();
  const client = await page.context().newCDPSession(page);
  await client.send("Network.enable");
  const exampleURL = new URL("/api/foundry/example", page.url()).href;
  guard.expected.push(exampleURL);
  await client.send("Network.setBlockedURLs", { urls: [exampleURL] });
  await page.getByRole("button", { name: "Preparar y ejecutar el ejemplo", exact: true }).click();
  await expect(page.getByRole("alert"), "el fallo real se declara").toBeVisible({ timeout: 20_000 });
  await expect(
    page.getByRole("button", { name: "Abrir la conversación del ejemplo", exact: true }),
  ).toHaveCount(0);
  await client.send("Network.setBlockedURLs", { urls: [] });
  await page.getByRole("button", { name: "Preparar y ejecutar el ejemplo", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Abrir la conversación del ejemplo", exact: true }),
    "desbloquear recupera el ejemplo real",
  ).toBeVisible({ timeout: 30_000 });
  await client.detach();
});

test("UI-01: la vista de Propuestas conserva entrada directa, recarga y atrás/adelante", async ({
  page,
}) => {
  requireRelease();
  guard = watchConsole(page);
  await signIn(page);

  // Direct entry: the route the shell reads decides the view, with no help from this
  // section (it never rewrites the address bar).
  await page.goto(route(TASKS_ROUTE, "proposals"));
  await expect(page.getByText("Análisis de tareas", { exact: true })).toBeVisible();
  await expect(page).toHaveURL(/view=proposals/);
  await expect(page.getByText("Histórico de tareas", { exact: true })).toHaveCount(0);

  // Reload keeps the destination.
  await page.reload();
  await expect(page.getByText("Análisis de tareas", { exact: true })).toBeVisible();
  await expect.poll(() => query(page.url(), "view")).toBe("proposals");

  // The section tabs move through the shell's navigation, so back/forward restore them.
  const tabs = page.getByRole("navigation", { name: /Secciones de Tareas/ });
  await tabs.getByRole("button", { name: "Tareas", exact: true }).click();
  await expect(page.getByText("Histórico de tareas", { exact: true })).toBeVisible();
  await expect.poll(() => query(page.url(), "view")).toBe("tasks");
  await page.goBack();
  await expect(page.getByText("Análisis de tareas", { exact: true })).toBeVisible();
  await expect.poll(() => query(page.url(), "view")).toBe("proposals");
  await page.goForward();
  await expect(page.getByText("Histórico de tareas", { exact: true })).toBeVisible();
  await expect.poll(() => query(page.url(), "view")).toBe("tasks");

  // A reload of a traversal destination still resolves to the same view.
  await page.goBack();
  await page.reload();
  await expect(page.getByText("Análisis de tareas", { exact: true })).toBeVisible();
});

test("TK-06/07/08: consentimiento del análisis, gates del proveedor y aceptación explícita", async ({
  page,
}) => {
  requireRelease();
  guard = watchConsole(page);
  await signIn(page);

  // The configuration view mirrors the installation field by field, including the real
  // diagnostics of both analysis engines.
  await page.goto(route(TASKS_ROUTE, "proposals"));
  const configured = await read<{
    config?: {
      auto_tasks: boolean;
      task_idle_seconds: number;
      max_input_chars: number;
      max_calls: number;
      experiment_seconds: number;
      provider: { kind: string; command: string[]; model: string; timeout: number; real_inference: boolean; reasoning?: string };
    };
    diagnostics?: {
      provider: { available: boolean; reason?: string };
      replay: { available: boolean; reason?: string };
      ready: boolean;
      reason?: string;
    };
  }>(page, "/api/tasks/config");
  expect(configured.httpStatus, "GET /api/tasks/config").toBe(200);
  const cfg = configured.config;
  const diagnostics = configured.diagnostics;
  expect(cfg?.provider, "la instalación publica su configuración de análisis").toBeTruthy();
  expect(diagnostics?.provider, "la instalación publica diagnósticos").toBeTruthy();

  await expect(page.locator("#cfg-kind")).toHaveValue(cfg?.provider.kind ?? "");
  await expect(page.locator("#cfg-model")).toHaveValue(cfg?.provider.model ?? "");
  await expect(page.locator("#cfg-command")).toHaveValue((cfg?.provider.command ?? []).join(" "));
  await expect(page.locator("#cfg-timeout")).toHaveValue(String(cfg?.provider.timeout));
  await expect(page.locator("#cfg-max_calls")).toHaveValue(String(cfg?.max_calls));
  await expect(page.locator("#cfg-max_input_chars")).toHaveValue(String(cfg?.max_input_chars));
  await expect(page.locator("#cfg-experiment_seconds")).toHaveValue(String(cfg?.experiment_seconds));
  await expect(page.locator("#cfg-task_idle_seconds")).toHaveValue(String(cfg?.task_idle_seconds));
  const autoBox = page.getByRole("checkbox", { name: /Proponer tareas automáticamente/ });
  await expect(autoBox).toBeChecked({ checked: cfg?.auto_tasks === true });
  await expect(page.getByRole("checkbox", { name: /inferencia real del proveedor/ })).toBeChecked({
    checked: cfg?.provider.real_inference === true,
  });
  if (diagnostics?.provider.available === false)
    await expect(page.getByText(diagnostics.provider.reason ?? "", { exact: false })).toBeVisible();
  await expect(
    page.getByText(
      diagnostics?.ready
        ? "análisis disponible"
        : "Análisis no disponible:",
      { exact: false },
    ).first(),
  ).toBeVisible();

  // Negative: a budget outside the published range never leaves the form.
  await page.locator("#cfg-max_calls").fill("1");
  await expect(page.getByRole("button", { name: "Guardar cambios", exact: true })).toBeDisabled();
  await expect(page.getByRole("alert")).toContainText("entre 2 y 200");
  await page.locator("#cfg-max_calls").fill(String(cfg?.max_calls));

  // Automatic proposals are an explicit opt-in that persists, and turning it off cancels
  // the queued automatic ones (the server does that; the screen must confirm the state).
  const wasAuto = cfg?.auto_tasks === true;
  await autoBox.setChecked(!wasAuto);
  await page.getByRole("button", { name: "Guardar cambios", exact: true }).click();
  await expect(page.getByText("Configuración confirmada por la instalación.")).toBeVisible();
  const patched = await read<{ config?: { auto_tasks: boolean } }>(page, "/api/tasks/config");
  expect(patched.config?.auto_tasks, "el consentimiento queda guardado").toBe(!wasAuto);
  await page.reload();
  await expect(
    page.getByRole("checkbox", { name: /Proponer tareas automáticamente/ }),
  ).toBeChecked({ checked: !wasAuto });
  if (wasAuto !== (!wasAuto)) {
    await page
      .getByRole("checkbox", { name: /Proponer tareas automáticamente/ })
      .setChecked(wasAuto);
    await page.getByRole("button", { name: "Guardar cambios", exact: true }).click();
    await expect(page.getByText("Configuración confirmada por la instalación.")).toBeVisible();
  }

  // The propose action follows the diagnostic of this installation: it is never offered
  // when the analysis cannot run, and with a ready provider the whole vertical runs.
  const stored = await conversations(page, "");
  const target =
    stored.find((row) => row.coverage?.status === "complete") ?? stored[0];
  expect(target?.id, "hay una conversación capturada para analizar").toBeTruthy();
  await page.goto(
    route(CONVERSATIONS_ROUTE, "conversations", {
      conversation: target?.id ?? "",
      revision: target?.revision ?? "",
    }),
  );
  const propose = page.getByRole("button", { name: "Proponer tareas", exact: true });
  await expect(propose).toBeVisible();
  const before = await tasks(page, `session_id=${encodeURIComponent(target?.id ?? "")}`);

  if (diagnostics?.provider.available === false || diagnostics?.ready === false) {
    await expect(
      propose,
      "sin análisis disponible la acción no se ofrece",
    ).toBeDisabled();
    await expect(
      page.getByText(diagnostics?.reason ?? diagnostics?.provider.reason ?? "", { exact: false }),
    ).toBeVisible();
    const refused = await page.request.post("/api/tasks/proposals", {
      data: { session_id: target?.id, expected_revision: target?.revision },
    });
    expect(refused.status(), "el servidor rechaza el análisis sin proveedor").toBe(409);
    const refusedBody = (await refused.json()) as { error?: string; code?: string };
    expect(refusedBody.error ?? "").toContain("proveedor");
    return;
  }

  await expect(propose).toBeEnabled();
  await propose.click();
  let proposal = await proposalOf(page, target?.id ?? "", target?.revision ?? "");
  for (let attempt = 0; attempt < 60; attempt++) {
    if (proposal.status !== "queued" && proposal.status !== "running") break;
    await page.waitForTimeout(2000);
    proposal = await proposalOf(page, target?.id ?? "", target?.revision ?? "");
  }
  expect(proposal.status, "la propuesta termina en un estado terminal").toBe("completed");
  const suggestions = proposal.suggestions ?? [];
  expect(suggestions.length, "una propuesta completa publica sugerencias").toBeGreaterThan(0);
  expect(
    suggestions.every((row) => (row.evidence ?? []).length > 0),
    "cada sugerencia trae sus citas",
  ).toBe(true);
  expect(
    (proposal.inference?.runs ?? []).some((run) => run.real_inference === true),
    "la propuesta trae recibo de inferencia real",
  ).toBe(true);
  expect(
    (await tasks(page, `session_id=${encodeURIComponent(target?.id ?? "")}`)).length,
    "una propuesta no crea tareas por sí sola",
  ).toBe(before.length);

  await expect(page.getByText("sugerencias", { exact: false }).first()).toBeVisible();
  await page.getByRole("checkbox", { name: /Aceptar sugerencia/ }).first().check();
  await page.getByRole("button", { name: "Aceptar seleccionadas", exact: true }).click();
  await expect(page.getByText("tareas creadas", { exact: false })).toBeVisible();
  const after = await tasks(page, `session_id=${encodeURIComponent(target?.id ?? "")}`);
  expect(after.length, "la aceptación explícita crea la tarea elegida").toBe(before.length + 1);
  const created = after.find((row) => !before.some((previous) => previous.id === row.id));
  expect(created?.completed, "la tarea aceptada queda pendiente para el humano").toBe(false);
  await expect(
    page.getByRole("button", { name: new RegExp(created?.title?.slice(0, 12) ?? "") }),
  ).toBeVisible();
});

test("TK-08 nativo: OMP propone con Magpie y sólo la aceptación humana crea tareas", async ({ page }) => {
  requireRelease();
  if (process.env.E2E_OMP_ANALYSIS_MODEL !== "gpt-6-luna")
    throw new Error("Authorize this real-inference case with E2E_OMP_ANALYSIS_MODEL=gpt-6-luna.");
  const session = process.env.E2E_PROPOSAL_SESSION;
  if (!session) throw new Error("E2E_PROPOSAL_SESSION must name a newly captured fixture without tasks.");
  test.setTimeout(180_000);
  guard = watchConsole(page);
  await signIn(page);
  const configured = await read<{
    config: { auto_tasks: boolean; provider: { kind: string; real_inference: boolean } };
    diagnostics: { provider: { available: boolean } };
  }>(page, "/api/tasks/config");
  expect(configured.httpStatus).toBe(200);
  expect(configured.config.auto_tasks).toBe(false);
  expect(configured.config.provider.kind).toBe("omp");
  expect(configured.config.provider.real_inference).toBe(true);
  expect(configured.diagnostics.provider.available).toBe(true);
  const configuredHarness = await read<{
    harnesses: { id: string; wired: boolean; fields: { key: string; value?: string }[] }[];
  }>(page, "/api/models/harnesses");
  const omp = configuredHarness.harnesses.find((harness) => harness.id === "omp");
  expect(omp?.wired).toBe(true);
  for (const role of ["model", "tiny"])
    expect(omp?.fields.find((field) => field.key === role)?.value).toBe("magpie/codex/gpt-6-luna");
  const target = (await conversations(page, "")).find((row) => row.id === session);
  expect(target?.coverage?.status).toBe("complete");
  const before = await tasks(page, "session_id=" + encodeURIComponent(session));
  expect(before, "the prepared fixture must not already contain accepted tasks").toHaveLength(0);
  await page.goto(route(CONVERSATIONS_ROUTE, "conversations", {
    conversation: session, revision: target?.revision ?? "",
  }));
  await page.getByRole("button", { name: "Proponer tareas", exact: true }).click();
  await expect.poll(async () => (await proposalOf(page, session, target?.revision ?? "")).status,
    { timeout: 150_000, intervals: [500, 1500] }).toBe("completed");
  const proposal = await proposalOf(page, session, target?.revision ?? "");
  expect(proposal.suggestions?.length).toBeGreaterThan(0);
  expect(proposal.inference?.runs?.length).toBeGreaterThan(0);
  for (const run of proposal.inference?.runs ?? []) {
    expect(run.provider).toBe("omp");
    expect(["gpt-6-luna", "codex/gpt-6-luna", "magpie/codex/gpt-6-luna"]).toContain(run.model);
    expect(run.real_inference).toBe(true);
    expect(run.tool_calls).toBe(0);
  }
  expect(await tasks(page, "session_id=" + encodeURIComponent(session))).toHaveLength(0);
  await page.getByRole("checkbox", { name: /Aceptar sugerencia/ }).first().check();
  await page.getByRole("button", { name: "Aceptar seleccionadas", exact: true }).click();
  await expect(page.getByText("tareas creadas", { exact: false })).toBeVisible();
  const accepted = await tasks(page, "session_id=" + encodeURIComponent(session));
  expect(accepted).toHaveLength(1);
  expect(accepted[0].completed).toBe(false);
  expect(["correct", "incorrect"]).not.toContain(accepted[0].label);
  expect(accepted[0].citations?.length).toBeGreaterThan(0);
  await page.reload();
  expect(await tasks(page, "session_id=" + encodeURIComponent(session))).toEqual(accepted);
  await expect(page.getByRole("button", { name: accepted[0].title, exact: true })).toBeVisible();
});

test("CV-05/03: el escaneo global es una cola real sobre las fuentes activas", async ({
  page,
}) => {
  requireRelease();
  guard = watchConsole(page);
  await signIn(page);
  await page.goto(route(CONVERSATIONS_ROUTE, "sources"));
  expect((await sources(page)).some((source) => source.path === required("E2E_SOURCE_PATH") && source.enabled), "la fuente del fixture está activa").toBe(true);

  // There is no global capture switch in this release: the manual scan is the explicit
  // consent of its own action and is never refused by a paused global figure.
  const request = page.waitForResponse(
    (response) =>
      response.url().includes("/api/conversations") && response.request().method() === "POST",
  );
  await page.getByRole("button", { name: "Escanear ahora", exact: true }).click();
  const response = await request;
  expect(response.status(), "el escaneo manual encola un job real").toBe(202);
  const job = page.locator('[data-slot="card"]').filter({ hasText: "Captura en curso" });
  await expect(job).toBeVisible();
  await expect(job.getByText("Terminada", { exact: true })).toBeVisible({
    timeout: 45_000,
  });
});

// CV-05 keeps the capture limits honest: a partially captured source publishes its real
// issue total with a bounded sample, survives a reload, is not doubled by a rescan, and
// recovers when the file is corrected. The case writes its own broken file first, so a
// rerun never depends on what a previous run left behind.
test("CV-05: una captura parcial publica su total con muestra acotada y se recupera al corregir el archivo", async ({
  page,
}) => {
  requireRelease();
  guard = watchConsole(page);
  const directory = required("E2E_CV05_FIX_DIR");
  const file = join(directory, "cv05-partial.jsonl");
  const record = (id: string, role: string, sequence: number, text: string) =>
    JSON.stringify({
      id,
      session_id: "cv05-fixable-20261017",
      role,
      kind: "message",
      sequence,
      text,
      timestamp: "2026-10-17T11:10:00Z",
    }) + "\n";
  const kept = record(
    "cv05-fixable-0",
    "user",
    0,
    "CV05 fixable source 20261017: retained evidence before the malformed tail",
  );
  const corrected = record(
    "cv05-fixable-1",
    "assistant",
    1,
    "Corrected line that replaces the malformed tail.",
  );
  await writeFile(file, kept + "not-json\n".repeat(205), "utf8");

  await signIn(page);
  await page.goto(route(CONVERSATIONS_ROUTE, "sources"));
  await registerSourcePath(page, file);
  const partialScan = await scanSource(page, sourceRow(page, file), "Terminada");

  // The receipt states the conversation this capture left behind, never the file count.
  expect((await jobReceipt(page, partialScan.jobId)).result?.sessions, "la captura parcial retuvo una conversación").toBe(1);
  await expect(partialScan.job).toContainText("Sesiones capturadas: 1");
  await expect(partialScan.job).not.toContainText("sin dato");

  // The screen states the real total (205) and the bounded sample (200) instead of
  // presenting twenty entries as everything that failed.
  const card = sourceRow(page, file);
  await expect(card.getByText("parcial", { exact: true }).first()).toBeVisible();
  await expect(card).toContainText("205 incidencias");
  await expect(card).toContainText("Se muestran 20 de 200 incidencias");
  await expect(card).toContainText("cv05-partial.jsonl");
  await expect(card).toContainText("malformed_json");
  await expect(card, "el estado del último escaneo se nombra, no se deja sin traducir").toContainText(
    "último escaneo: completa",
  );

  const expectPartial = (row: SourceRow) => {
    expect(row.coverage?.status, "cobertura persistida").toBe("partial");
    expect(row.coverage?.issue_count, "total persistido, no el tamaño de la muestra").toBe(205);
    expect(row.coverage?.blocking_issue_count).toBe(205);
    expect(row.coverage?.issues?.length, "muestra persistida acotada").toBe(200);
    const issue = row.coverage?.issues?.[0];
    expect(issue?.file_name, "la incidencia conserva el archivo").toBe("cv05-partial.jsonl");
    expect(issue?.line, "la incidencia conserva la línea").toBe(2);
    expect(issue?.reason, "la incidencia conserva el motivo").toBe("malformed_json");
  };
  const readBack = async () => {
    const row = await storedSource(page, file);
    return { row, captured: await conversations(page, `source_id=${row.id}`) };
  };

  const first = await readBack();
  expectPartial(first.row);
  expect(first.captured.length, "la evidencia válida sí se conserva").toBe(1);

  // Reload and rescan read the installation: nothing is lost and nothing doubles.
  await page.reload();
  await expect(sourceRow(page, file)).toContainText("Se muestran 20 de 200 incidencias");
  const reloaded = await readBack();
  expectPartial(reloaded.row);
  expect(reloaded.captured.map((item) => item.id)).toEqual(first.captured.map((item) => item.id));

  const unchangedScan = await scanSource(page, sourceRow(page, file), "Terminada");
  expect((await jobReceipt(page, unchangedScan.jobId)).result?.sessions, "un rescaneo sin cambios no infla el contador").toBe(1);
  const rescanned = await readBack();
  expectPartial(rescanned.row);
  expect(rescanned.captured.map((item) => item.id)).toEqual(first.captured.map((item) => item.id));
  expect(rescanned.captured[0]?.revision).toBe(first.captured[0]?.revision);

  // Correcting the file turns the same source into a complete capture with one
  // conversation: the recovered scan neither loses nor duplicates evidence.
  await writeFile(file, kept + corrected, "utf8");
  const repairedScan = await scanSource(page, sourceRow(page, file), "Terminada");
  expect((await jobReceipt(page, repairedScan.jobId)).result?.sessions, "la reparación conserva la misma conversación").toBe(1);
  const fixedCard = sourceRow(page, file);
  await expect(fixedCard.getByText("completa", { exact: true }).first()).toBeVisible();
  await expect(fixedCard).toContainText("0 incidencias");
  await expect(fixedCard).not.toContainText("Se muestran 20 de");
  const fixed = await readBack();
  expect(fixed.row.coverage?.status).toBe("complete");
  expect(fixed.row.coverage?.issue_count).toBe(0);
  expect(fixed.row.coverage?.event_count).toBe(2);
  expect(fixed.captured.length).toBe(1);
  expect(fixed.captured[0]?.id).toBe(first.captured[0]?.id);
  expect(fixed.captured[0]?.coverage?.status).toBe("complete");
  expect(fixed.captured[0]?.revision).not.toBe(first.captured[0]?.revision);
});

// CV-05: the per-file byte limit is a visible, blocking issue of the capture and never a
// silent skip nor an invented conversation, while a small valid source still captures.
test("CV-05: un archivo por encima del límite de 64 MiB se rechaza con incidencia bloqueante", async ({
  page,
}) => {
  requireRelease();
  guard = watchConsole(page);
  guard.expected.push("/api/tasks/proposals/");
  const overLimit = required("E2E_CV05_LIMIT_PATH");
  const valid = required("E2E_CV05_VALID_PATH");
  const title = required("E2E_CV05_VALID_TITLE");
  await signIn(page);
  await page.goto(route(CONVERSATIONS_ROUTE, "sources"));

  // The oversized file is refused: the job reports it and the card keeps the reason.
  await registerSourcePath(page, overLimit);
  const { job, jobId } = await scanSource(page, sourceRow(page, overLimit), "Falló");
  const rejectReceipt = await jobReceipt(page, jobId);
  expect(rejectReceipt.status).toBe("failed");
  expect(rejectReceipt.result?.sessions, "un archivo rechazado captura cero conversaciones").toBe(0);
  await expect(job).toContainText("Sesiones capturadas: 0");
  await expect(job).not.toContainText("sin dato");
  await expect(job).toContainText("parcial");
  await expect(job).toContainText("1 incidencias");
  await expect(job).toContainText("large-over-limit.jsonl");
  await expect(job).toContainText("source_byte_limit");

  const rejected = await storedSource(page, overLimit);
  expect(rejected.coverage?.status).toBe("partial");
  expect(rejected.coverage?.issue_count).toBe(1);
  expect(rejected.coverage?.blocking_issue_count).toBe(1);
  expect(rejected.coverage?.event_count ?? 0, "un archivo rechazado no captura eventos").toBe(0);
  const issue = rejected.coverage?.issues?.[0];
  expect(issue?.file_name, "el rechazo nombra el archivo").toBe("large-over-limit.jsonl");
  expect(issue?.reason, "el rechazo nombra el motivo").toBe("source_byte_limit");
  expect(issue?.severity).toBe("blocking");
  expect(
    await conversations(page, `source_id=${rejected.id}`),
    "el rechazo no inventa una conversación",
  ).toEqual([]);

  await page.reload();
  const rejectedCard = sourceRow(page, overLimit);
  await expect(rejectedCard.getByText("parcial", { exact: true }).first()).toBeVisible();
  await expect(rejectedCard).toContainText("1 incidencias");
  await expect(rejectedCard).toContainText("source_byte_limit");
  await expect(rejectedCard).toContainText("último escaneo: parcial");

  // A small valid source in the same installation captures completely.
  await registerSourcePath(page, valid);
  const validScan = await scanSource(page, sourceRow(page, valid), "Terminada");
  expect((await jobReceipt(page, validScan.jobId)).result?.sessions, "una fuente válida captura una conversación").toBe(1);
  await expect(validScan.job).toContainText("Sesiones capturadas: 1");
  const validCard = sourceRow(page, valid);
  await expect(validCard.getByText("completa", { exact: true }).first()).toBeVisible();
  await expect(validCard).toContainText("0 incidencias");
  const captured = await storedSource(page, valid);
  expect(captured.coverage?.status).toBe("complete");
  expect(captured.coverage?.issue_count).toBe(0);
  expect(captured.coverage?.event_count).toBe(3);
  const sessions = await conversations(page, `source_id=${captured.id}`);
  expect(sessions.length, "una conversación capturada, sin duplicar").toBe(1);
  expect(sessions[0]?.title).toContain(title);
  expect(sessions[0]?.coverage?.status).toBe("complete");

  // Captured markup stays text: the literal line is shown, no dialog fires and no node
  // of the transcript is injected as markup.
  const dialogs: string[] = [];
  page.on("dialog", (dialog) => {
    dialogs.push(dialog.message());
    void dialog.dismiss();
  });
  await page.goto(route(CONVERSATIONS_ROUTE, "conversations"));
  await page.getByLabel("Buscar conversación").fill(title);
  await expect.poll(() => query(page.url(), "q"), { timeout: 15_000 }).toBe(title);
  const row = page.locator("tbody tr").filter({ hasText: title }).first();
  await expect(row).toBeVisible();
  await row.getByRole("button", { name: "Abrir", exact: true }).click();
  const events = page.locator('article[id^="event-"]');
  const markup = events.filter({ hasText: "onerror" }).first();
  await expect(markup).toBeVisible();
  expect(await markup.locator("img").count(), "el marcado capturado no se inyecta").toBe(0);
  expect(await page.locator('script:has-text("onerror")').count()).toBe(0);
  expect(dialogs, "ningún contenido capturado abrió un diálogo").toEqual([]);
});

/** One captured event as the full read publishes it: the canonical fields plus the
 *  preserved evidence of the adapter that wrote the record. */
interface FullEventRow {
  event_index?: number;
  id?: string;
  id_origin?: string;
  role?: string;
  kind?: string;
  text?: string;
  line?: number;
  channel?: string;
  native_type?: string;
  metadata?: Record<string, unknown>;
  raw_record?: Record<string, unknown>;
  host_id?: string;
  host_name?: string;
  origin?: string;
}

interface AdapterSample {
  adapter: string;
  session: string;
  title: string;
  /** role/kind/text of every captured event, in the order the source wrote them. */
  events: { role: string; kind: string; text?: string; channel?: string; native: boolean }[];
  /** Where this adapter keeps the captured model inside its preserved raw record. */
  model: string[];
  /** Where it keeps the captured usage and the numbers that must be there. */
  usage: { path: string[]; values: Record<string, number> };
  metadata: Record<string, string>;
  /** Origin evidence the record carries; only canonical promotes it to the event fields. */
  origin?: { host_id: string; host_name: string; origin: string };
  rawOrigin?: Record<string, string>;
}

/** The five adapters of CV-09, each with its own new sample and what it must capture. */
const CV09_SAMPLES: AdapterSample[] = [
  {
    adapter: "canonical",
    session: "cv09-canonical-mine",
    title: "pregunta canónica propia",
    events: [
      { role: "service", kind: "session_metadata", native: true },
      { role: "user", kind: "message", text: "pregunta canónica propia", native: true },
      { role: "assistant", kind: "message", text: "respuesta canónica propia", channel: "final", native: true },
    ],
    model: ["model"],
    usage: { path: ["usage"], values: { input_tokens: 21, output_tokens: 7, total_tokens: 28 } },
    metadata: { cwd: "/cv09-mine/canonical", version: "mine-v1", source: "canonical-mine" },
    origin: { host_id: "local", host_name: "Fixture host", origin: "cv09-mine" },
  },
  {
    adapter: "codex",
    session: "cv09-codex-mine",
    title: "pregunta codex propia",
    events: [
      { role: "service", kind: "session_metadata", native: true },
      { role: "service", kind: "native_event", native: false },
      { role: "user", kind: "message", text: "pregunta codex propia", native: true },
      { role: "assistant", kind: "message", text: "respuesta codex propia", channel: "final", native: true },
      { role: "service", kind: "native_event", native: false },
    ],
    model: ["payload", "model"],
    usage: { path: ["payload", "info", "total_token_usage"], values: { input_tokens: 22, output_tokens: 8, total_tokens: 30 } },
    metadata: { cwd: "/cv09-mine/codex", version: "mine-v1", source: "codex-mine" },
  },
  {
    adapter: "claude",
    session: "cv09-claude-mine",
    title: "pregunta claude propia",
    events: [
      { role: "service", kind: "session_metadata", native: true },
      { role: "user", kind: "message", text: "pregunta claude propia", native: true },
      { role: "assistant", kind: "message", text: "respuesta claude propia", native: true },
    ],
    model: ["message", "model"],
    usage: { path: ["message", "usage"], values: { input_tokens: 23, output_tokens: 9 } },
    metadata: { cwd: "/cv09-mine/claude", version: "mine-v1", source: "claude-mine" },
  },
  {
    adapter: "pi",
    session: "cv09-pi-mine",
    title: "pregunta pi propia",
    events: [
      { role: "service", kind: "session_metadata", native: true },
      { role: "user", kind: "message", text: "pregunta pi propia", native: true },
      { role: "assistant", kind: "message", text: "respuesta pi propia", native: true },
    ],
    model: ["message", "model"],
    usage: { path: ["message", "usage"], values: { input: 24, output: 10, totalTokens: 34 } },
    metadata: { cwd: "/cv09-mine/pi", version: "mine-v1", source: "pi-mine" },
  },
  {
    adapter: "omp",
    session: "cv09-omp-mine",
    title: "pregunta omp propia",
    events: [
      { role: "service", kind: "session_metadata", native: true },
      { role: "user", kind: "message", text: "pregunta omp propia", native: true },
      { role: "assistant", kind: "message", text: "respuesta omp propia", channel: "final", native: true },
    ],
    model: ["model"],
    usage: { path: ["usage"], values: { input_tokens: 25, output_tokens: 11, total_tokens: 36 } },
    metadata: { cwd: "/cv09-mine/omp", version: "mine-v1", source: "omp-mine" },
    rawOrigin: { host_id: "local" },
  },
];

/** Reads one nested key path of the published JSON without trusting its shape. */
function dig(value: unknown, path: string[]): unknown {
  let current = value;
  for (const key of path) {
    if (!current || typeof current !== "object") return undefined;
    current = (current as Record<string, unknown>)[key];
  }
  return current;
}

async function fullRevision(page: Page, sessionId: string) {
  const stored = (await conversations(page, `q=${encodeURIComponent(sessionId)}`))[0];
  if (!stored?.revision) throw new Error(`conversation ${sessionId} is not captured`);
  const reply = await read<{ events?: FullEventRow[] }>(
    page,
    `/api/conversations/${encodeURIComponent(stored.id)}/revisions/${encodeURIComponent(stored.revision)}?limit=100`,
  );
  expect(reply.httpStatus, "lectura completa de la revisión").toBe(200);
  return { conversation: stored, events: reply.events ?? [] };
}

// CV-09: every adapter captures its own sample with the roles, order and text of the
// source, keeps the native identifiers and the origin metadata of the record, and the
// captured model/usage stay readable in the preserved evidence after a reload.
test("CV-09: cada adapter ingiere su muestra con roles, orden, identidad y evidencia capturada", async ({
  page,
}) => {
  test.setTimeout(180_000);
  requireRelease();
  guard = watchConsole(page);
  guard.expected.push("/api/tasks/proposals/");
  const directory = required("E2E_CV09_DIR");
  await signIn(page);
  await page.goto(route(CONVERSATIONS_ROUTE, "sources"));

  for (const sample of CV09_SAMPLES) {
    const file = join(directory, `${sample.adapter}.jsonl`);
    await registerSourcePath(page, file, sample.adapter);
    const scan = await scanSource(page, sourceRow(page, file), "Terminada");
    const receipt = await jobReceipt(page, scan.jobId);
    expect(receipt.status, `escaneo ${sample.adapter}`).toBe("succeeded");
    expect(receipt.result?.files, `archivos de ${sample.adapter}`).toBe(1);
    expect(receipt.result?.sessions, `conversaciones de ${sample.adapter}`).toBe(1);
    const stored = await storedSource(page, file);
    expect(stored.adapter).toBe(sample.adapter);
    expect(stored.coverage?.status, `cobertura de ${sample.adapter}`).toBe("complete");
    expect(stored.coverage?.issue_count, `incidencias de ${sample.adapter}`).toBe(0);

    const { conversation, events } = await fullRevision(page, sample.session);
    expect(conversation.title, `título de ${sample.adapter}`).toContain(sample.title);
    expect(events.length, `eventos de ${sample.adapter}`).toBe(sample.events.length);
    sample.events.forEach((expected, index) => {
      const event = events[index];
      // Absent and empty mean the same for these optional fields on the wire.
      expect(
        {
          role: event?.role,
          kind: event?.kind,
          text: event?.text ?? "",
          channel: event?.channel ?? "",
        },
        `evento ${index} de ${sample.adapter}`,
      ).toEqual({
        role: expected.role,
        kind: expected.kind,
        text: expected.text ?? "",
        channel: expected.channel ?? "",
      });
      expect(typeof event?.event_index, `índice de ${sample.adapter}`).toBe("number");
      expect(typeof event?.line, `línea de ${sample.adapter}`).toBe("number");
      expect(event?.id_origin, `procedencia del id de ${sample.adapter} #${index}`).toBe(
        expected.native ? "native" : "generated",
      );
    });

    // Origin evidence of the record and the session metadata the adapter wrote.
    const metadata = events[0]?.metadata;
    for (const [key, value] of Object.entries(sample.metadata)) {
      expect(dig(metadata, [key]), `metadata ${key} de ${sample.adapter}`).toBe(value);
    }
    if (sample.origin) {
      const withOrigin = events.find((event) => event.host_id !== undefined);
      expect(withOrigin?.host_id, `host_id de ${sample.adapter}`).toBe(sample.origin.host_id);
      expect(withOrigin?.host_name, `host_name de ${sample.adapter}`).toBe(sample.origin.host_name);
      expect(withOrigin?.origin, `origin de ${sample.adapter}`).toBe(sample.origin.origin);
    }
    for (const [key, value] of Object.entries(sample.rawOrigin ?? {})) {
      const carrierEvent = events.find((event) => dig(event.raw_record, [key]) !== undefined);
      expect(dig(carrierEvent?.raw_record, [key]), `${key} de ${sample.adapter} en la evidencia`).toBe(value);
    }

    // The model and the usage the adapter logged are readable in the preserved evidence;
    // each adapter puts them in its own event of the sample, so both are located by need.
    const modelCarrier = events.find((event) => dig(event.raw_record, sample.model) !== undefined);
    expect(dig(modelCarrier?.raw_record, sample.model), `modelo capturado de ${sample.adapter}`).toBe(
      `fixture-modelo-${sample.adapter}`,
    );
    const usageCarrier = events.find((event) => dig(event.raw_record, sample.usage.path) !== undefined);
    const usage = dig(usageCarrier?.raw_record, sample.usage.path);
    for (const [key, value] of Object.entries(sample.usage.values)) {
      expect(dig(usage, [key]), `uso ${key} de ${sample.adapter}`).toBe(value);
    }

    // The detail screen shows the same conversation the installation recorded.
    await page.goto(route(CONVERSATIONS_ROUTE, "conversations"));
    await page.getByLabel("Buscar conversación").fill(sample.title);
    await expect.poll(() => query(page.url(), "q"), { timeout: 15_000 }).toBe(sample.title);
    const row = page.locator("tbody tr").filter({ hasText: sample.title }).first();
    await expect(row).toBeVisible();
    await row.getByRole("button", { name: "Abrir", exact: true }).click();
    const rendered = page.locator('article[id^="event-"]');
    await expect.poll(async () => rendered.count(), { timeout: 15_000 }).toBeGreaterThanOrEqual(
      sample.events.length,
    );
    const texts = await rendered.allInnerTexts();
    const expectedTexts = sample.events.map((event) => event.text).filter((text): text is string => Boolean(text));
    let cursor = 0;
    for (const text of expectedTexts) {
      const at = texts.findIndex((line, index) => index >= cursor && line.includes(text));
      expect(at, `${text} aparece en orden en el detalle de ${sample.adapter}`).toBeGreaterThanOrEqual(cursor);
      cursor = at + 1;
    }
    // Reload: the installation still answers the same evidence, not a screen copy.
    await page.reload();
    const afterReload = await fullRevision(page, sample.session);
    expect(afterReload.conversation.revision).toBe(conversation.revision);
    expect(afterReload.events.length).toBe(events.length);
    await page.goto(route(CONVERSATIONS_ROUTE, "sources"));
  }
});

// CV-09: switching the adapter of a source and changing the file each capture a new
// revision of the same conversation; nothing is duplicated and the older revision stays
// readable with the evidence of the adapter that wrote it.
test("CV-09: adapter y revisión cambiantes invalidan sin duplicar la conversación", async ({
  page,
}) => {
  test.setTimeout(120_000);
  requireRelease();
  guard = watchConsole(page);
  const directory = required("E2E_CV09_EDIT_DIR");
  const file = join(directory, "codex.jsonl");
  const record = (id: string, type: string, text: string, channel?: string, timestamp?: string) => ({
    type: "item_completed",
    payload: { item: { id, type, text, ...(channel ? { channel } : {}), ...(timestamp ? { timestamp } : {}) } },
  });
  const records = [
    { type: "session_meta", payload: { id: "cv09-edit-codex", cwd: "/cv09-mine/edit", version: "mine-v1", source: "codex-edit" } },
    record("cv09-edit-u", "UserMessage", "pregunta editable", undefined, "2026-10-17T12:05:00Z"),
    record("cv09-edit-a", "AgentMessage", "respuesta editable", "final", "2026-10-17T12:05:01Z"),
  ];
  // The case owns this copy: it writes the records itself so a rerun is deterministic.
  await writeFile(file, records.map((item) => JSON.stringify(item)).join("\n") + "\n", "utf8");

  await signIn(page);
  await page.goto(route(CONVERSATIONS_ROUTE, "sources"));
  await registerSourcePath(page, file, "canonical");
  // A rerun may find the source already switched by the previous pass: the case puts it
  // back through the same screen so the scenario starts from the wrong adapter.
  if ((await storedSource(page, file)).adapter !== "canonical") {
    await changeAdapter(page, file, "canonical");
  }

  // Read with the wrong adapter: the records are retained raw-only and reported.
  const first = await scanSource(page, sourceRow(page, file), "Terminada");
  const firstReceipt = await jobReceipt(page, first.jobId);
  expect(firstReceipt.result?.sessions).toBe(1);
  const wrong = await fullRevision(page, "cv09-edit-codex");
  const wrongRevision = wrong.conversation.revision ?? "";
  expect(wrongRevision, "la lectura equivocada también queda capturada").not.toBe("");
  expect(wrong.events.filter((event) => event.role === "service").length).toBe(wrong.events.length);
  expect(wrong.events.some((event) => event.role === "user")).toBe(false);
  expect(
    (await storedSource(page, file)).coverage?.issue_count,
    "la lectura equivocada deja incidencias",
  ).toBeGreaterThan(0);

  // Switching the adapter through the screen captures a new revision of the same session.
  await changeAdapter(page, file, "codex");
  const second = await scanSource(page, sourceRow(page, file), "Terminada");
  const secondReceipt = await jobReceipt(page, second.jobId);
  expect(secondReceipt.result?.changed, "el adapter nuevo capturó una revisión").toBe(1);
  const switched = await fullRevision(page, "cv09-edit-codex");
  const switchedRevision = switched.conversation.revision ?? "";
  expect(switched.conversation.id).toBe(wrong.conversation.id);
  expect(switchedRevision).not.toBe(wrongRevision);
  expect(switched.events.map((event) => event.role)).toEqual(["service", "user", "assistant"]);
  expect(switched.events[2]?.text).toBe("respuesta editable");
  expect((await storedSource(page, file)).coverage?.issue_count, "adaptador correcto sin incidencias").toBe(0);

  // Appending to the file captures one more revision without touching the previous ones.
  await writeFile(file, records.map((item) => JSON.stringify(item)).join("\n") + "\n" + JSON.stringify(record("cv09-edit-b", "AgentMessage", "respuesta añadida", "final", "2026-10-17T12:05:02Z")) + "\n", "utf8");
  const third = await scanSource(page, sourceRow(page, file), "Terminada");
  const thirdReceipt = await jobReceipt(page, third.jobId);
  expect(thirdReceipt.result?.changed).toBe(1);
  const grown = await fullRevision(page, "cv09-edit-codex");
  expect(grown.conversation.id).toBe(wrong.conversation.id);
  expect(grown.conversation.revision).not.toBe(switchedRevision);
  expect(grown.events.length).toBe(4);

  // One conversation only, and the revision captured with the wrong adapter is still there.
  const captured = await conversations(page, "q=cv09-edit-codex");
  expect(captured.length, "una sola conversación, sin duplicar").toBe(1);
  const revisions = await read<{ revisions?: { revision?: string }[] }>(
    page,
    `/api/conversations/${encodeURIComponent(captured[0]?.id ?? "")}/revisions`,
  );
  const storedRevisions = (revisions.revisions ?? []).map((item) => item.revision);
  expect(storedRevisions).toContain(wrongRevision);
  expect(storedRevisions).toContain(switchedRevision);
  const older = await read<{ events?: FullEventRow[] }>(
    page,
    `/api/conversations/${encodeURIComponent(wrong.conversation.id)}/revisions/${encodeURIComponent(wrongRevision)}?limit=100`,
  );
  expect(older.httpStatus).toBe(200);
  expect((older.events ?? []).filter((event) => event.role === "service").length).toBe(
    (older.events ?? []).length,
  );
});

// CV-07: a transcript longer than one page is walked with the screen's own "Cargar más
// eventos" without losing, repeating or reordering a single event across the boundary, and
// the immutable revision it reads does not change because of the pagination.
test("CV-07: la transcripción se pagina sin perder ni repetir eventos en la frontera", async ({
  page,
}) => {
  test.setTimeout(120_000);
  requireRelease();
  guard = watchConsole(page);
  guard.expected.push("/api/tasks/proposals/");
  const dialogs: string[] = [];
  page.on("dialog", (dialog) => {
    dialogs.push(dialog.message());
    void dialog.dismiss();
  });
  const directory = required("E2E_CV07_EDIT_DIR");
  const file = join(directory, "cv07-paged.jsonl");
  const total = 105;
  const text = (index: number) =>
    index === 100
      ? `cv07 linea ${String(index).padStart(3, "0")} <img src=x onerror="alert('cv07')"> inerte`
      : `cv07 linea ${String(index).padStart(3, "0")}`;
  const lines = [
    JSON.stringify({
      type: "session_meta",
      payload: { id: "cv07-paged-mine", cwd: "/cv07-mine", version: "mine-v1", source: "cv07-mine" },
    }),
  ];
  for (let index = 0; index < total; index += 1) {
    lines.push(
      JSON.stringify({
        session_id: "cv07-paged-mine",
        id: `cv07-paged-${index}`,
        role: index % 2 === 0 ? "user" : "assistant",
        kind: "message",
        text: text(index),
        sequence: index,
      }),
    );
  }
  // The case owns this file and writes it again on every run, so reruns are deterministic.
  await writeFile(file, lines.join("\n") + "\n", "utf8");

  await signIn(page);
  await page.goto(route(CONVERSATIONS_ROUTE, "sources"));
  await registerSourcePath(page, file, "canonical");
  const scan = await scanSource(page, sourceRow(page, file), "Terminada");
  const receipt = await jobReceipt(page, scan.jobId);
  expect(receipt.result?.sessions).toBe(1);

  const openTranscript = async () => {
    await page.goto(route(CONVERSATIONS_ROUTE, "conversations"));
    await page.getByLabel("Buscar conversación").fill("cv07 linea 000");
    await expect.poll(() => query(page.url(), "q"), { timeout: 15_000 }).toBe("cv07 linea 000");
    const row = page.locator("tbody tr").filter({ hasText: "cv07 linea 000" }).first();
    await expect(row).toBeVisible();
    await row.getByRole("button", { name: "Abrir", exact: true }).click();
    await expect(page.locator('article[id^="event-"]').first()).toBeVisible();
  };
  const rendered = () => page.locator('article[id^="event-"]');
  const numbers = async () => {
    const texts = await rendered().allInnerTexts();
    return texts
      .map((line) => /cv07 linea (\d{3})/.exec(line)?.[1])
      .filter((value): value is string => Boolean(value));
  };
  const more = () => page.getByRole("button", { name: "Cargar más eventos", exact: true });

  await openTranscript();
  const conversation = (await conversations(page, "q=cv07 linea 000"))[0];
  expect(conversation?.revision, "la conversación quedó capturada").toBeTruthy();

  // First page: the 100 events the installation serves before the cursor moves on
  // (the first of them is the session metadata, so 99 messages are on this page).
  expect(await rendered().count(), "primera página de la transcripción").toBe(100);
  const firstPage = await numbers();
  expect(firstPage).toEqual(
    Array.from({ length: 99 }, (_, index) => String(index).padStart(3, "0")),
  );
  expect(firstPage.includes("100"), "la frontera aún no está cargada").toBe(false);
  await expect(more()).toBeEnabled();

  // The boundary: the look-ahead event is served by the next page, never skipped.
  await more().click();
  await expect.poll(async () => rendered().count(), { timeout: 20_000 }).toBe(total + 1);
  const walked = await numbers();
  expect(walked, "recorrido completo sin huecos, duplicados ni desorden").toEqual(
    Array.from({ length: total }, (_, index) => String(index).padStart(3, "0")),
  );
  expect(walked[walked.length - 1], "último evento de la transcripción").toBe(
    String(total - 1).padStart(3, "0"),
  );
  await expect(more()).toBeDisabled();

  // The event behind the boundary is the inert markup line: text, never a node or dialog.
  const boundary = page.locator('article[id^="event-"]').filter({ hasText: "cv07 linea 100" }).first();
  await expect(boundary).toBeVisible();
  expect(await boundary.locator("img").count(), "el marcado capturado no se inyecta").toBe(0);
  expect(await page.locator('script:has-text("alert(\'cv07\')")').count()).toBe(0);
  expect(dialogs, "ningún contenido capturado abrió un diálogo").toEqual([]);

  // Reload: the same immutable revision serves the same paged walk.
  await page.reload();
  await openTranscript();
  const reloaded = await conversations(page, "q=cv07 linea 000");
  expect(reloaded[0]?.revision, "la revisión no cambió por paginar").toBe(conversation?.revision);
  expect(reloaded[0]?.id).toBe(conversation?.id);
  expect(await rendered().count(), "la recarga sirve la misma primera página").toBe(100);
  const reloadedFirstPage = await numbers();
  expect(reloadedFirstPage[reloadedFirstPage.length - 1]).toBe("098");
});

// CV-07 (resto): el texto completo de un evento fuera de la primera página y el
// seguimiento de su cita. La cita debe apuntar al event_index, revisión y quote exactos
// incluso tras recapturar una línea nueva, sin ejecutar el marcado capturado.
test("CV-07: texto completo y seguimiento de una cita fuera de la primera página", async ({
  page,
}) => {
  test.setTimeout(180_000);
  requireRelease();
  guard = watchConsole(page);
  guard.expected.push("/api/tasks/proposals/", "/api/tasks");
  const dialogs: string[] = [];
  page.on("dialog", (dialog) => {
    dialogs.push(dialog.message());
    void dialog.dismiss();
  });
  const directory = required("E2E_CV07_EDIT_DIR");
  const file = join(directory, "cv07-fulltext.jsonl");
  const longIndex = 101; // the 102nd message: event_index 102, behind the first page
  const head = "CV07-FULL request";
  const tail = "CV07-TAIL-END";
  const longText = `${head} ${"a".repeat(5000)} ${tail} <img src=x onerror="alert('cv07-full')"> fin inerte`;
  const message = (index: number) =>
    JSON.stringify({
      session_id: "cv07-fulltext-mine",
      id: `cv07-fulltext-${index}`,
      role: index === longIndex ? "user" : index % 2 === 0 ? "user" : "assistant",
      kind: "message",
      text: index === longIndex ? longText : `cv07 texto ${String(index).padStart(3, "0")}`,
      sequence: index,
    });
  const lines = [
    JSON.stringify({
      type: "session_meta",
      payload: { id: "cv07-fulltext-mine", cwd: "/cv07-mine", version: "mine-v1", source: "cv07-mine" },
    }),
  ];
  for (let index = 0; index < 105; index += 1) lines.push(message(index));
  await writeFile(file, lines.join("\n") + "\n", "utf8");

  await signIn(page);
  await page.goto(route(CONVERSATIONS_ROUTE, "sources"));
  await registerSourcePath(page, file, "canonical");
  await scanSource(page, sourceRow(page, file), "Terminada");

  // Open the conversation and walk to the long event behind the first page.
  await page.goto(route(CONVERSATIONS_ROUTE, "conversations"));
  await page.getByLabel("Buscar conversación").fill("cv07 texto 000");
  await expect.poll(() => query(page.url(), "q"), { timeout: 15_000 }).toBe("cv07 texto 000");
  const row = page.locator("tbody tr").filter({ hasText: "cv07 texto 000" }).first();
  await expect(row).toBeVisible();
  await row.getByRole("button", { name: "Abrir", exact: true }).click();
  const longEvent = page.locator("article#event-102");
  await page.getByRole("button", { name: "Cargar más eventos", exact: true }).click();
  await expect(longEvent).toBeVisible({ timeout: 20_000 });

  // The message of that event is longer than the caption, so it is folded behind
  // "Ver el mensaje": the compact read cuts it, the full read does not.
  const openMessage = async () => {
    const details = longEvent.locator("details");
    if (!(await details.evaluate((element) => (element as HTMLDetailsElement).open))) {
      await details.locator("summary").click();
    }
    return details.innerText();
  };
  const compactText = await openMessage();
  expect(compactText).toContain(head);
  expect(compactText, "la lectura compacta no trae el final del texto").not.toContain(tail);
  expect(await longEvent.locator("img").count()).toBe(0);

  // Full text: the same event keeps its place and now carries its tail, still as text.
  await page.getByRole("checkbox", { name: "Texto completo" }).check();
  await expect(longEvent, "el evento sigue en pantalla tras cambiar la proyección").toBeVisible();
  await expect.poll(async () => openMessage(), { timeout: 20_000 }).toContain(tail);
  expect(await openMessage()).toContain(head);
  expect(await longEvent.locator("img").count(), "el marcado capturado sigue sin inyectarse").toBe(0);
  expect(dialogs, "ningún contenido capturado abrió un diálogo").toEqual([]);

  // A human range over that event, saved from the screen, keeps its exact citation.
  const conversation = (await conversations(page, "q=cv07 texto 000"))[0];
  const revision = conversation?.revision ?? "";
  expect(revision).not.toBe("");
  const title = `Cita larga ${Date.now()}`;
  // The range editor numbers events as the operator sees them: display 103 = event_index 102.
  await page.getByLabel("Inicio", { exact: true }).fill("103");
  await page.getByLabel("Fin", { exact: true }).fill("103");
  await page.getByLabel("Nombre de la tarea").fill(title);
  await page.getByRole("button", { name: "Guardar tarea", exact: true }).click();
  await expect(page.getByText("Tarea guardada.")).toBeVisible();

  const stored = (await tasks(page, `session_id=${encodeURIComponent(conversation?.id ?? "")}`)).find(
    (item) => item.first_event === 102 && item.last_event === 102,
  );
  expect(stored?.title).toBe(title);
  expect(stored?.revision, "la tarea queda fijada a la revisión actual").toBe(revision);
  const citation = stored?.citations?.[0];
  expect(citation?.event_index, "la cita apunta al evento elegido").toBe(102);
  expect(longText.startsWith(citation?.quote ?? "\u0000"), "la cita conserva el texto original").toBe(true);
  expect((citation?.quote ?? "").length, "la cita no se queda vacía").toBeGreaterThan(100);

  // The citation leads to that exact revision and event, on a page it had to load.
  const followCitation = async () => {
    await page.goto(route(TASKS_ROUTE, undefined, { task: stored?.id ?? "" }));
    const follow = page.getByRole("button", { name: "Seguir la cita", exact: true }).first();
    await expect(follow).toBeVisible();
    await follow.click();
    await expect.poll(() => query(page.url(), "conversation")).toBe(conversation?.id);
    await expect.poll(() => query(page.url(), "revision")).toBe(revision);
    await expect.poll(() => query(page.url(), "event")).toBe("102");
    const focused = page.locator("article#event-102");
    await expect(focused).toBeVisible({ timeout: 20_000 });
    await expect(focused).toHaveAttribute("aria-current", "true");
    return focused;
  };
  const first = await followCitation();
  expect(await first.innerText()).toContain(head);

  // Toggling the projection with the citation still focused must not ask for the same page
  // twice: following and keeping the window share one bounded path.
  const asked: string[] = [];
  const collect = (request: Request) => {
    if (request.url().includes("/revisions/")) asked.push(new URL(request.url()).search);
  };
  const focusedMessage = async () => {
    const details = page.locator("article#event-102 details");
    if (!(await details.evaluate((element) => element.hasAttribute("open")))) {
      await details.locator("summary").click();
    }
    return details.innerText();
  };
  page.on("request", collect);
  await page.getByRole("checkbox", { name: "Texto completo" }).check();
  await expect.poll(async () => focusedMessage(), { timeout: 20_000 }).toContain(tail);
  page.off("request", collect);
  const cursors = asked.filter((search) => search.includes("cursor="));
  expect(cursors.length, "la página siguiente se pide una sola vez").toBe(1);
  expect(new Set(asked).size, "ninguna lectura se repite al cambiar la proyección").toBe(asked.length);

  // A new capture of the same source adds a revision; the citation keeps its own.
  const appended = message(105).replace("cv07 texto 105", "cv07 texto NUEVA recaptura");
  await writeFile(file, lines.join("\n") + "\n" + appended + "\n", "utf8");
  await page.goto(route(CONVERSATIONS_ROUTE, "sources"));
  await scanSource(page, sourceRow(page, file), "Terminada");
  const recaptured = (await conversations(page, "q=cv07 texto 000"))[0];
  expect(recaptured?.revision, "la recaptura crea otra revisión").not.toBe(revision);
  expect(recaptured?.id, "la conversación mantiene su identidad").toBe(conversation?.id);

  const again = await followCitation();
  await expect(page.locator("body")).not.toContainText("cv07 texto NUEVA recaptura");
  expect(await again.innerText()).toContain(head);
  expect(
    (await tasks(page, `session_id=${encodeURIComponent(conversation?.id ?? "")}`)).find(
      (item) => item.id === stored?.id,
    )?.revision,
    "la tarea sigue en su revisión histórica",
  ).toBe(revision);
  expect(dialogs, "seguir la cita no ejecuta el marcado capturado").toEqual([]);
});

/** The two fixtures of the list case: one canonical capture that completes and one codex
 *  capture with a malformed line, so source, motor and estado really differ between the
 *  two rows. The nonce changes the file on every run, so a rescan always has work. */
const CV06_COMPLETE = "CV06 completo propio: registro canónico con identidad propia";
const CV06_PARTIAL = "CV06 parcial propio: registro codex con identidad propia";
const CV06_LONG = "CV06-LARGO";

function cv06Canonical(nonce: string) {
  return (
    [
      { session_id: "cv06-owned-complete", role: "user", kind: "message", text: CV06_COMPLETE },
      {
        session_id: "cv06-owned-complete",
        role: "assistant",
        kind: "message",
        text: `${CV06_LONG} ${"dato-largo-sin-marcado ".repeat(40)}`,
        channel: "final",
      },
      { session_id: "cv06-owned-complete", role: "user", kind: "message", text: `CV06 lote ${nonce}` },
    ]
      .map((record) => JSON.stringify(record))
      .join("\n") + "\n"
  );
}

function cv06Codex(nonce: string) {
  const item = (type: string, text: string, channel?: string) => ({
    type: "item_completed",
    payload: { item: { id: `cv06-owned-partial-${type}`, type, text, ...(channel ? { channel } : {}) } },
  });
  return (
    [
      {
        type: "session_meta",
        payload: { id: "cv06-owned-partial", cwd: "/cv06/partial", version: "cv06-v1", source: "cv06-partial" },
      },
      item("UserMessage", CV06_PARTIAL),
      item("AgentMessage", `CV06 respuesta parcial ${nonce}`, "final"),
      "no-es-json",
    ]
      .map((record) => (typeof record === "string" ? record : JSON.stringify(record)))
      .join("\n") + "\n"
  );
}

/** One filter applied in the screen: the wait matches the request that carries exactly
 *  those query values, so the assertions read the answer of the new query and never the
 *  rows of the previous one. */
async function filterList(
  page: Page,
  params: Record<string, string>,
  apply: () => Promise<unknown>,
) {
  const pending = page.waitForResponse((response) => {
    if (response.request().method() !== "GET") return false;
    const url = new URL(response.url());
    if (url.pathname !== "/api/conversations") return false;
    // An empty expected value means the filter is not in the query at all.
    return Object.entries(params).every(([key, value]) => (url.searchParams.get(key) ?? "") === value);
  });
  await apply();
  const response = await pending;
  expect(response.status(), "GET /api/conversations").toBe(200);
}

// CV-06: the list searches and filters by source, motor and estado over the rows the
// installation really has; an empty answer keeps no row of the previous query, a long
// capture still opens and goes back, and a conversation the installation does not have is
// stated as absent instead of showing another conversation's revision.
test("CV-06: la lista filtra por fuente, motor y estado, y vuelve conservando la selección", async ({
  page,
}) => {
  test.setTimeout(240_000);
  requireRelease();
  guard = watchConsole(page);
  guard.expected.push("/api/tasks/proposals/");
  const directory = required("E2E_CV06_DIR");
  const nonce = String(Date.now());
  const complete = join(directory, "cv06-complete.jsonl");
  const partial = join(directory, "cv06-partial.jsonl");
  // The case owns both fixtures: two files, two adapters, one of them broken on purpose.
  await writeFile(complete, cv06Canonical(nonce), "utf8");
  await writeFile(partial, cv06Codex(nonce), "utf8");

  await signIn(page);
  await page.goto(route(CONVERSATIONS_ROUTE, "sources"));
  await registerSourcePath(page, complete, "canonical");
  await registerSourcePath(page, partial, "codex");
  for (const file of [complete, partial]) {
    const scan = await scanSource(page, sourceRow(page, file), "Terminada");
    expect((await jobReceipt(page, scan.jobId)).result?.sessions, `captura de ${file}`).toBe(1);
  }
  const canonicalSource = await storedSource(page, complete);
  const codexSource = await storedSource(page, partial);
  expect(canonicalSource.adapter).toBe("canonical");
  expect(codexSource.adapter).toBe("codex");
  expect(canonicalSource.coverage?.status, "cobertura de la captura canónica").toBe("complete");
  expect(codexSource.coverage?.status, "cobertura de la captura codex").toBe("partial");

  await page.goto(route(CONVERSATIONS_ROUTE, "conversations"));
  const search = page.getByLabel("Buscar conversación");
  const sourceFilter = page.getByLabel("Fuente", { exact: true });
  const motorFilter = page.getByLabel("Motor", { exact: true });
  const statusFilter = page.getByLabel("Estado", { exact: true });
  const rows = page.locator("tbody tr");
  const matching = (title: string) => rows.filter({ hasText: title });
  const empty = page.getByText("Sin coincidencias", { exact: true });

  // Two sources of two adapters: each search answers its own row and never the other one.
  await filterList(page, { q: CV06_COMPLETE }, () => search.fill(CV06_COMPLETE));
  await expect(matching(CV06_COMPLETE)).toHaveCount(1);
  await expect(matching(CV06_PARTIAL)).toHaveCount(0);
  await filterList(page, { q: CV06_PARTIAL }, () => search.fill(CV06_PARTIAL));
  await expect(matching(CV06_PARTIAL)).toHaveCount(1);
  await expect(matching(CV06_COMPLETE)).toHaveCount(0);
  await filterList(page, { q: "" }, () => search.fill(""));

  // The source filter answers the rows the installation reports for that source, not a
  // copy of the screen: the same body is read back from the API and compared with the row.
  await filterList(page, { source_id: codexSource.id }, () => sourceFilter.selectOption(codexSource.id));
  await expect(matching(CV06_PARTIAL)).toHaveCount(1);
  await expect(matching(CV06_COMPLETE)).toHaveCount(0);
  const bySource = await conversations(page, `source_id=${codexSource.id}`);
  expect(bySource.map((row) => row.title)).toEqual([CV06_PARTIAL]);
  await expect(matching(bySource[0]?.title ?? "")).toHaveCount(1);

  await filterList(page, { source_id: canonicalSource.id }, () => sourceFilter.selectOption(canonicalSource.id));
  await expect(matching(CV06_COMPLETE)).toHaveCount(1);
  await expect(matching(CV06_PARTIAL)).toHaveCount(0);

  // An impossible combination of motor and source is empty: no row of the previous query
  // stays behind under the new filters.
  await filterList(page, { adapter: "codex" }, () => motorFilter.selectOption("codex"));
  await expect(rows).toHaveCount(0);
  await expect(empty).toBeVisible();

  await filterList(page, { source_id: codexSource.id }, () => sourceFilter.selectOption(codexSource.id));
  await expect(matching(CV06_PARTIAL)).toHaveCount(1);
  await filterList(page, { adapter: "" }, () => motorFilter.selectOption(""));
  await filterList(page, { status: "complete" }, () => statusFilter.selectOption("complete"));
  await expect(rows).toHaveCount(0);
  await expect(empty).toBeVisible();
  await filterList(page, { status: "partial" }, () => statusFilter.selectOption("partial"));
  await expect(matching(CV06_PARTIAL)).toHaveCount(1);
  await expect(matching(CV06_COMPLETE)).toHaveCount(0);

  // A search term is a literal: the wildcards the operator writes find what they wrote and
  // never turn the list into "everything" or into another row's title.
  await filterList(page, { status: "" }, () => statusFilter.selectOption(""));
  await filterList(page, { source_id: canonicalSource.id }, () => sourceFilter.selectOption(canonicalSource.id));
  await expect(matching(CV06_COMPLETE)).toHaveCount(1);
  await filterList(page, { q: "%" }, () => search.fill("%"));
  await expect(rows).toHaveCount(0);
  await expect(empty).toBeVisible();
  await filterList(page, { q: "CV06_completo" }, () => search.fill("CV06_completo"));
  await expect(rows).toHaveCount(0);
  await expect(empty).toBeVisible();
  await filterList(page, { q: CV06_COMPLETE }, () => search.fill(CV06_COMPLETE));
  await expect(matching(CV06_COMPLETE)).toHaveCount(1);

  // Opening a conversation keeps the destination and the selection; a long capture is
  // shown without freezing the screen, and coming back keeps both filters and rows.
  await rows.filter({ hasText: CV06_COMPLETE }).getByRole("button", { name: "Abrir", exact: true }).click();
  await expect(page.getByRole("heading", { name: CV06_COMPLETE, exact: false }).first()).toBeVisible();
  await expect.poll(() => query(page.url(), "conversation")).toBeTruthy();
  expect(query(page.url(), "q"), "la URL del detalle conserva la búsqueda").toBe(CV06_COMPLETE);
  expect(query(page.url(), "source")).toBe(canonicalSource.id);
  await expect(page.locator('article[id^="event-"]').filter({ hasText: CV06_LONG }).first()).toBeVisible();
  await expect(page.getByText(CV06_PARTIAL)).toHaveCount(0);

  await page.getByRole("button", { name: "Volver a la lista", exact: true }).click();
  await expect.poll(() => query(page.url(), "conversation")).toBeNull();
  expect(query(page.url(), "q"), "volver conserva la búsqueda").toBe(CV06_COMPLETE);
  expect(query(page.url(), "source"), "volver conserva la fuente").toBe(canonicalSource.id);
  await expect(search).toHaveValue(CV06_COMPLETE);
  await expect(sourceFilter).toHaveValue(canonicalSource.id);
  await expect(matching(CV06_COMPLETE)).toHaveCount(1);
  await expect(matching(CV06_PARTIAL)).toHaveCount(0);

  await page.reload();
  await expect(search).toHaveValue(CV06_COMPLETE);
  await expect(sourceFilter).toHaveValue(canonicalSource.id);
  await expect(matching(CV06_COMPLETE)).toHaveCount(1);
  await expect(matching(CV06_PARTIAL)).toHaveCount(0);

  // A conversation this installation does not have is reported as absent: no other
  // conversation's revision is shown and the screen never claims a feature is missing.
  const absent = `cv06-ausente-${nonce}`;
  guard.expected.push(`/api/conversations/${absent}`);
  const missing = await read<{ code?: string }>(page, `/api/conversations/${absent}`);
  expect(missing.httpStatus, "la conversación ausente no existe").toBe(404);
  expect(missing.code).toBe("conversation_not_found");
  await page.goto(route(CONVERSATIONS_ROUTE, "conversations", { conversation: absent, q: CV06_COMPLETE }));
  await expect(page.getByText("No se pudo leer esta vista", { exact: true })).toBeVisible();
  await expect(page.getByText("Esta entrega no expone", { exact: false })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Reintentar", exact: true })).toBeVisible();
  await expect(page.locator('article[id^="event-"]')).toHaveCount(0);
  await expect(page.getByText(CV06_PARTIAL)).toHaveCount(0);
  await expect(page.getByText("Leyendo la conversación…", { exact: true })).toHaveCount(0);
  // Retrying keeps that honest state instead of falling back to another conversation.
  await page.getByRole("button", { name: "Reintentar", exact: true }).click();
  await expect(page.getByText("No se pudo leer esta vista", { exact: true })).toBeVisible();
  await expect(page.locator('article[id^="event-"]')).toHaveCount(0);
});

/** The page size of the list route and the pages the screen walks on its own. The case
 *  writes one capture more than one page, so the second page only exists behind the
 *  real "Cargar más conversaciones" control. */
const CV06_PAGE_SIZE = 50;

function cv06PageCapture(title: string, nonce: string) {
  const session = title.toLowerCase().replace(/[^a-z0-9]+/g, "-");
  return (
    [
      { session_id: session, role: "user", kind: "message", text: title },
      { session_id: session, role: "assistant", kind: "message", text: `${title} respuesta · lote ${nonce}`, channel: "final" },
    ]
      .map((record) => JSON.stringify(record))
      .join("\n") + "\n"
  );
}

/** The same walk the screen does, read back from the route: a page of 50 and the cursor
 *  pages after it, in the order the installation answers. */
async function conversationWalk(page: Page, sourceId: string) {
  const collected: ConversationRow[] = [];
  let cursor = "";
  for (let step = 0; step < 5; step += 1) {
    const query = `source_id=${encodeURIComponent(sourceId)}&limit=${CV06_PAGE_SIZE}${
      cursor ? `&cursor=${cursor}` : ""
    }`;
    const reply = await read<{ sessions?: ConversationRow[]; next_cursor?: string }>(
      page,
      `/api/conversations?${query}`,
    );
    expect(reply.httpStatus, `GET /api/conversations?${query}`).toBe(200);
    collected.push(...(reply.sessions ?? []));
    cursor = reply.next_cursor ?? "";
    if (!cursor) break;
  }
  return collected;
}

// CV-06: the list of captured conversations pages with its own control: every row of the
// installation arrives once, in the order the route answers, the end of the list is stated,
// and going into a conversation and coming back keeps both the loaded rows and the filter.
test("CV-06: la lista pagina con Cargar más sin repetir filas y conserva la selección", async ({
  page,
}) => {
  test.setTimeout(240_000);
  requireRelease();
  guard = watchConsole(page);
  guard.expected.push("/api/tasks/proposals/");
  const folder = join(required("E2E_CV06_DIR"), "page");
  const nonce = String(Date.now());
  const captures = Array.from({ length: CV06_PAGE_SIZE + 1 }, (_, index) => {
    const label = String(index + 1).padStart(2, "0");
    return { path: join(folder, `cv06-page-${label}.jsonl`), title: `CV06 paginado ${label}` };
  });
  // The case owns the folder: one capture per file, one more than one page.
  await mkdir(folder, { recursive: true });
  for (const capture of captures) {
    await writeFile(capture.path, cv06PageCapture(capture.title, nonce), "utf8");
  }

  await signIn(page);
  await page.goto(route(CONVERSATIONS_ROUTE, "sources"));
  await registerSourcePath(page, folder, "canonical");
  const scan = await scanSource(page, sourceRow(page, folder), "Terminada");
  expect(
    (await jobReceipt(page, scan.jobId)).result?.sessions,
    "la carpeta captura sus sesiones",
  ).toBe(CV06_PAGE_SIZE + 1);
  const source = await storedSource(page, folder);
  expect(source.coverage?.status).toBe("complete");

  await page.goto(route(CONVERSATIONS_ROUTE, "conversations"));
  const sourceFilter = page.getByLabel("Fuente", { exact: true });
  const rows = page.locator("tbody tr");
  const more = page.getByRole("button", { name: "Cargar más conversaciones", exact: true });
  const walk = await conversationWalk(page, source.id);
  expect(walk, "la instalación tiene una página y una fila más").toHaveLength(CV06_PAGE_SIZE + 1);
  expect(new Set(walk.map((row) => row.id)).size, "identificadores únicos").toBe(
    CV06_PAGE_SIZE + 1,
  );

  await filterList(page, { source_id: source.id }, () => sourceFilter.selectOption(source.id));
  await expect(rows).toHaveCount(CV06_PAGE_SIZE);
  await expect(more).toBeEnabled();
  // The first page is the route's own order, row by row, and not a page the screen invented.
  for (let index = 0; index < CV06_PAGE_SIZE; index += 1) {
    await expect(rows.nth(index), `fila ${index}`).toContainText(walk[index]?.path ?? "");
  }
  await expect(rows.filter({ hasText: walk[CV06_PAGE_SIZE]?.path ?? "" })).toHaveCount(0);

  await more.click();
  await expect(rows).toHaveCount(CV06_PAGE_SIZE + 1);
  for (let index = CV06_PAGE_SIZE; index < CV06_PAGE_SIZE + 1; index += 1) {
    await expect(rows.nth(index), `fila ${index}`).toContainText(walk[index]?.path ?? "");
  }
  await expect(page.getByText(`${CV06_PAGE_SIZE + 1} / ${CV06_PAGE_SIZE + 1}`, { exact: false })).toBeVisible();
  await expect(more).toBeDisabled();

  // The last row only exists after loading more: opening it and coming back keeps the loaded
  // pages and the filter the operator had, instead of falling back to the first page.
  const last = walk[CV06_PAGE_SIZE];
  await rows.last().getByRole("button", { name: "Abrir", exact: true }).click();
  await expect(page.getByRole("heading", { name: last?.title ?? "", exact: false }).first()).toBeVisible();
  await expect(page.locator('article[id^="event-"]').first()).toBeVisible();
  await page.getByRole("button", { name: "Volver a la lista", exact: true }).click();
  await expect(sourceFilter).toHaveValue(source.id);
  await expect(rows).toHaveCount(CV06_PAGE_SIZE + 1);
  await expect(rows.last()).toContainText(last?.path ?? "");
});

/** The two captures of the history case: one conversation that carries the chosen task and
 *  a new revision after it, and one with the captures the volume of tasks is seeded from. */
const TK03_VOLUME_CAPTURES = 54;

/** The page size of the task history: `fetchTasks` sends no limit, so the route default of
 *  50 decides how many rows the first page holds. Three captures carry the literal search
 *  titles instead of the paginated ones, and two of the paginated ones are completed. */
const TK03_PAGE_SIZE = 50;
const TK03_PAGINATED = TK03_VOLUME_CAPTURES - 3;
const TK03_INCOMPLETE = TK03_PAGINATED - 2;

function tk03Anchor(nonce: string) {
  return (
    [
      { session_id: "tk03-anchor", role: "user", kind: "message", text: `TK03 ancla ${nonce}: petición humana` },
      { session_id: "tk03-anchor", role: "assistant", kind: "message", text: "respuesta del ancla" },
      { session_id: "tk03-anchor", role: "user", kind: "message", text: `TK03 ancla ${nonce}: segunda petición` },
    ]
      .map((record) => JSON.stringify(record))
      .join("\n") + "\n"
  );
}

function tk03Volume(nonce: string) {
  const records: Record<string, string>[] = [];
  for (let index = 1; index <= TK03_VOLUME_CAPTURES; index += 1) {
    const label = String(index).padStart(2, "0");
    records.push({ session_id: "tk03-volume", role: "user", kind: "message", text: `TK03 paginado ${nonce} ${label}` });
    records.push({ session_id: "tk03-volume", role: "assistant", kind: "message", text: `respuesta ${label}` });
  }
  return records.map((record) => JSON.stringify(record)).join("\n") + "\n";
}

/** One page of the history with the same query the screen uses, walked with its cursor. */
async function taskWalk(page: Page, q: string) {
  const collected: TaskRow[] = [];
  let cursor = "";
  for (let step = 0; step < 5; step += 1) {
    const query = `q=${encodeURIComponent(q)}&limit=${TK03_PAGE_SIZE}${cursor ? `&cursor=${cursor}` : ""}`;
    const reply = await read<{ tasks?: TaskRow[]; next_cursor?: string }>(page, `/api/tasks?${query}`);
    expect(reply.httpStatus, `GET /api/tasks?${query}`).toBe(200);
    collected.push(...(reply.tasks ?? []));
    cursor = reply.next_cursor ?? "";
    if (!cursor) break;
  }
  return collected;
}

/** One history state applied in the screen: the wait matches the request that carries
 *  exactly those query values, never the answer of the previous one. */
async function taskQuery(page: Page, params: Record<string, string>, apply: () => Promise<unknown>) {
  const pending = page.waitForResponse((response) => {
    if (response.request().method() !== "GET") return false;
    const url = new URL(response.url());
    if (url.pathname !== "/api/tasks") return false;
    return Object.entries(params).every(([key, value]) => (url.searchParams.get(key) ?? "") === value);
  });
  await apply();
  const response = await pending;
  expect(response.status(), "GET /api/tasks").toBe(200);
}

/** One authenticated write or read of the routes the case seeds with: the browser session
 *  is the same the screen uses, so no test-only endpoint or direct database exists. */
async function tasksApi(page: Page, path: string, data?: Record<string, unknown>) {
  const response = data
    ? await page.request.post(path, { data })
    : await page.request.get(path);
  const body: unknown = await response.json();
  const parsed = (body && typeof body === "object" ? body : {}) as Record<string, unknown>;
  return { ...parsed, httpStatus: response.status() } as Record<string, unknown> & { httpStatus: number };
}

// TK-03: the history filters its four states, searches title and feedback literally, pages
// with its own control, keeps the chosen task and its exact feedback after a reload, and
// follows a citation to the revision that task was created on even after a new capture.
test("TK-03: el histórico filtra, pagina y conserva la tarea y su cita histórica", async ({
  page,
}) => {
  test.setTimeout(300_000);
  requireRelease();
  guard = watchConsole(page);
  guard.expected.push("/api/tasks/proposals/");
  const directory = required("E2E_TK03_DIR");
  const nonce = String(Date.now());
  const anchorPath = join(directory, "tk03-anchor.jsonl");
  const volumePath = join(directory, "tk03-volume.jsonl");
  await writeFile(anchorPath, tk03Anchor(nonce), "utf8");
  await writeFile(volumePath, tk03Volume(nonce), "utf8");

  await signIn(page);
  await page.goto(route(CONVERSATIONS_ROUTE, "sources"));
  for (const file of [anchorPath, volumePath]) {
    await registerSourcePath(page, file, "canonical");
    const scan = await scanSource(page, sourceRow(page, file), "Terminada");
    expect((await jobReceipt(page, scan.jobId)).result?.sessions, `captura de ${file}`).toBe(1);
    expect((await storedSource(page, file)).coverage?.status).toBe("complete");
  }

  // Volume and the states of the branches are preconditions of this case, seeded through
  // the authenticated routes: the gates themselves belong to their own TK-04/05 cases.
  const volume = await read<{ conversation?: ConversationRow; revisions?: { revision: string }[] }>(
    page,
    "/api/conversations/tk03-volume",
  );
  expect(volume.httpStatus, "la conversación del volumen está capturada").toBe(200);
  const volumeRevision = volume.revisions?.[0]?.revision ?? "";
  expect(volumeRevision).not.toBe("");
  const seeded: string[] = [];
  for (let index = 0; index < TK03_VOLUME_CAPTURES; index += 1) {
    const label = String(index + 1).padStart(2, "0");
    const title =
      index === TK03_PAGE_SIZE
        ? `TK03 literal_${nonce}`
        : index === TK03_PAGE_SIZE + 1
          ? `TK03 literal ${nonce}`
          : index === TK03_PAGE_SIZE + 2
            ? `TK03 literal%${nonce}`
            : `TK03 paginado ${nonce} ${label}`;
    const created = await tasksApi(page, "/api/tasks", {
      session_id: "tk03-volume",
      revision: volumeRevision,
      first_event: index * 2,
      last_event: index * 2,
      title,
    });
    expect(created.httpStatus, `tarea sembrada ${title}`).toBe(201);
    seeded.push((created.task as { id: string }).id);
  }
  // One branch is completed and another one valued: both leave the incomplete set.
  for (const [index, label] of [
    [0, "pending"],
    [1, "incorrect"],
  ] as const) {
    const id = seeded[index];
    const done = await tasksApi(page, `/api/tasks/${encodeURIComponent(id)}/completion`, {
      completed: true,
      expected_generation: 0,
    });
    expect(done.httpStatus, `finalización sembrada de ${id}`).toBe(200);
    if (label !== "pending") {
      const valued = await tasksApi(page, `/api/tasks/${encodeURIComponent(id)}/label`, {
        label,
        feedback: "",
        expected_generation: 1,
      });
      expect(valued.httpStatus, `valoración sembrada de ${id}`).toBe(200);
    }
  }

  // The chosen task is created, completed and valued from the screen: the history case
  // needs that state as its precondition, not a second acceptance of the human gates.
  const anchorTitle = `TK03 ancla ${nonce}`;
  await page.goto(route(CONVERSATIONS_ROUTE, "conversations"));
  await page.getByLabel("Buscar conversación").fill(anchorTitle);
  await expect.poll(() => query(page.url(), "q")).toBeTruthy();
  const anchorRow = page.locator("tbody tr").filter({ hasText: anchorTitle }).first();
  await expect(anchorRow).toBeVisible();
  await anchorRow.getByRole("button", { name: "Abrir", exact: true }).click();
  await page.getByLabel("Nombre de la tarea").fill(anchorTitle);
  await page.getByRole("button", { name: "Guardar tarea", exact: true }).click();
  await expect(page.getByText("Tarea guardada.")).toBeVisible();
  await page.getByRole("button", { name: "Abrir tarea", exact: true }).click();
  await expect.poll(() => query(page.url(), "task")).toBeTruthy();
  const anchorId = query(page.url(), "task") ?? "";
  const anchor = await taskById(page, anchorId);
  const anchorRevision = anchor.revision;
  await page.getByRole("button", { name: "Marcar tarea completada", exact: true }).click();
  await expect(page.getByText("Finalización confirmada. Ahora puedes valorar el resultado.")).toBeVisible();
  const seededFeedback = `feedback TK03 ${nonce}: 100% revisado_bien`;
  await page.getByLabel("Valoración").selectOption("correct");
  await page.getByLabel("Qué funcionó o qué falló").fill(seededFeedback);
  await page.getByRole("button", { name: "Guardar valoración", exact: true }).click();
  await expect(page.getByText("Valoración guardada.")).toBeVisible();

  // A new capture of the same conversation: the task stays on the revision it was created
  // on, and following its citation must land there instead of on the new one.
  await writeFile(anchorPath, tk03Anchor(nonce) + JSON.stringify({ session_id: "tk03-anchor", role: "assistant", kind: "message", text: `cierre posterior ${nonce}` }) + "\n", "utf8");
  await page.goto(route(CONVERSATIONS_ROUTE, "sources"));
  await scanSource(page, sourceRow(page, anchorPath), "Terminada");
  const after = await read<{ revisions?: { revision: string }[] }>(page, "/api/conversations/tk03-anchor");
  const latestRevision = after.revisions?.[0]?.revision ?? "";
  expect(latestRevision, "la recaptura publica una revisión nueva").not.toBe(anchorRevision);

  await page.goto(route(TASKS_ROUTE));
  const rows = page.locator("tbody tr");
  const search = page.getByLabel("Buscar tarea");
  const more = page.getByRole("button", { name: "Cargar más tareas", exact: true });
  const paginated = `TK03 paginado ${nonce}`;

  // The four branches through their own buttons: every row the screen shows belongs to the
  // state that is pressed, and the seeded rows answer each filter.
  for (const [button, statusValue, badge, scope, expected] of [
    ["Sin completar", "incomplete", "Sin completar", paginated, `${TK03_INCOMPLETE}`],
    ["Sin valorar", "pending", "Sin valorar", paginated, "1"],
    ["Incorrectas", "incorrect", "Incorrecta", paginated, "1"],
    ["Correctas", "correct", "Correcta", anchorTitle, "1"],
  ] as const) {
    // The search only travels when it changes; the button below always moves the state.
    if ((await search.inputValue()) !== scope) {
      await taskQuery(page, { q: scope }, () => search.fill(scope));
    }
    await taskQuery(page, { status: statusValue, q: scope }, () =>
      page.getByRole("button", { name: button, exact: true }).click(),
    );
    await expect(rows, `filas de ${button}`).toHaveCount(Number(expected));
    for (const text of await rows.allInnerTexts()) {
      expect(text, `toda fila de ${button} declara su estado`).toContain(badge);
    }
  }

  // Title and feedback are searched literally: the wildcards of the operator find their own
  // task and never the neighbouring rows that only share the pattern.
  await taskQuery(page, { status: "" }, () => page.getByRole("button", { name: "Todas", exact: true }).click());
  const literals = [
    [`TK03 literal_${nonce}`, 0],
    [`TK03 literal%${nonce}`, TK03_PAGE_SIZE + 2],
    [`TK03 literal ${nonce}`, TK03_PAGE_SIZE + 1],
  ] as const;
  for (const [term] of literals) {
    await taskQuery(page, { q: term }, () => search.fill(term));
    await expect(rows, `búsqueda literal de ${term}`).toHaveCount(1);
    const found = await tasks(page, `q=${encodeURIComponent(term)}`);
    expect(found.map((row) => row.title), `la ruta responde la misma fila para ${term}`).toEqual([term]);
  }
  // The feedback is searched with the same literal rule, and only the chosen task carries it.
  const feedbackMarker = `${nonce}: 100% revisado_bien`;
  await taskQuery(page, { q: feedbackMarker }, () => search.fill(feedbackMarker));
  await expect(rows).toHaveCount(1);
  await expect(rows.first()).toContainText(anchorTitle);
  await taskQuery(page, { q: `TK03 nada ${nonce}` }, () => search.fill(`TK03 nada ${nonce}`));
  await expect(rows, "una búsqueda sin coincidencias no deja filas anteriores").toHaveCount(0);

  // The history pages with its own control over one page of tasks plus one.
  const walk = await taskWalk(page, paginated);
  expect(walk).toHaveLength(TK03_PAGINATED);
  expect(new Set(walk.map((row) => row.id)).size).toBe(TK03_PAGINATED);
  await taskQuery(page, { q: paginated }, () => search.fill(paginated));
  await expect(rows).toHaveCount(TK03_PAGE_SIZE);
  await expect(more).toBeEnabled();
  for (let index = 0; index < TK03_PAGE_SIZE; index += 1) {
    await expect(rows.nth(index), `fila ${index} del histórico`).toContainText(walk[index]?.title ?? "");
  }
  await more.click();
  await expect(rows).toHaveCount(TK03_PAGINATED);
  for (let index = TK03_PAGE_SIZE; index < TK03_PAGINATED; index += 1) {
    await expect(rows.nth(index), `fila ${index} del histórico`).toContainText(walk[index]?.title ?? "");
  }
  await expect(page.getByText(`${TK03_PAGINATED} / ${TK03_PAGINATED}`, { exact: false })).toBeVisible();
  await expect(more).toBeDisabled();

  // The chosen task keeps its exact feedback after a reload, and the task body the
  // installation answers is the same text the screen shows.
  await taskQuery(page, { q: anchorTitle }, () => search.fill(anchorTitle));
  await rows.first().getByRole("button", { name: "Revisar tarea", exact: true }).click();
  await expect(page.getByText(new RegExp(`Revisión ${anchorRevision.slice(0, 12)}`))).toBeVisible();
  await expect(page.getByRole("button", { name: "Seguir la cita", exact: true })).toHaveCount(
    anchor.citations?.length ?? 0,
  );
  await expect(page.getByLabel("Qué funcionó o qué falló")).toHaveValue(seededFeedback);
  const exact = `TK03 exacto ${nonce}: 100% revisado_bien · ñ á`;
  await page.getByLabel("Qué funcionó o qué falló").fill(exact);
  await page.getByRole("button", { name: "Guardar valoración", exact: true }).click();
  await expect(page.getByText("Valoración guardada.")).toBeVisible();
  await page.reload();
  await expect(page.getByLabel("Qué funcionó o qué falló")).toHaveValue(exact);
  const stored = await taskById(page, anchorId);
  expect(stored.feedback, "el feedback guardado es exactamente el escrito").toBe(exact);
  expect(stored.revision, "la tarea sigue en su revisión").toBe(anchorRevision);

  // The citation leads to that revision and event; the task and its history stay untouched.
  await page.getByRole("button", { name: "Seguir la cita", exact: true }).first().click();
  await expect.poll(() => query(page.url(), "conversation")).toBe("tk03-anchor");
  await expect.poll(() => query(page.url(), "revision")).toBe(anchorRevision);
  const citation = stored.citations?.[0];
  await expect.poll(() => query(page.url(), "event")).toBe(String(citation?.event_index));
  const focused = page.locator(`article#event-${citation?.event_index}`);
  await expect(focused).toBeVisible({ timeout: 20_000 });
  await expect(focused).toHaveAttribute("aria-current", "true");
  await expect(focused).toContainText((citation?.quote ?? "").slice(0, 24));
  await expect(page.getByText("Revisión histórica: muestra la captura de esa fecha")).toBeVisible();
  const unchanged = await taskById(page, anchorId);
  expect(unchanged.revision, "seguir la cita no mueve la tarea de revisión").toBe(anchorRevision);
  expect(unchanged.feedback).toBe(exact);
  expect(unchanged.completion_generation).toBe(stored.completion_generation);

  // An absent task is stated as absent, retrying keeps that state, and the history recovers.
  const absent = `tk03-ausente-${nonce}`;
  guard.expected.push(`/api/tasks/${absent}`);
  const missing = await read<{ code?: string }>(page, `/api/tasks/${absent}`);
  expect(missing.httpStatus).toBe(404);
  expect(missing.code).toBe("task_not_found");
  await page.goto(route(TASKS_ROUTE, undefined, { task: absent }));
  await expect(page.getByText("No se pudo leer esta vista", { exact: true })).toBeVisible();
  await expect(page.getByText("Esta entrega no expone", { exact: false })).toHaveCount(0);
  await expect(page.getByText("No se encontró la tarea.", { exact: false })).toBeVisible();
  await expect(page.getByRole("button", { name: "Reintentar", exact: true })).toBeVisible();
  await expect(page.getByText(anchorTitle)).toHaveCount(0);
  await page.getByRole("button", { name: "Reintentar", exact: true }).click();
  await expect(page.getByText("No se pudo leer esta vista", { exact: true })).toBeVisible();
  await page.goto(route(TASKS_ROUTE));
  await expect(rows.first()).toBeVisible();
});

/** The task history as the detail route publishes it: the append-only record of the two
 *  human gates. */
interface TaskHistoryRow {
  completions?: { generation: number; completed: boolean; confirmed_at?: number; revoked_generation?: number; created?: number }[];
  labels?: { generation: number; action: string; label?: string; feedback?: string; created?: number }[];
}

async function taskHistory(page: Page, id: string) {
  const reply = await read<TaskHistoryRow>(page, `/api/tasks/${encodeURIComponent(id)}/history`);
  expect(reply.httpStatus, `GET /api/tasks/${id}/history`).toBe(200);
  return reply;
}

// TK-04/05: the two human gates over one task created in the screen: the valuation is
// refused until the human confirms completion, the generation and its confirmation are
// durable, every label/note change stays visible in the append-only record, reopening
// retracts the valuation and revokes eligibility, and an obsolete editor cannot overwrite
// a newer decision. Captured events and citations are never altered by any of it.
test("TK-04/05: los dos gates humanos, su generación durable y el historial append-only", async ({
  page,
}) => {
  test.setTimeout(300_000);
  requireRelease();
  guard = watchConsole(page);
  guard.expected.push("/api/tasks/proposals/");
  const directory = required("E2E_TK03_DIR");
  const nonce = String(Date.now());
  const capturePath = join(directory, `tk04-gates-${nonce}.jsonl`);
  const title = `TK04 gates ${nonce}`;
  await writeFile(
    capturePath,
    [
      { session_id: `tk04-gates-${nonce}`, role: "user", kind: "message", text: title },
      { session_id: `tk04-gates-${nonce}`, role: "assistant", kind: "message", text: "respuesta del gate" },
      { session_id: `tk04-gates-${nonce}`, role: "user", kind: "message", text: `TK04 gates ${nonce} segunda petición` },
    ]
      .map((record) => JSON.stringify(record))
      .join("\n") + "\n",
    "utf8",
  );

  await signIn(page);
  await page.goto(route(CONVERSATIONS_ROUTE, "sources"));
  await registerSourcePath(page, capturePath, "canonical");
  const scan = await scanSource(page, sourceRow(page, capturePath), "Terminada");
  expect((await jobReceipt(page, scan.jobId)).result?.sessions).toBe(1);

  // The task is created in the screen, over the exact revision and range.
  await page.goto(route(CONVERSATIONS_ROUTE, "conversations"));
  await page.getByLabel("Buscar conversación").fill(title);
  await expect.poll(() => query(page.url(), "q")).toBeTruthy();
  const conversationRow = page.locator("tbody tr").filter({ hasText: title }).first();
  await expect(conversationRow).toBeVisible();
  await conversationRow.getByRole("button", { name: "Abrir", exact: true }).click();
  await page.getByLabel("Nombre de la tarea").fill(title);
  await page.getByRole("button", { name: "Guardar tarea", exact: true }).click();
  await expect(page.getByText("Tarea guardada.")).toBeVisible();
  await page.getByRole("button", { name: "Abrir tarea", exact: true }).click();
  await expect.poll(() => query(page.url(), "task")).toBeTruthy();
  const id = query(page.url(), "task") ?? "";
  const created = await taskById(page, id);
  // This case triggers two refusals on purpose: the valuation of an incomplete task and the
  // write of an obsolete editor. Both are the 409 the installation answers by contract.
  guard.expected.push(`/api/tasks/${id}/label`);
  const citations = JSON.stringify(created.citations ?? []);
  expect(created.completed, "la tarea nueva no está completada").toBe(false);
  expect(created.status, "la tarea nueva está sin completar").toBe("incomplete");
  expect(created.completion_generation, "la tarea nueva no gastó generaciones").toBe(0);

  // Gate 1 not confirmed: the control is disabled and the installation rejects the write.
  const labelSelect = page.getByLabel("Valoración");
  await expect(labelSelect).toBeDisabled();
  await expect(page.getByText("Confirma primero que la tarea está completada.")).toBeVisible();
  const refused = await tasksApi(page, `/api/tasks/${encodeURIComponent(id)}/label`, {
    label: "correct",
    feedback: "no debe guardarse",
    expected_generation: 0,
  });
  expect(refused.httpStatus, "valorar sin completar se rechaza").toBe(409);
  expect(refused.code).toBe("task_incomplete");
  const untouched = await taskById(page, id);
  expect(untouched.label).toBe("pending");
  expect(untouched.feedback).toBe("");
  const emptyHistory = await taskHistory(page, id);
  expect(emptyHistory.completions ?? [], "la tarea nueva no tiene finalizaciones").toEqual([]);
  expect(emptyHistory.labels ?? [], "la tarea nueva no tiene valoraciones").toEqual([]);

  // Gate 1: completion is explicit and its generation/confirmation survives a reload.
  const completing = page.waitForResponse(
    (response) => response.request().method() === "POST" && new URL(response.url()).pathname === `/api/tasks/${id}/completion`,
  );
  await page.getByRole("button", { name: "Marcar tarea completada", exact: true }).click();
  expect((await completing).status()).toBe(200);
  await expect(page.getByText("Finalización confirmada. Ahora puedes valorar el resultado.")).toBeVisible();
  const completed = await taskById(page, id);
  expect(completed.completed).toBe(true);
  expect(completed.completion_generation, "completar avanza la generación").toBe(1);
  expect(typeof completed.completion_confirmed_at, "la confirmación queda fechada").toBe("number");
  await page.reload();
  await expect(page.getByText("Finalización confirmada", { exact: false }).first()).toBeVisible();
  await expect(labelSelect).toBeEnabled();
  const afterReload = await taskById(page, id);
  expect(afterReload.completion_generation).toBe(1);
  expect(afterReload.completion_confirmed_at).toBe(completed.completion_confirmed_at);

  // Gate 2 over its three values: every write keeps its note and its own entry, and the
  // entries of the previous changes are still the ones that were written.
  const feedback = page.getByLabel("Qué funcionó o qué falló");
  const write = async (apply: () => Promise<unknown>, expected: number) => {
    const pending = page.waitForResponse(
      (response) => response.request().method() === "POST" && new URL(response.url()).pathname === `/api/tasks/${id}/label`,
    );
    await apply();
    expect((await pending).status(), `escritura ${expected}`).toBe(200);
    // The record of its own change is the wait: a notice of a previous write is not one.
    await expect
      .poll(async () => (await taskHistory(page, id)).labels?.length, { timeout: 15_000 })
      .toBe(expected);
    await expect(page.getByText("Valoración guardada.")).toBeVisible();
  };
  const valuations = [
    ["correct", `correcta ${nonce} con nota exacta`],
    ["incorrect", `incorrecta ${nonce}, la nota anterior sigue en el historial`],
    ["pending", `vuelve a pendiente ${nonce}`],
  ] as const;
  for (const [index, [label, text]] of valuations.entries()) {
    await write(async () => {
      await labelSelect.selectOption(label);
      await feedback.fill(text);
      await page.getByRole("button", { name: "Guardar valoración", exact: true }).click();
    }, index + 1);
    const stored = await taskById(page, id);
    expect(stored.label, `etiqueta ${label}`).toBe(label);
    expect(stored.feedback, `nota de ${label}`).toBe(text);
  }
  const valued = await taskHistory(page, id);
  expect((valued.completions ?? []).length, "sólo la finalización confirmada").toBe(1);
  expect(
    (valued.labels ?? []).map((entry) => [entry.action, entry.label, entry.feedback]),
    "el historial conserva los tres cambios, en orden",
  ).toEqual([
    ["label", "correct", `correcta ${nonce} con nota exacta`],
    ["label", "incorrect", `incorrecta ${nonce}, la nota anterior sigue en el historial`],
    ["label", "pending", `vuelve a pendiente ${nonce}`],
  ]);

  // A note-only change is another entry, not a rewrite of the previous one.
  await write(async () => {
    await feedback.fill(`sólo la nota cambia ${nonce}`);
    await page.getByRole("button", { name: "Guardar valoración", exact: true }).click();
  }, 4);
  const noted = await taskHistory(page, id);
  expect((noted.labels ?? []).length, "la nota añade su propio registro").toBe(4);
  expect((noted.labels ?? [])[3]?.action).toBe("label");
  expect((noted.labels ?? [])[3]?.feedback).toBe(`sólo la nota cambia ${nonce}`);
  expect((noted.labels ?? [])[0]?.feedback, "el primer cambio no se reescribe").toBe(
    `correcta ${nonce} con nota exacta`,
  );

  // An obsolete editor cannot overwrite a newer decision: another tab completes the task
  // first, and the open one keeps its old generation.
  const other = await page.context().newPage();
  const otherGuard = watchConsole(other);
  otherGuard.expected.push("/api/tasks/proposals/");
  await other.goto(route(TASKS_ROUTE, undefined, { task: id }));
  await expect(other.getByRole("button", { name: "Reabrir tarea", exact: true })).toBeVisible();
  await other.getByRole("button", { name: "Reabrir tarea", exact: true }).click();
  await expect(other.getByText(/Tarea reabierta/)).toBeVisible();
  const reopened = await taskById(page, id);
  expect(reopened.completed, "la otra pestaña reabrió la tarea").toBe(false);
  expect(reopened.completion_generation, "reabrir avanza la generación").toBe(2);
  const obsolete = page.waitForResponse(
    (response) => response.request().method() === "POST" && new URL(response.url()).pathname === `/api/tasks/${id}/label`,
  );
  await feedback.fill("esta pestaña quedó obsoleta");
  await page.getByRole("button", { name: "Guardar valoración", exact: true }).click();
  expect((await obsolete).status(), "la pestaña obsoleta es rechazada").toBe(409);
  await expect(page.getByText("La tarea cambió; vuelve a cargar su generación actual.")).toBeVisible();
  // The obsolete editor refreshes the whole task, history included: after its refusal the
  // panel shows the reopen and the revoked generation, not the state it was left with.
  const panel = page.locator('[data-slot="card"]').filter({ hasText: "Historial de esta tarea" });
  await expect(panel, "la pestaña obsoleta refresca el historial tras el 409").toContainText("Reabierta");
  await expect(panel).toContainText("revocó la generación 1");
  const notOverwritten = await taskById(page, id);
  expect(notOverwritten.completed, "la pestaña obsoleta no completó nada").toBe(false);
  expect(notOverwritten.feedback, "la pestaña obsoleta no escribió su nota").toBe("");
  expect(notOverwritten.completion_generation).toBe(2);
  await other.close();

  // Reopening retracts the valuation and the note in their own entries, and leaves the
  // task incomplete: the eligibility the installation checks is revoked with it.
  await page.reload();
  await expect(page.getByLabel("Valoración")).toBeDisabled();
  await expect(page.getByLabel("Valoración")).toHaveValue("pending");
  await expect(page.getByLabel("Qué funcionó o qué falló")).toHaveValue("");
  const retracted = await taskById(page, id);
  expect(retracted.status).toBe("incomplete");
  expect(retracted.feedback, "reabrir retira la nota").toBe("");
  expect(retracted.completion_confirmed_at, "reabrir retira la fecha de confirmación").toBeUndefined();
  const history = await taskHistory(page, id);
  const completions = history.completions ?? [];
  expect(completions.length, "cada transición de finalización tiene su registro").toBe(2);
  expect([completions[0]?.completed, completions[0]?.generation]).toEqual([true, 1]);
  expect([completions[1]?.completed, completions[1]?.generation, completions[1]?.revoked_generation]).toEqual([false, 2, 1]);
  expect((history.labels ?? []).length, "reabrir añade su propio registro").toBe(5);
  expect((history.labels ?? [])[4]).toMatchObject({ generation: 2, action: "label", label: "pending", feedback: "" });
  await expect(panel).toContainText(`sólo la nota cambia ${nonce}`);
  await expect(panel, "el primer cambio sigue visible en el historial").toContainText(
    `correcta ${nonce} con nota exacta`,
  );
  await expect(panel).toContainText("Reabierta");
  await expect(panel).toContainText("revocó la generación 1");
  guard.expected.push(`/api/foundry/tasks/${id}/derive`);
  const derive = await tasksApi(page, `/api/foundry/tasks/${encodeURIComponent(id)}/derive`, {});
  expect(derive.httpStatus, "una tarea reabierta no puede derivar").toBe(409);
  expect(derive.code).toBe("task_incomplete");
  const jobs = await read<{ jobs?: { subject?: string; kind?: string }[] }>(page, "/api/foundry/learning/state?compact=1");
  expect(
    (jobs.jobs ?? []).filter((job) => job.subject === id && job.kind === "derive").length,
    "el rechazo no dejó ningún trabajo de derivación",
  ).toBe(0);

  // None of the gates touched the captured evidence or the pinned citations.
  const final = await taskById(page, id);
  expect(JSON.stringify(final.citations ?? []), "las citas siguen exactas").toBe(citations);
  expect(final.revision, "la tarea sigue en su revisión").toBe(created.revision);
  expect(final.first_event).toBe(created.first_event);
  expect(final.last_event).toBe(created.last_event);
});

// TK-04/05: while the history of a task is still being read or cannot be read, the panel
// states that instead of claiming the task has no changes, and it never presents the record
// it could not read as the current one. The failures are real ones of this context: the
// browser blocks that exact URL (CDP setBlockedURLs, no stubbed answer) and a real latency.
test("TK-04/05: el historial declara su lectura, su fallo y nunca un vacío falso", async ({ page }) => {
  test.setTimeout(180_000);
  requireRelease();
  guard = watchConsole(page);
  guard.expected.push("/api/tasks/proposals/");
  const directory = required("E2E_TK03_DIR");
  const nonce = String(Date.now());
  const capturePath = join(directory, `tk04-panel-${nonce}.jsonl`);
  const title = `TK04 panel ${nonce}`;
  await writeFile(
    capturePath,
    [
      { session_id: `tk04-panel-${nonce}`, role: "user", kind: "message", text: title },
      { session_id: `tk04-panel-${nonce}`, role: "assistant", kind: "message", text: "respuesta del panel" },
    ]
      .map((record) => JSON.stringify(record))
      .join("\n") + "\n",
    "utf8",
  );

  await signIn(page);
  await page.goto(route(CONVERSATIONS_ROUTE, "sources"));
  await registerSourcePath(page, capturePath, "canonical");
  await scanSource(page, sourceRow(page, capturePath), "Terminada");
  await page.goto(route(CONVERSATIONS_ROUTE, "conversations"));
  await page.getByLabel("Buscar conversación").fill(title);
  await expect.poll(() => query(page.url(), "q")).toBeTruthy();
  await page.locator("tbody tr").filter({ hasText: title }).first().getByRole("button", { name: "Abrir", exact: true }).click();
  await page.getByLabel("Nombre de la tarea").fill(title);
  await page.getByRole("button", { name: "Guardar tarea", exact: true }).click();
  await expect(page.getByText("Tarea guardada.")).toBeVisible();
  await page.getByRole("button", { name: "Abrir tarea", exact: true }).click();
  await expect.poll(() => query(page.url(), "task")).toBeTruthy();
  const id = query(page.url(), "task") ?? "";
  await page.getByRole("button", { name: "Marcar tarea completada", exact: true }).click();
  await expect(page.getByText("Finalización confirmada. Ahora puedes valorar el resultado.")).toBeVisible();

  // The browser itself blocks the history read of this task (CDP setBlockedURLs, no stubbed
  // answer): the panel states the failure it really got and never invents an empty record.
  const client = await page.context().newCDPSession(page);
  await client.send("Network.enable");
  const historyURL = new URL(`/api/tasks/${encodeURIComponent(id)}/history`, page.url()).href;
  guard.expected.push(`/api/tasks/${id}/history`);
  const blocked = async (urls: string[]) => client.send("Network.setBlockedURLs", { urls });

  await blocked([historyURL]);
  await page.getByRole("button", { name: "Volver al histórico", exact: true }).click();
  await expect(page.locator("tbody tr").filter({ hasText: title }).first()).toBeVisible();
  await page.locator("tbody tr").filter({ hasText: title }).first().getByRole("button", { name: "Revisar tarea", exact: true }).click();
  const panel = page.locator('[data-slot="card"]').filter({ hasText: "Historial de esta tarea" });
  await expect(panel.getByText("No se pudo leer esta vista", { exact: true })).toBeVisible({ timeout: 20_000 });
  await expect(
    panel.getByText("Todavía no hay cambios que mostrar", { exact: false }),
    "una lectura fallida no se presenta como tarea sin cambios",
  ).toHaveCount(0);

  // Unblocking and retrying reads the real record again.
  await blocked([]);
  await panel.getByRole("button", { name: "Reintentar", exact: true }).click();
  await expect(panel.getByText("Completada y confirmada", { exact: false })).toBeVisible({ timeout: 20_000 });
  await expect(panel.getByText("No se pudo leer esta vista", { exact: true })).toHaveCount(0);

  // A real 3s latency on every request of this context: the next change of the task makes the
  // panel read its history again while the task itself is already on screen, and a read in
  // course is stated as one instead of being presented as a task without changes.
  await client.send("Network.emulateNetworkConditions", {
    offline: false,
    latency: 3000,
    downloadThroughput: -1,
    uploadThroughput: -1,
  });
  await page.getByRole("button", { name: "Reabrir tarea", exact: true }).click();
  await expect(panel.getByText("Leyendo el historial…", { exact: true })).toBeVisible({ timeout: 30_000 });
  await expect(
    panel.getByText("Todavía no hay cambios que mostrar", { exact: false }),
    "una lectura en curso no se presenta como tarea sin cambios",
  ).toHaveCount(0);
  await client.send("Network.emulateNetworkConditions", {
    offline: false,
    latency: 0,
    downloadThroughput: -1,
    uploadThroughput: -1,
  });
  await expect(panel.getByText("Reabierta", { exact: false })).toBeVisible({ timeout: 30_000 });
  await expect(panel.getByText("Todavía no hay cambios que mostrar", { exact: false })).toHaveCount(0);

  // The refresh of an accepted valuation is blocked: the failure is stated and the record it
  // could not read is not shown as if it were the current one. Retrying shows the new entry.
  await page.getByRole("button", { name: "Marcar tarea completada", exact: true }).click();
  await expect(page.getByText("Finalización confirmada. Ahora puedes valorar el resultado.")).toBeVisible();
  const note = `nota del panel ${nonce}`;
  await page.getByLabel("Valoración").selectOption("correct");
  await page.getByLabel("Qué funcionó o qué falló").fill(note);
  await blocked([historyURL]);
  await page.getByRole("button", { name: "Guardar valoración", exact: true }).click();
  await expect(page.getByText("Valoración guardada.")).toBeVisible();
  await expect(panel.getByText("No se pudo leer esta vista", { exact: true })).toBeVisible({ timeout: 20_000 });
  await expect(panel.getByText("Reabierta", { exact: false }), "el registro viejo no se muestra como actual").toHaveCount(0);
  await expect(panel.getByText("Todavía no hay cambios que mostrar", { exact: false })).toHaveCount(0);
  await blocked([]);
  await panel.getByRole("button", { name: "Reintentar", exact: true }).click();
  await expect(panel.getByText(note, { exact: false })).toBeVisible({ timeout: 20_000 });
  await client.detach();
});


// TK-02: two tabs of the screen sending the same new range at the same time are one task:
// the installation creates it once and answers the other write with that same task, without
// touching the title, the valuation or the note it already had, and a real overlap is still
// refused. Each range is new for this run, so the case never depends on stored decisions.
test("TK-02: dos pestañas con el mismo rango nuevo crean una sola tarea", async ({ page, browser }) => {
  test.setTimeout(240_000);
  requireRelease();
  guard = watchConsole(page);
  guard.expected.push("/api/tasks/proposals/");
  const directory = required("E2E_TK03_DIR");
  const nonce = String(Date.now());
  const capturePath = join(directory, `tk02-race-${nonce}.jsonl`);
  const title = `TK02 carrera ${nonce}`;
  const records: Record<string, string>[] = [];
  for (const index of [1, 2, 3, 4]) {
    records.push({ session_id: `tk02-race-${nonce}`, role: "user", kind: "message", text: `${title} petición ${index}` });
    records.push({ session_id: `tk02-race-${nonce}`, role: "assistant", kind: "message", text: `respuesta ${index}` });
  }
  await writeFile(capturePath, records.map((record) => JSON.stringify(record)).join("\n") + "\n", "utf8");

  await signIn(page);
  await page.goto(route(CONVERSATIONS_ROUTE, "sources"));
  await registerSourcePath(page, capturePath, "canonical");
  await scanSource(page, sourceRow(page, capturePath), "Terminada");
  const conversation = (await conversations(page, `q=${encodeURIComponent(title)}`))[0];
  const session = conversation?.id ?? "";
  expect(session).not.toBe("");
  const detail = route(CONVERSATIONS_ROUTE, "conversations", { conversation: session });

  // The second tab lives in its own context, so the two writes travel over their own
  // connections and arrive together the way two real tabs do.
  const context = await browser.newContext();
  const second = await context.newPage();
  const secondGuard = watchConsole(second);
  secondGuard.expected.push("/api/tasks/proposals/");
  await signIn(second);

  const ranges = [
    { start: "1", end: "1" },
    { start: "3", end: "3" },
    { start: "5", end: "5" },
  ];
  const reused: { id: string; range: { start: string; end: string } }[] = [];
  for (const tab of [page, second]) {
    await tab.goto(detail);
    await expect(tab.getByLabel("Nombre de la tarea")).toBeVisible();
  }
  for (const range of ranges) {
    for (const tab of [page, second]) {
      await tab.getByLabel("Nombre de la tarea").fill(title);
      await tab.getByLabel("Inicio", { exact: true }).fill(range.start);
      await tab.getByLabel("Fin", { exact: true }).fill(range.end);
      await expect(tab.getByRole("button", { name: "Guardar tarea", exact: true })).toBeEnabled();
    }
    const asks = [page, second].map((tab) =>
      tab.waitForResponse(
        (response) => response.request().method() === "POST" && new URL(response.url()).pathname === "/api/tasks",
      ),
    );
    await Promise.all(
      [page, second].map((tab) => tab.getByRole("button", { name: "Guardar tarea", exact: true }).click()),
    );
    const answers = [];
    for (const ask of asks) {
      const response = await ask;
      answers.push({ status: response.status(), body: (await response.json()) as { task?: { id?: string }; reused?: boolean } });
    }
    const statuses = answers.map((answer) => answer.status).sort();
    expect(statuses, `rango ${range.start}: una creación y una reutilización`).toEqual([200, 201]);
    const ids = new Set(answers.map((answer) => answer.body.task?.id ?? ""));
    expect(ids.size, `rango ${range.start}: las dos pestañas hablan de la misma tarea`).toBe(1);
    expect(answers.filter((answer) => answer.body.reused === true).length, `rango ${range.start}`).toBe(1);
    const id = [...ids][0] ?? "";
    expect(id).not.toBe("");
    const stored = await tasks(page, `session_id=${encodeURIComponent(session)}`);
    expect(stored.filter((row) => row.first_event === Number(range.start) - 1).length, `rango ${range.start}: una sola fila`).toBe(1);
    reused.push({ id, range });
  }
  const storedAfterRaces = await tasks(page, `session_id=${encodeURIComponent(session)}`);
  expect(storedAfterRaces.length, "tres rangos nuevos, tres tareas").toBe(3);

  // The decisions of the stored task are preserved by a repeated write, and a real overlap
  // is still refused by the installation.
  const chosen = reused[0];
  const id = chosen?.id ?? "";
  await second.goto(route(TASKS_ROUTE, undefined, { task: id }));
  await expect(second.getByRole("button", { name: "Marcar tarea completada", exact: true })).toBeVisible();
  const completing = second.waitForResponse(
    (response) => response.request().method() === "POST" && new URL(response.url()).pathname === `/api/tasks/${id}/completion`,
  );
  await second.getByRole("button", { name: "Marcar tarea completada", exact: true }).click();
  expect((await completing).status()).toBe(200);
  await second.getByLabel("Valoración").selectOption("correct");
  await second.getByLabel("Qué funcionó o qué falló").fill(`nota de la carrera ${nonce}`);
  const valuing = second.waitForResponse(
    (response) => response.request().method() === "POST" && new URL(response.url()).pathname === `/api/tasks/${id}/label`,
  );
  await second.getByRole("button", { name: "Guardar valoración", exact: true }).click();
  expect((await valuing).status()).toBe(200);
  await second.goto(detail);
  await page.getByLabel("Nombre de la tarea").fill(title);
  await page.getByLabel("Inicio", { exact: true }).fill(chosen?.range.start ?? "1");
  await page.getByLabel("Fin", { exact: true }).fill(chosen?.range.end ?? "1");
  const repeat = page.waitForResponse(
    (response) => response.request().method() === "POST" && new URL(response.url()).pathname === "/api/tasks",
  );
  await page.getByRole("button", { name: "Guardar tarea", exact: true }).click();
  const repeated = (await (await repeat).json()) as { task?: { id?: string; feedback?: string; label?: string }; reused?: boolean };
  expect(repeated.reused, "repetir el rango reutiliza la tarea").toBe(true);
  expect(repeated.task?.id).toBe(id);
  expect(repeated.task?.feedback, "la nota guardada no se pisa").toBe(`nota de la carrera ${nonce}`);
  await expect(page.getByText("La tarea ya existía; se conservó su valoración.")).toBeVisible();
  expect((await tasks(page, `session_id=${encodeURIComponent(session)}`)).length, "repetir no añade otra tarea").toBe(3);

  await page.getByLabel("Inicio", { exact: true }).fill("1");
  await page.getByLabel("Fin", { exact: true }).fill("2");
  await expect(page.getByRole("button", { name: "Guardar tarea", exact: true })).toBeEnabled();
  // The overlap below is refused on purpose: that 409 is the contract, not a console error.
  guard.expected.push(new URL("/api/tasks", page.url()).href);
  const overlapping = page.waitForResponse(
    (response) => response.request().method() === "POST" && new URL(response.url()).pathname === "/api/tasks",
  );
  await page.getByRole("button", { name: "Guardar tarea", exact: true }).click();
  expect((await overlapping).status(), "el solape real sigue rechazado").toBe(409);
  await expect(page.getByText("El rango se solapa con otra tarea de esta revisión.")).toBeVisible();
  expect((await tasks(page, `session_id=${encodeURIComponent(session)}`)).length).toBe(3);
  const final = await taskById(page, id);
  expect(final.feedback, "la nota sigue siendo la guardada").toBe(`nota de la carrera ${nonce}`);
  expect(final.label).toBe("correct");
  expect(final.completed).toBe(true);
  await context.close();
});

/** Writes one fixture file of the boundary case, in its own directory. */
async function writeBoundaryFile(dir: string, name: string, text: string, records: number) {
  await mkdir(dir, { recursive: true });
  const lines: string[] = [];
  for (let index = 0; index < records; index += 1) {
    lines.push(JSON.stringify({ session_id: `boundary-${name}-${index}`, role: "user", kind: "message", text: index === 0 ? text : `${text} seguimiento ${index}` }));
    lines.push(JSON.stringify({ session_id: `boundary-${name}-${index}`, role: "assistant", kind: "message", text: "respuesta del fixture" }));
  }
  await writeFile(join(dir, name.endsWith(".json") ? name : `${name}.jsonl`), lines.join("\n") + "\n", "utf8");
}

// CV-01/CV-02: the ingestion boundary refuses credentials, keys and tool configuration before
// reading them, a mixed tree keeps capturing only what it may, the OpenCode history store is
// reported as unsupported instead of being read as another format, and an absent path is not
// registered. Every fixture here is new and belongs to this case.
test("CV-01/02: la frontera de ingesta no lee credenciales, claves ni el historial de OpenCode", async ({
  page,
}) => {
  test.setTimeout(240_000);
  requireRelease();
  guard = watchConsole(page);
  guard.expected.push("/api/tasks/proposals/");
  const directory = required("E2E_CV01_DIR");
  const nonce = String(Date.now());
  // Every run owns a new tree: a previous pass never decides what this one may register.
  const mixed = join(directory, `mixed-${nonce}`);
  const canonical = join(directory, `canonical-${nonce}`);
  const markers = {
    auth: `CV01-GREEN-AUTH-${nonce}`,
    credentials: `CV01-GREEN-CREDENTIALS-${nonce}`,
    ssh: `CV01-GREEN-SSH-${nonce}`,
    opencode: `CV02-GREEN-OPENCODE-${nonce}`,
    xdg: `CV02-GREEN-XDG-${nonce}`,
    allowed: `CV01-GREEN-ALLOWED-${nonce}`,
    discovered: `CV01-GREEN-DISCOVER-${nonce}`,
  };
  await writeBoundaryFile(join(mixed, "auth"), "auth", markers.auth, 2);
  await writeBoundaryFile(join(mixed, "credentials"), "credentials", markers.credentials, 2);
  await writeBoundaryFile(join(mixed, ".ssh"), "authorized_keys", markers.ssh, 2);
  await writeBoundaryFile(join(mixed, ".opencode", "storage", "session", "project"), "session.json", markers.opencode, 2);
  await writeBoundaryFile(join(mixed, ".local", "share", "opencode", "storage", "session"), "session.json", markers.xdg, 2);
  await writeBoundaryFile(join(mixed, "allowed"), "session", markers.allowed, 2);
  await writeBoundaryFile(canonical, "session", markers.discovered, 2);

  await signIn(page);
  await page.goto(route(CONVERSATIONS_ROUTE, "sources"));
  const sourceRows = await sources(page);
  expect(sourceRows.some((source) => source.path === mixed)).toBe(false);

  // A direct sensitive root, a file inside one and the OpenCode store are refused with their
  // own reason, and a path that does not exist is not registered either.
  // The five registrations below are refused on purpose: those 400 are the contract.
  guard.expected.push(new URL("/api/conversations/sources", page.url()).href);
  guard.expected.push(new URL("/api/conversations/sources/", page.url()).href);
  for (const [path, expected] of [
    [join(mixed, "auth"), "La fuente no puede ser credenciales, claves ni configuración de herramientas."],
    [join(mixed, "credentials", "credentials.jsonl"), "La fuente no puede ser credenciales, claves ni configuración de herramientas."],
    [join(mixed, ".ssh", "authorized_keys.jsonl"), "La fuente no puede estar dentro de credenciales, claves ni tokens."],
    [join(mixed, ".opencode"), "El historial de OpenCode no está soportado en esta entrega y no se ingiere con otro adaptador."],
    [join(mixed, ".local", "share", "opencode", "storage"), "El historial de OpenCode no está soportado en esta entrega y no se ingiere con otro adaptador."],
    [join(directory, `absent-${nonce}`), "La ruta explícita no existe o no es accesible."],
  ] as const) {
    const response = page.waitForResponse(
      (candidate) => candidate.request().method() === "POST" && new URL(candidate.url()).pathname === "/api/conversations/sources",
    );
    await page.getByRole("button", { name: "Añadir fuente", exact: true }).first().click();
    const dialog = page.getByRole("dialog");
    await dialog.getByLabel("Ruta o archivo de la fuente").fill(path);
    await dialog.getByRole("button", { name: "Registrar fuente", exact: true }).click();
    expect((await response).status(), `registro de ${path}`).toBe(400);
    await expect(dialog.getByText(expected)).toBeVisible();
    await dialog.getByRole("button", { name: "Cancelar", exact: true }).click();
    await expect(dialog).toBeHidden();
  }
  const refused = await sources(page);
  expect(refused.map((source) => source.path)).toEqual(sourceRows.map((source) => source.path));

  // The mixed tree is registered and scanned: only the allowed capture is ingested, the
  // exclusions are not capture errors, and no marker of the sensitive subtrees is searchable.
  await registerSourcePath(page, mixed, "canonical");
  const scan = await scanSource(page, sourceRow(page, mixed), "Terminada");
  const receipt = await jobReceipt(page, scan.jobId);
  expect(receipt.status).toBe("succeeded");
  expect(receipt.result?.files, "sólo el archivo permitido").toBe(1);
  expect(receipt.result?.errors, "una exclusión no es un error de captura").toBe(0);
  const stored = await storedSource(page, mixed);
  expect(stored.coverage?.status).toBe("complete");
  expect(stored.coverage?.blocking_issue_count).toBe(0);
  const captured = await conversations(page, `source_id=${stored.id}`);
  expect(captured.map((row) => row.title)).toEqual([markers.allowed]);

  await page.goto(route(CONVERSATIONS_ROUTE, "conversations"));
  const search = page.getByLabel("Buscar conversación");
  for (const marker of [markers.auth, markers.credentials, markers.ssh, markers.opencode, markers.xdg]) {
    await search.fill(marker);
    await expect.poll(() => query(page.url(), "q")).toBe(marker);
    await expect(page.locator("tbody tr"), `marcador ingerido ${marker}`).toHaveCount(0);
    await expect(page.getByText("Sin coincidencias", { exact: true })).toBeVisible();
  }
  await search.fill(markers.allowed);
  await expect.poll(() => query(page.url(), "q")).toBe(markers.allowed);
  await expect(page.locator("tbody tr").filter({ hasText: markers.allowed })).toHaveCount(1);

  // The same boundary holds when a registered source is pointed at a sensitive path: the
  // change is refused and the source keeps the path it had.
  await page.goto(route(CONVERSATIONS_ROUTE, "sources"));
  const relink = page.waitForResponse(
    (candidate) => candidate.request().method() === "PATCH" && new URL(candidate.url()).pathname.startsWith("/api/conversations/sources/"),
  );
  await sourceCard(page, mixed).getByRole("button", { name: "Cambiar ruta", exact: true }).first().click();
  const relinkDialog = page.getByRole("dialog");
  await relinkDialog.getByLabel("Nueva ruta").fill(join(mixed, "auth"));
  await relinkDialog.getByRole("button", { name: "Guardar cambio", exact: true }).click();
  expect((await relink).status(), "mover una fuente a credenciales se rechaza").toBe(400);
  await expect(relinkDialog.getByText("La fuente no puede ser credenciales, claves ni configuración de herramientas.")).toBeVisible();
  await relinkDialog.getByRole("button", { name: "Cancelar", exact: true }).click();
  expect((await storedSource(page, mixed)).path, "la fuente conserva su ruta").toBe(mixed);

  // Discovery reads only the roots it is given: the canonical tree is a candidate, the
  // OpenCode store is stated as unsupported with its reason, and nothing is auto-registered.
  await page.goto(route(CONVERSATIONS_ROUTE, "sources"));
  const before = (await sources(page)).length;
  await page.getByRole("button", { name: "Detectar fuentes", exact: true }).click();
  const discover = page.getByRole("dialog");
  await discover.getByLabel("Raíz 1").fill(canonical);
  await discover.getByRole("button", { name: "Añadir raíz", exact: true }).click();
  await discover.getByLabel("Raíz 2").fill(join(mixed, ".opencode"));
  const detected = page.waitForResponse(
    (candidate) => candidate.request().method() === "POST" && new URL(candidate.url()).pathname === "/api/conversations/sources/discover",
  );
  await discover.getByRole("button", { name: "Detectar", exact: true }).click();
  expect((await detected).status()).toBe(200);
  await expect(page.getByText(canonical, { exact: false }).first()).toBeVisible();
  await expect(page.getByText("Herramientas no soportadas", { exact: true })).toBeVisible();
  await expect(page.getByText(/opencode_history_not_supported/).first()).toBeVisible();
  await expect(page.getByText(join(mixed, ".opencode"), { exact: false }).first()).toBeVisible();
  expect((await sources(page)).length, "detectar no registra fuentes").toBe(before);
});
