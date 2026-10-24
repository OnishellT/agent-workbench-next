import { execFileSync } from "node:child_process";
import { readFileSync, readlinkSync, statSync } from "node:fs";
import { expect, test, type Page } from "@playwright/test";
import { BASE, requireRelease, signIn, watchConsole, type ConsoleGuard } from "./helpers";

// M02 UI-08: estados de lectura (carga, vacío, fallo, reintento y cancelación) sobre el
// release real. La fixture es la instalación de quien ejecuta el caso: su propio Root,
// su hijo Magpie gestionado y un ejecutable que la reinicia. El fallo se provoca de
// verdad (señal a ESE proceso), nunca con una respuesta falsa del navegador.
//
//   E2E_BASE_URL=http://127.0.0.1:19064 E2E_ACCESS_TOKEN=... \
//   E2E_FIXTURE_PID_FILE=.../run/workbench.pid E2E_FIXTURE_DIR=... \
//   E2E_RESTART_COMMAND=.../restart.sh \
//     bun run test tests/e2e/shell-read-states-release.spec.ts
//
// La instalación debe tener al menos una conversación capturada: la pantalla no rellena
// nada y este caso no la sustituye por una lista inventada.

let guard: ConsoleGuard | null = null;

test.beforeAll(requireRelease);

test.beforeEach(({ page }) => {
  guard = watchConsole(page);
});

test.afterEach(() => {
  expect(guard?.errors ?? [], "consola del navegador").toEqual([]);
  guard = null;
});

interface ReadStateFixture {
  root: number;
  dir: string;
  child: number;
  restart: string;
}

/** La instalación que este caso puede parar y detener: el pid lo publica la fixture y
 *  todo lo demás se verifica desde /proc antes de señalizar nada. */
function requiredReadStateFixture(): ReadStateFixture {
  const pidFile = process.env.E2E_FIXTURE_PID_FILE;
  const dir = process.env.E2E_FIXTURE_DIR;
  const restart = process.env.E2E_RESTART_COMMAND;
  if (!pidFile || !dir || !restart)
    throw new Error(
      "This case stops, pauses and restarts the fixture installation: set E2E_FIXTURE_PID_FILE, E2E_FIXTURE_DIR and E2E_RESTART_COMMAND (an executable that restarts it). No default, no fallback.",
    );
  if (!dir.startsWith("/") || dir.endsWith("/"))
    throw new Error(`E2E_FIXTURE_DIR must be an absolute directory without a trailing slash, got "${dir}"`);
  const root = Number(readFileSync(pidFile, "utf8").trim());
  if (!Number.isInteger(root) || root <= 0)
    throw new Error(`the fixture pid file ${pidFile} does not name a process`);
  if (statSync(`/proc/${root}`).uid !== process.getuid?.())
    throw new Error(`installation ${root} is not owned by this user`);
  if (readlinkSync(`/proc/${root}/cwd`) !== dir)
    throw new Error(`the installation ${root} does not run in the fixture directory ${dir}`);
  const children = readFileSync(`/proc/${root}/task/${root}/children`, "utf8").trim().split(/\s+/).filter(Boolean);
  if (children.length !== 1)
    throw new Error(`installation ${root} must supervise exactly one child, saw ${children.join(",")}`);
  const child = Number(children[0]);
  if (readlinkSync(`/proc/${child}/exe`).split("/").pop() !== "magpie")
    throw new Error(`process ${child} is not the managed Magpie of installation ${root}`);
  if (!statSync(restart).isFile())
    throw new Error(`E2E_RESTART_COMMAND ${restart} is not a file`);
  return { root, dir, child, restart };
}

function alive(pid: number) {
  try {
    return statSync(`/proc/${pid}`).isDirectory();
  } catch {
    return false;
  }
}

/** One read of the real API: the boundary where a JSON body becomes a named shape. */
async function readJSON<T>(page: Page, path: string): Promise<T> {
  const response = await page.request.get(path);
  expect(response.status(), path).toBe(200);
  return (await response.json()) as T;
}

/** Reinicio real de la instalación de este caso, esperando a que vuelva a servir. */
async function restartInstallation(page: Page, fixture: ReadStateFixture) {
  execFileSync(fixture.restart, { stdio: "pipe" });
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
}

/** Parada real: la instalación termina y el navegador queda sin servicio. */
async function stopInstallation(page: Page, fixture: ReadStateFixture) {
  const host = guard?.expected.push(new URL(BASE!).host) ?? -1;
  process.kill(fixture.root, "SIGTERM");
  await expect
    .poll(() => alive(fixture.root), { timeout: 60_000, intervals: [500], message: "la instalación termina" })
    .toBe(false);
  await expect
    .poll(
      async () => {
        try {
          return (await page.request.get("/api/bootstrap", { timeout: 1500 })).status();
        } catch {
          return 0;
        }
      },
      { message: "el navegador queda sin servicio", timeout: 30_000, intervals: [500] },
    )
    .toBe(0);
  return host;
}

function sidebar(page: Page) {
  return page.getByRole("navigation", { name: "Navegación principal" });
}

function content(page: Page) {
  return page.locator("#wb-content");
}

function failureOf(page: Page) {
  return content(page).getByRole("alert").filter({ hasText: "No se pudo leer esta vista" });
}

/** The first page the installation really serves (the same limit the screen asks for):
 *  the screens must reflect these rows, not more. */
async function capturedSessions(page: Page) {
  const body = await readJSON<{ sessions?: { id: string; title?: string }[] }>(
    page,
    "/api/conversations?limit=50",
  );
  const rows = body.sessions ?? [];
  if (!rows.length)
    throw new Error(
      "this installation has no captured conversation: capture one source on the fixture before this run (no list is invented here)",
    );
  return rows;
}

async function openView(page: Page, name: string) {
  await content(page).getByRole("navigation").getByRole("button", { name, exact: true }).click();
}

/** The row of one source, as the other cases of this release find it: by its path and by
 *  the controls only that row has. */
function sourceRow(page: Page, path: string) {
  return content(page)
    .locator("div")
    .filter({ hasText: path })
    .filter({ has: page.getByRole("button", { name: "Escanear", exact: true }) })
    .last();
}

/** Congela la instalación de este caso y espera a que deje de contestar de verdad: la
 *  lectura en vuelo de los casos siguientes es real, no una respuesta falsa. */
async function freezeInstallation(page: Page, fixture: ReadStateFixture) {
  process.kill(fixture.root, "SIGSTOP");
  await expect
    .poll(
      async () => {
        try {
          await page.request.get("/api/bootstrap", { timeout: 700 });
          return "answered";
        } catch {
          return "silent";
        }
      },
      { message: "la instalación detenida deja de contestar", timeout: 30_000, intervals: [200] },
    )
    .toBe("silent");
}

function thawInstallation(fixture: ReadStateFixture) {
  if (alive(fixture.root)) process.kill(fixture.root, "SIGCONT");
}

// UI-08: una instalación recién puesta no deja ninguna lectura a medias: cada vacío se
// declara con su siguiente acción real y la lista enseña exactamente lo que sirve el
// servicio, ni una fila de más ni una de menos.
test("una instalación vacía declara sus vacíos y la lista refleja lo que sirve", async ({ page }) => {
  test.setTimeout(120_000);
  requiredReadStateFixture();
  await signIn(page);
  await sidebar(page).getByRole("button", { name: "Tareas", exact: true }).click();
  await expect(content(page).getByText("Leyendo tareas…")).toHaveCount(0);
  // La lectura termina declarando lo que el servicio sirve: filas reales o el vacío con
  // su siguiente acción, nunca una tarjeta en blanco ni una lectura a medias.
  const tasks = await readJSON<{ tasks?: unknown[] }>(page, "/api/tasks?limit=50");
  if (tasks.tasks?.length) {
    await expect(content(page).getByRole("table")).toBeVisible();
  } else {
    await expect(content(page).getByText("Sin tareas")).toBeVisible();
    await expect(content(page).getByRole("button", { name: "Ver conversaciones", exact: true })).toBeVisible();
  }
  await expect(failureOf(page)).toHaveCount(0);

  // Foundry is its own app behind /foundry/: it starts on demand and its first page loads.
  await sidebar(page).getByRole("button", { name: "Foundry", exact: true }).click();
  await expect(page.frameLocator('iframe[title="Foundry"]').getByRole("heading", { name: "Resumen", exact: true })).toBeVisible({ timeout: 30_000 });
  await expect(content(page).getByText("Abriendo Foundry…")).toHaveCount(0);
  await expect(failureOf(page)).toHaveCount(0);

  // La fila que la pantalla publica es la que el servicio sirve.
  const rows = await capturedSessions(page);
  await sidebar(page).getByRole("button", { name: "Conversaciones", exact: true }).click();
  await expect(content(page).locator('[aria-busy="true"]')).toHaveCount(0);
  if (rows.length) await expect(content(page).locator("table tbody tr")).toHaveCount(rows.length);
  else await expect(content(page).getByText("Sin conversaciones")).toBeVisible();
  for (const row of rows)
    await expect(content(page).getByText(row.title ?? row.id, { exact: false })).toBeVisible();
});

// UI-08: la lectura falla de verdad y la pantalla lo declara; el reintento se hace
// contra la instalación ya recuperada y devuelve el dato real, sin inventar ni duplicar.
test("una lectura fallida se declara y el reintento real recupera el dato", async ({ page }) => {
  test.setTimeout(240_000);
  const fixture = requiredReadStateFixture();
  await signIn(page);
  const rows = await capturedSessions(page);
  await sidebar(page).getByRole("button", { name: "Conversaciones", exact: true }).click();
  await expect(content(page).getByText(rows[0].title ?? rows[0].id, { exact: false })).toBeVisible();

  const host = await stopInstallation(page, fixture);
  await content(page).getByRole("button", { name: "Recargar", exact: true }).click();
  // El fallo se declara con el motivo real y una acción; el shell sigue en pie.
  const failure = failureOf(page);
  await expect(failure).toBeVisible();
  await expect(failure).toContainText("No se pudo conectar con el servicio");
  await expect(failure.getByRole("button", { name: "Reintentar", exact: true })).toBeVisible();
  await expect(sidebar(page)).toBeVisible();

  await restartInstallation(page, fixture);
  if (guard && host >= 0) guard.expected.splice(host, 1);
  await failure.getByRole("button", { name: "Reintentar", exact: true }).click();
  await expect(failureOf(page)).toHaveCount(0);
  await expect(content(page).getByText(rows[0].title ?? rows[0].id, { exact: false })).toBeVisible();
  // La lectura repetida no añadió ni perdió filas.
  const after = await capturedSessions(page);
  expect(after.map((row) => row.id)).toEqual(rows.map((row) => row.id));
});

// UI-08: una lectura en vuelo que el operador abandona no publica su resultado en el
// módulo siguiente, y al volver se lee de nuevo de verdad. El retardo es real: la
// instalación se detiene (SIGSTOP) mientras la lectura está pedida.
test("una lectura en vuelo no publica datos en el módulo siguiente", async ({ page }) => {
  test.setTimeout(180_000);
  const fixture = requiredReadStateFixture();
  await signIn(page);
  // Los dos módulos se montan antes de detener la instalación: el shell no descarga
  // ningún trozo mientras está detenida.
  await sidebar(page).getByRole("button", { name: "Tareas", exact: true }).click();
  await expect(content(page).getByText("Leyendo tareas…")).toHaveCount(0);
  await sidebar(page).getByRole("button", { name: "Conversaciones", exact: true }).click();
  const rows = await capturedSessions(page);
  const title = rows[0].title ?? rows[0].id;
  await expect(content(page).getByText(title, { exact: false })).toBeVisible();

  const host = guard?.expected.push(new URL(BASE!).host) ?? -1;
  await freezeInstallation(page, fixture);
  try {
    await page.getByRole("button", { name: "Recargar", exact: true }).click();
    await expect(content(page).getByText("Leyendo conversaciones…")).toBeVisible();
    // El operador se va con la lectura a medias: el otro módulo no recibe nada suyo.
    await sidebar(page).getByRole("button", { name: "Tareas", exact: true }).click();
    await expect(content(page).getByText("Leyendo tareas…")).toBeVisible();
    await expect(content(page).getByText(title, { exact: false })).toHaveCount(0);

    thawInstallation(fixture);
    await expect(content(page).getByText("Leyendo tareas…")).toHaveCount(0, { timeout: 30_000 });
    await expect(content(page).getByText(title, { exact: false })).toHaveCount(0);
    await expect(content(page).locator('[aria-busy="true"]')).toHaveCount(0);
    await expect(failureOf(page)).toHaveCount(0);
    await expect(content(page).getByRole("alert")).toHaveCount(0);
  } finally {
    thawInstallation(fixture);
    if (guard && host >= 0) guard.expected.splice(host, 1);
  }

  // Y al volver, la lectura se repite contra el servicio real.
  await sidebar(page).getByRole("button", { name: "Conversaciones", exact: true }).click();
  await expect(content(page).getByText(title, { exact: false })).toBeVisible();
});

// UI-08: una recarga sobre datos ya leídos también declara que está leyendo. Sin esa
// señal la pantalla vieja se lee como si fuera la actual. La tarea de la cita se guarda
// con el flujo real del producto (sin proveedor: crearla es trabajo local) y su reintento
// reutiliza la misma tarea en lugar de duplicarla.
test("una lista ya leída declara su lectura en vuelo", async ({ page }) => {
  test.setTimeout(240_000);
  const fixture = requiredReadStateFixture();
  const whilePaused = async (body: () => Promise<void>) => {
    await freezeInstallation(page, fixture);
    try {
      await body();
    } finally {
      thawInstallation(fixture);
    }
  };
  await signIn(page);
  const rows = await capturedSessions(page);
  const title = rows[0].title ?? rows[0].id;
  const marker = "UI08 lectura en vuelo";

  // Leer las propuestas de una revisión sin propuesta responde 404 por contrato.
  guard?.expected.push("/api/tasks/proposals/");
  // Una tarea real de la cita, con el mismo rango: repetir el caso no crea otra.
  await sidebar(page).getByRole("button", { name: "Conversaciones", exact: true }).click();
  await content(page)
    .getByRole("row")
    .filter({ hasText: title })
    .getByRole("button", { name: "Abrir", exact: true })
    .click();
  await expect(content(page).getByText("Tareas de esta revisión")).toBeVisible();
  await content(page).getByLabel("Nombre de la tarea").fill(marker);
  await content(page).getByRole("button", { name: "Guardar tarea", exact: true }).click();
  await expect(content(page).getByText(/Tarea guardada\.|La tarea ya existía/)).toBeVisible();
  await content(page).getByRole("button", { name: "Volver a la lista", exact: true }).click();
  await expect(content(page).getByText(title, { exact: false })).toBeVisible();

  const host = guard?.expected.push(new URL(BASE!).host) ?? -1;
  try {
    // Conversaciones ya leída: la recarga se declara mientras el servicio no contesta.
    await whilePaused(async () => {
      await content(page).getByRole("button", { name: "Recargar", exact: true }).click();
      await expect(content(page).getByText("Leyendo conversaciones…")).toBeVisible();
    });
    await expect(content(page).getByText("Leyendo conversaciones…")).toHaveCount(0);
    await expect(content(page).getByText(title, { exact: false })).toBeVisible();

    // Tareas ya leída: igual, con la fila de la tarea todavía en pantalla.
    await sidebar(page).getByRole("button", { name: "Tareas", exact: true }).click();
    await expect(content(page).getByText(marker, { exact: false })).toBeVisible();
    await whilePaused(async () => {
      await content(page).getByRole("button", { name: "Recargar", exact: true }).click();
      await expect(content(page).getByText("Leyendo tareas…")).toBeVisible();
      await expect(content(page).getByText(marker, { exact: false })).toBeVisible();
    });
    await expect(content(page).getByText("Leyendo tareas…")).toHaveCount(0);
    await expect(content(page).getByRole("row").filter({ hasText: marker })).toHaveCount(1);
    await expect(failureOf(page)).toHaveCount(0);
  } finally {
    thawInstallation(fixture);
    if (guard && host >= 0) guard.expected.splice(host, 1);
  }
});

// UI-08: el fallo de un módulo gestionado se declara en su lectura sin derribar el shell
// ni contar como medición, y la recuperación (reanudar ESE proceso) devuelve el dato.
test("un módulo gestionado que no responde no derriba el shell", async ({ page }) => {
  test.setTimeout(120_000);
  const fixture = requiredReadStateFixture();
  await signIn(page, "/?section=settings");
  process.kill(fixture.child, "SIGSTOP");
  try {
    const response = await page.request.get("/api/bootstrap");
    expect(response.status()).toBe(200);
    await sidebar(page).getByRole("button", { name: "Conexiones", exact: true }).click();
    await expect(page.locator("#wb-content h1")).toHaveText("Conexiones");
  } finally {
    if (alive(fixture.child)) process.kill(fixture.child, "SIGCONT");
  }
  await sidebar(page).getByRole("button", { name: "Ajustes", exact: true }).click();
  await expect(page.locator("#wb-content h1")).toHaveText("Ajustes");
});

// UI-08: un host sin Magpie dice por qué en palabras del panel —el contrato contesta un
// código— y volver al host local recupera su panel nativo.
test("un host sin Magpie declara el fallo en palabras del panel y vuelve al local", async ({ page }) => {
  test.setTimeout(120_000);
  requiredReadStateFixture();
  guard?.expected.push("/api/hosts/");
  await signIn(page, "/?section=models&host=ui08-no-existe");
  const alert = content(page).getByRole("alert").filter({ hasText: "No se pudo abrir Magpie" });
  await expect(alert).toBeVisible();
  await expect(alert).toContainText("El Magpie de ese host no está en ejecución.");
  await expect(alert).not.toContainText("magpie_not_running");
  await expect(alert.getByRole("button", { name: "Reintentar", exact: true })).toBeVisible();
  await expect(
    alert.getByRole("button", { name: "Preparar Magpie y revisar SSH", exact: true }),
  ).toBeVisible();

  // El host local vuelve a abrir su panel nativo: la recuperación es real, no un reintento
  // que sólo cambia el texto.
  await content(page).getByLabel("Host de los modelos").selectOption("local");
  await expect(content(page).getByRole("alert")).toHaveCount(0);
  await expect(content(page).locator('iframe[title^="Magpie ·"]')).toBeVisible({ timeout: 30_000 });
});

// UI-08: repetir una lectura no duplica nada, y una escritura que no llegó al servicio no
// deja rastro: el reintento la aplica una sola vez, con una sola fila.
test("repetir la lectura no duplica la mutación y el reintento no duplica la fila", async ({ page }) => {
  test.setTimeout(300_000);
  const fixture = requiredReadStateFixture();
  await signIn(page);
  const mutations: string[] = [];
  page.on("request", (request) => {
    if (request.method() === "GET") return;
    const url = new URL(request.url());
    if (url.host === new URL(BASE!).host) mutations.push(`${request.method()} ${url.pathname}`);
  });

  const sourcesBefore = await readJSON<{ sources: { id: string; path: string; enabled: boolean }[] }>(
    page,
    "/api/conversations/sources",
  );
  if (!sourcesBefore.sources.length)
    throw new Error("this installation has no registered source: register one on the fixture before this run");
  const source = sourcesBefore.sources[0];
  const sessions = await readJSON<{ sessions: { id: string; revision?: string }[] }>(
    page,
    "/api/conversations?limit=50",
  );

  // Lecturas repetidas: recargar la lista, cambiar de vista y volver.
  await sidebar(page).getByRole("button", { name: "Conversaciones", exact: true }).click();
  for (let attempt = 0; attempt < 3; attempt++) {
    await content(page).getByRole("button", { name: "Recargar", exact: true }).click();
    await expect(content(page).locator('[aria-busy="true"]')).toHaveCount(0);
  }
  await openView(page, "Fuentes");
  await expect(content(page).getByText("Carpetas y archivos", { exact: true })).toBeVisible();
  await openView(page, "Conversaciones");
  await expect(content(page).getByText("Conversaciones capturadas", { exact: true })).toBeVisible();
  expect(mutations, "una lectura repetida no escribe").toEqual([]);
  const sourcesAfterReads = await readJSON<{ sources: unknown[] }>(page, "/api/conversations/sources");
  expect(sourcesAfterReads).toEqual(sourcesBefore);
  const sessionsAfterReads = await readJSON<{ sessions: { id: string; revision?: string }[] }>(
    page,
    "/api/conversations?limit=50",
  );
  expect(sessionsAfterReads).toEqual(sessions);

  // Escritura real que no llega: el servicio está parado cuando el operador la pide.
  await openView(page, "Fuentes");
  const row = sourceRow(page, source.path);
  await expect(row).toContainText("activa");
  await stopInstallation(page, fixture);
  await content(page).getByRole("button", { name: "Pausar", exact: true }).click();
  const writeError = content(page).getByRole("alert").filter({ hasText: "No se pudo conectar" });
  await expect(writeError).toBeVisible();
  // Sin escritura optimista: la fuente sigue activa como la reportó el servicio.
  await expect(row).toContainText("activa");

  await restartInstallation(page, fixture);
  const untouched = await readJSON<{ sources: { id: string; path: string; enabled: boolean }[] }>(
    page,
    "/api/conversations/sources",
  );
  expect(untouched.sources.map((row) => row.id), "la escritura fallida no dejó rastro").toEqual(
    sourcesBefore.sources.map((row) => row.id),
  );
  expect(untouched.sources.find((row) => row.id === source.id)?.enabled).toBe(true);

  // El reintento se aplica una vez: una sola fila y el estado que el servicio confirma.
  await openView(page, "Conversaciones");
  await openView(page, "Fuentes");
  const retried = sourceRow(page, source.path);
  await expect(retried).toContainText("activa");
  await content(page).getByRole("button", { name: "Pausar", exact: true }).click();
  await expect(content(page).getByText("Fuente pausada.")).toBeVisible();
  await expect(retried).toContainText("pausada");
  const paused = await readJSON<{ sources: { id: string; path: string; enabled: boolean }[] }>(
    page,
    "/api/conversations/sources",
  );
  expect(paused.sources.filter((row) => row.path === source.path)).toHaveLength(1);
  expect(paused.sources.find((row) => row.id === source.id)?.enabled).toBe(false);

  // La fixture queda como estaba.
  await content(page).getByRole("button", { name: "Activar", exact: true }).click();
  await expect(content(page).getByText("Fuente activada.")).toBeVisible();
  const restored = await readJSON<{ sources: { id: string; enabled: boolean }[] }>(
    page,
    "/api/conversations/sources",
  );
  expect(restored.sources.find((row) => row.id === source.id)?.enabled).toBe(true);
});
