import { execFileSync } from "node:child_process";
import { readFileSync, renameSync } from "node:fs";
import { expect, test, type Locator, type Page, type Request } from "@playwright/test";
import { BASE, requireRelease, signIn, TOKEN, watchConsole, type ConsoleGuard } from "./helpers";

// OP-08 (observabilidad y soporte) sobre el release real: una operación existente sin
// proveedor ni IA —la captura de una fuente propia— desde el navegador, con su recibo
// correlacionable (respuesta de la petición → job → efecto durable), y la lectura de los
// canales de soporte que el producto ya sirve. No hay logger, panel ni opción nuevos: el
// canal del navegador es Ajustes→Diagnóstico (GET /api/diagnostics) más el recibo del job
// y la fila de la fuente; el log propio de la instalación sigue siendo local (archivo del
// proceso) y se comprueba como tal, nunca se presenta como visor del producto. PL-09 ya
// cubre el hijo gestionado sano/detenido: aquí no se repite, sólo se lee su estado.
//
// Preconditions prepared by this case's owner (no defaults, no fallback):
//   E2E_FIXTURE_SOURCE        absolute root of the fixture source to capture
//   E2E_FIXTURE_LOG           absolute path of this installation's own log file
//   E2E_OP08_CANARIES_FILE    file with one own canary per line (outside the source root)
//   E2E_RESTART_COMMAND       executable that restarts only this installation (durability)
//
//   E2E_BASE_URL=http://127.0.0.1:19080 E2E_ACCESS_TOKEN=... \
//     bun run test tests/e2e/conversations-support-release.spec.ts

const CONVERSATIONS_ROUTE = process.env.E2E_CONVERSATIONS_ROUTE ?? "/?section=conversations";

let guard: ConsoleGuard | null = null;

test.beforeEach(() => {
  guard = null;
});

test.afterEach(() => {
  expect(guard?.errors ?? [], "consola del navegador").toEqual([]);
  guard = null;
});

function required(name: string) {
  const value = process.env[name];
  if (!value)
    throw new Error(
      `${name} is not set: prepare that fixture for the installation under test (no default, no fallback).`,
    );
  if (!value.startsWith("/"))
    throw new Error(`${name} must be an absolute server path, got "${value}"`);
  return value;
}

function route(base: string, view?: string) {
  const url = new URL(base, "http://workbench.invalid");
  if (view) url.searchParams.set("view", view);
  return url.pathname + url.search;
}

interface ScanResult {
  files?: number;
  changed?: number;
  sessions?: number;
  errors?: number;
  limited?: boolean;
  coverage?: { status?: string; issue_count?: number; issues?: { file_name?: string; reason?: string }[] };
}

interface JobReceipt extends ScanResult {
  job_id: string;
  kind: string;
  subject: string;
  status: string;
  error?: string;
  progress?: ScanResult;
  result?: ScanResult;
}

interface SourceRow {
  id: string;
  path: string;
  adapter: string;
  enabled: boolean;
  error?: string;
  last_scan?: { status?: string; files?: number; changed?: number; errors?: number; limited?: boolean };
  coverage?: { status?: string; issue_count?: number };
}

interface ConversationRow {
  id: string;
  source_id: string;
  title: string;
  revision: string;
  coverage?: { status?: string; event_count?: number };
}

interface Diagnostics {
  application: string;
  status: string;
  modules: Record<string, string>;
  routes_not_ported: string[];
  native_module?: { id: string; state: string; healthy?: boolean; probes?: number; events?: { event: string; detail?: string }[] };
}

/** Support channel the product already serves: the authenticated diagnostics report.
 *  Every call is one real read; nothing here is mocked or intercepted. */
async function diagnostics(page: Page): Promise<Diagnostics> {
  const response = await page.request.get("/api/diagnostics");
  expect(response.status(), "GET /api/diagnostics").toBe(200);
  return (await response.json()) as Diagnostics;
}

async function bootstrap(page: Page): Promise<{ status: string }> {
  const response = await page.request.get("/api/bootstrap");
  expect(response.status(), "GET /api/bootstrap").toBe(200);
  const body = (await response.json()) as { status: string };
  return body;
}

async function sources(page: Page): Promise<SourceRow[]> {
  const response = await page.request.get("/api/conversations/sources");
  expect(response.status(), "GET /api/conversations/sources").toBe(200);
  const body = (await response.json()) as { sources?: SourceRow[] };
  return body.sources ?? [];
}

async function conversations(page: Page): Promise<ConversationRow[]> {
  const response = await page.request.get("/api/conversations");
  expect(response.status(), "GET /api/conversations").toBe(200);
  const body = (await response.json()) as { sessions?: ConversationRow[] };
  return body.sessions ?? [];
}

/** Receipt of one capture job exactly as the installation persists it. */
async function jobReceipt(page: Page, jobId: string): Promise<JobReceipt> {
  const response = await page.request.get(`/api/foundry/jobs/${encodeURIComponent(jobId)}`);
  expect(response.status(), `GET /api/foundry/jobs/${jobId}`).toBe(200);
  return (await response.json()) as JobReceipt;
}

async function revisionBody(page: Page, conversationId: string, revision: string) {
  const response = await page.request.get(
    `/api/conversations/${encodeURIComponent(conversationId)}/revisions/${encodeURIComponent(revision)}?limit=100`,
  );
  expect(response.status(), "GET revision of the captured conversation").toBe(200);
  return await response.text();
}

/** My own canaries: values this fixture owns, never an operator secret. */
function canaries(): string[] {
  return readFileSync(required("E2E_OP08_CANARIES_FILE"), "utf8")
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line.length > 0 && !line.startsWith("#"));
}

/** Control of the redaction case: the canaries really are in the captured input, so
 *  their absence from the support channels below is a result and not an empty file. */
function expectFixtureCarriesCanaries(sourcePath: string) {
  const transcript = readFileSync(`${sourcePath}/session.jsonl`, "utf8");
  const credencial = readFileSync(`${sourcePath}/credentials.json`, "utf8");
  for (const canary of canaries()) {
    expect(
      transcript.includes(canary) || credencial.includes(canary),
      `la fixture debe contener su propio canario ${canary.slice(0, 12)}…`,
    ).toBe(true);
  }
  expect(transcript).toContain("op08-canary-password-value");
  expect(credencial).toContain("op08-canary-file-credential-");
}

/** None of my own canaries, and not the session token of this instance either: the
 *  support and log channels publish state, never authentication material. */
function expectNoCanary(text: string, where: string) {
  const values = [...canaries(), TOKEN ?? ""].filter((value) => value.length > 0);
  expect(values.length, "the fixture must declare its own canaries").toBeGreaterThan(0);
  for (const value of values) {
    expect(text, `${where} published a canary or the session token`).not.toContain(value);
  }
}

// The installation's own log file is local evidence of this private fixture, not a
// product screen: it is read from disk to show what the browser channel does not publish.
const localLog = () => readFileSync(required("E2E_FIXTURE_LOG"), "utf8");

/** One source row: the deepest container of its own path that also owns its scan control. */
function sourceRow(page: Page, path: string) {
  return page
    .locator("div")
    .filter({ hasText: path })
    .filter({ has: page.getByRole("button", { name: "Escanear", exact: true }) })
    .last();
}

async function registerSource(page: Page, path: string) {
  if ((await sources(page)).some((source) => source.path === path)) return;
  await page.getByRole("button", { name: "Añadir fuente", exact: true }).first().click();
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("Ruta o archivo de la fuente").fill(path);
  await dialog.getByLabel("Adaptador").selectOption("canonical");
  await dialog.getByRole("button", { name: "Registrar fuente", exact: true }).click();
  await expect(dialog).toBeHidden();
}

/** One manual capture from the screen, followed to its terminal state: the request
 *  receipt (202 + job id) and the job the screen really polled are correlated here. */
async function scanFromScreen(page: Page, row: Locator, terminal: "Terminada" | "Falló") {
  const polls: string[] = [];
  const observe = (request: Request) => {
    const url = request.url();
    if (url.includes("/api/foundry/jobs/")) polls.push(url);
  };
  page.on("request", observe);
  try {
    const answered = page.waitForResponse(
      (response) => response.request().method() === "POST" && response.url().endsWith("/scan"),
    );
    await row.getByRole("button", { name: "Escanear", exact: true }).click();
    const response = await answered;
    expect(response.status(), "POST …/scan").toBe(202);
    const receipt = (await response.json()) as { job_id: string; source_id: string; status: string };
    // The capture panel is a house card whose title is not a heading; the same filter
    // the neighbouring capture spec uses keeps this card apart from the sources panel.
    const card = page.locator('[data-slot="card"]').filter({ hasText: "Captura en curso" });
    await expect(card.getByText(terminal, { exact: true })).toBeVisible({ timeout: 60_000 });
    // The card names the subject the request returned: the screen follows this job.
    await expect(card).toContainText(receipt.source_id);
    expect(
      polls.some((url) => url.endsWith(`/api/foundry/jobs/${receipt.job_id}`)),
      `la pantalla no siguió el job ${receipt.job_id} que devolvió la petición`,
    ).toBe(true);
    return { receipt, card, polls };
  } finally {
    page.off("request", observe);
  }
}

test("OP-08: la captura real deja un recibo correlacionable y durable, sin publicar canarios", async ({
  page,
}) => {
  test.setTimeout(180_000);
  requireRelease();
  guard = watchConsole(page);
  const sourcePath = required("E2E_FIXTURE_SOURCE");
  const restart = required("E2E_RESTART_COMMAND");
  await signIn(page);

  // Estado de soporte antes de la operación: el mismo informe autenticado que sirve
  // Ajustes→Diagnóstico, leído aquí como canal del producto (no se repite la campaña
  // de PL-09 sobre el hijo gestionado; sólo se usa su estado honesto como referencia).
  const before = await diagnostics(page);
  expect(before.status, "el estado de la aplicación es honesto, no un 'ok' decorativo").toBe(
    (await bootstrap(page)).status,
  );
  expect(["partial", "degraded"]).toContain(before.status);
  expect(before.modules.foundry).toBe("mounted");
  expectNoCanary(JSON.stringify(before), "GET /api/diagnostics");

  await page.goto(route(CONVERSATIONS_ROUTE, "sources"));
  await registerSource(page, sourcePath);
  const rows = await sources(page);
  const stored = rows.find((source) => source.path === sourcePath);
  if (!stored) throw new Error(`the installation did not keep the source ${sourcePath}`);
  const row = sourceRow(page, sourcePath);

  // La operación real: la captura sale del navegador y la petición devuelve el recibo
  // que la pantalla debe seguir. El job pertenece a esta fuente, no a otra.
  const { receipt, card } = await scanFromScreen(page, row, "Terminada");
  expect(receipt.status).toBe("queued");
  expect(receipt.source_id, "el recibo de la petición apunta a la fuente pedida").toBe(stored.id);

  // Correlación petición → job → efecto: el mismo id, la misma fuente y el resultado real.
  const job = await jobReceipt(page, receipt.job_id);
  expect(job.job_id).toBe(receipt.job_id);
  expect(job.kind).toBe("scan");
  expect(job.subject, "el job conserva el sujeto que devolvió la petición").toBe(receipt.source_id);
  expect(job.status).toBe("succeeded");
  expect(job.result?.sessions ?? 0, "la captura durable cuenta su sesión").toBeGreaterThanOrEqual(1);
  expect(job.result?.coverage?.status).toBe("complete");
  expect(
    job.result?.coverage?.issues?.some(
      (issue) => issue.file_name === "credentials.json" && issue.reason === "sensitive_path_excluded",
    ),
    "la exclusión del archivo sensible se reporta con su nombre y no se oculta",
  ).toBe(true);

  // La pantalla pinta exactamente ese cuerpo, nunca una copia propia: el panel muestra
  // el progreso del job y su resultado, y el sujeto correlacionado de la petición.
  await expect(card).toContainText(`Archivos: ${job.progress?.files ?? 0}`);
  await expect(card).toContainText(`Errores: ${job.progress?.errors ?? 0}`);
  await expect(card).toContainText(`Sesiones capturadas: ${job.result?.sessions ?? 0}`);
  await expect(card).toContainText("completa");

  // Efecto durable en la fila de la fuente y en la conversación capturada.
  const afterScan = (await sources(page)).find((source) => source.id === stored.id);
  expect(afterScan?.last_scan?.status).toBe("complete");
  expect(afterScan?.last_scan?.errors).toBe(0);
  expect(afterScan?.error ?? "").toBe("");
  await expect(row).toContainText("último escaneo: completa");
  const captured = (await conversations(page)).filter((item) => item.source_id === stored.id);
  expect(captured.length, "una fuente con un archivo deja una conversación, sin duplicar").toBe(1);
  const revision = captured[0].revision;

  // Redacción: el canario del propio fixture no aparece en ningún canal de soporte ni
  // log, y la captura muestra la redacción en vez de callarla. El archivo con nombre de
  // credencial ni se abre: su valor no puede estar en ninguna parte.
  expectFixtureCarriesCanaries(sourcePath);
  const body = await revisionBody(page, captured[0].id, revision);
  expect(body, "la redacción real de la captura queda a la vista").toContain("[REDACTED:");
  for (const [where, text] of [
    ["la revisión capturada", body],
    ["el recibo del job", JSON.stringify(job)],
    ["la lista de fuentes", JSON.stringify(await sources(page))],
    ["el informe de diagnóstico", JSON.stringify(await diagnostics(page))],
    ["el log local de la instalación", localLog()],
  ] as const) {
    expectNoCanary(text, where);
  }

  // Recarga: el recibo duradero y la conversación siguen ahí con la misma revisión.
  await page.reload();
  await expect(sourceRow(page, sourcePath)).toContainText("último escaneo: completa");
  expect((await jobReceipt(page, receipt.job_id)).status).toBe("succeeded");
  expect((await conversations(page)).filter((item) => item.source_id === stored.id)[0]?.revision).toBe(
    revision,
  );

  // Reinicio de la instalación: la correlación y el efecto sobreviven; ninguna lectura
  // inventa un estado nuevo para el job ya terminado.
  const restarting = guard?.expected.push(new URL(BASE!).host) ?? -1;
  execFileSync(restart, { stdio: "pipe" });
  await expect
    .poll(
      async () => {
        try {
          return (await page.request.get("/api/bootstrap", { timeout: 2000 })).status();
        } catch {
          return 0;
        }
      },
      { message: "la instalación reiniciada debe volver a servir", timeout: 90_000, intervals: [500] },
    )
    .toBe(200);
  if (guard && restarting >= 0) guard.expected.splice(restarting, 1);
  const restarted = await jobReceipt(page, receipt.job_id);
  expect(restarted.status).toBe("succeeded");
  expect(restarted.subject).toBe(receipt.source_id);
  expect(restarted.result?.sessions).toBe(job.result?.sessions);
  const survived = (await conversations(page)).filter((item) => item.source_id === stored.id);
  expect(survived.length).toBe(1);
  expect(survived[0].revision).toBe(revision);
  await page.reload();
  await expect(sourceRow(page, sourcePath)).toContainText("último escaneo: completa");
});

test("OP-08: una captura que falla no se pinta como éxito y vuelve al recuperar", async ({ page }) => {
  test.setTimeout(180_000);
  requireRelease();
  guard = watchConsole(page);
  const sourcePath = required("E2E_FIXTURE_SOURCE");
  const moved = `${sourcePath}-op08-moved`;
  await signIn(page);

  await page.goto(route(CONVERSATIONS_ROUTE, "sources"));
  await registerSource(page, sourcePath);
  const stored = (await sources(page)).find((source) => source.path === sourcePath);
  if (!stored) throw new Error(`the installation did not keep the source ${sourcePath}`);
  const baseline = (await conversations(page)).filter((item) => item.source_id === stored.id);
  expect(baseline.length, "la fixture debe empezar con su conversación capturada").toBe(1);
  const beforeDiagnostics = await diagnostics(page);
  expect(beforeDiagnostics.native_module?.state).toBe("ready");

  // El propio caso rompe SU fixture: la raíz desaparece y la captura real falla.
  renameSync(sourcePath, moved);
  try {
    const { receipt, card } = await scanFromScreen(page, sourceRow(page, sourcePath), "Falló");
    const job = await jobReceipt(page, receipt.job_id);
    expect(job.subject).toBe(stored.id);
    expect(job.status).toBe("failed");
    expect(job.error, "el recibo nombra el fallo, no un éxito vacío").toBe("scan_errors");
    expect(job.result?.errors ?? 0).toBeGreaterThanOrEqual(1);
    await expect(card).toContainText("scan_errors");
    await expect(card.getByText("Terminada", { exact: true })).toHaveCount(0);

    // Estado honesto: la fuente queda fallida y parcial, la captura anterior no se
    // reescribe como si nada, y el estado de soporte no maquilla nada como sano.
    const failed = (await sources(page)).find((source) => source.id === stored.id);
    expect(failed?.last_scan?.status).toBe("failed");
    expect(failed?.last_scan?.errors ?? 0).toBeGreaterThanOrEqual(1);
    expect(failed?.coverage?.status).toBe("partial");
    expect(failed?.error, "la fila conserva el fallo visible").toBe("scan_failed");
    const row = sourceRow(page, sourcePath);
    await expect(row).toContainText("último escaneo: Falló");
    await expect(row).toContainText("scan_failed");
    const afterFailure = (await conversations(page)).filter((item) => item.source_id === stored.id);
    expect(afterFailure.length, "un fallo no crea ni duplica conversaciones").toBe(1);
    expect(afterFailure[0].revision).toBe(baseline[0].revision);

    const diagnosticsAfter = await diagnostics(page);
    expect(diagnosticsAfter.status, "el estado global sigue siendo el real").toBe(beforeDiagnostics.status);
    expect(diagnosticsAfter.modules.foundry, "montado no es sano").toBe("mounted");
    expect(diagnosticsAfter.native_module?.state, "el módulo gestionado no cambia por este fallo").toBe(
      beforeDiagnostics.native_module?.state,
    );
    // El armazón sigue diciendo lo mismo que el informe: parcial, no un "listo" nuevo.
    await expect(page.getByText("estado parcial", { exact: false }).first()).toBeVisible();
    for (const [where, text] of [
      ["el recibo del job fallido", JSON.stringify(job)],
      ["la fila fallida de la fuente", JSON.stringify(failed)],
      ["el informe de diagnóstico tras el fallo", JSON.stringify(diagnosticsAfter)],
      ["el log local tras el fallo", localLog()],
    ] as const) {
      expectNoCanary(text, where);
    }
  } finally {
    // La fixture nunca queda rota, ni siquiera si un assert falla antes.
    renameSync(moved, sourcePath);
  }

  // Recuperación real desde la pantalla: la misma fuente vuelve a capturar completa y
  // el fallo deja de estar vigente sin borrar la identidad de la conversación.
  const { receipt: recovered } = await scanFromScreen(page, sourceRow(page, sourcePath), "Terminada");
  const recoveredJob = await jobReceipt(page, recovered.job_id);
  expect(recoveredJob.status).toBe("succeeded");
  expect(recoveredJob.result?.errors).toBe(0);
  expect(recoveredJob.result?.coverage?.status).toBe("complete");
  const healed = (await sources(page)).find((source) => source.id === stored.id);
  expect(healed?.last_scan?.status).toBe("complete");
  expect(healed?.coverage?.status).toBe("complete");
  expect(healed?.error ?? "").toBe("");
  const after = (await conversations(page)).filter((item) => item.source_id === stored.id);
  expect(after.length, "la recuperación no duplica la captura").toBe(1);
  expect(after[0].revision).toBe(baseline[0].revision);

  await page.reload();
  const restored = sourceRow(page, sourcePath);
  await expect(restored).toContainText("último escaneo: completa");
  await expect(restored).not.toContainText("scan_failed");
  expectNoCanary(localLog(), "el log local tras la recuperación");
});
