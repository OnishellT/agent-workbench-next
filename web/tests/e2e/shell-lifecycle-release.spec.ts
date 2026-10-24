import { execFileSync } from "node:child_process";
import { readFileSync, readlinkSync, statSync } from "node:fs";
import { expect, test, type Page } from "@playwright/test";
import { BASE, requireRelease, serverSettings, signIn, watchConsole, type ConsoleGuard } from "./helpers";

// M02 PL-11: cierre/reinicio ordenado. La interfaz mantiene un PTY real del motor,
// el servicio se para por su propia señal, el navegador queda offline, ningún hijo
// gestionado (motor y Magpie) sobrevive y al volver no hay replay, sesión duplicada
// ni un "completado" inventado. Sin modelo, sin proveedor y sin inferencia: el
// comando se ejecuta en la interfaz nativa del motor (`!cmd`), que es trabajo local.
//
//   E2E_BASE_URL=http://127.0.0.1:19012 E2E_ACCESS_TOKEN=... E2E_PROJECT_DIR=... \
//   E2E_WORKBENCH_PID=... E2E_MAGPIE_CHILD_PID=... E2E_FIXTURE_DIR=... E2E_RESTART_COMMAND=... \
//     bun run test tests/e2e/shell-lifecycle-release.spec.ts
//
// La fixture es de quien ejecuta el caso: un Root propio (su PID y su cwd verificados
// desde /proc), su hijo Magpie, la carpeta de proyecto y un ejecutable que reinicia esa
// instalación. Sin ellos el caso falla en voz alta; nunca se señaliza un proceso ajeno.

let guard: ConsoleGuard | null = null;

test.beforeAll(requireRelease);

test.beforeEach(({ page }) => {
  guard = watchConsole(page);
});

test.afterEach(() => {
  expect(guard?.errors ?? [], "consola del navegador").toEqual([]);
  guard = null;
});

interface LifecycleFixture {
  root: number;
  child: number;
  project: string;
  restart: string;
}

/** La instalación que este caso puede parar: el pid lo publica la propia fixture y
 *  todo lo demás se verifica desde /proc antes de señalizar nada. */
function requiredLifecycleFixture(): LifecycleFixture {
  const pidFile = process.env.E2E_FIXTURE_PID_FILE;
  const dir = process.env.E2E_FIXTURE_DIR;
  const project = process.env.E2E_PROJECT_DIR;
  const restart = process.env.E2E_RESTART_COMMAND;
  if (!pidFile || !dir || !project || !restart)
    throw new Error(
      "This case stops and restarts the fixture installation: set E2E_FIXTURE_PID_FILE (the pid file of that installation), E2E_FIXTURE_DIR, E2E_PROJECT_DIR and E2E_RESTART_COMMAND (an executable that restarts it). No default, no fallback.",
    );
  for (const [name, value] of [
    ["E2E_FIXTURE_DIR", dir],
    ["E2E_PROJECT_DIR", project],
  ] as const) {
    if (!value.startsWith("/") || value.endsWith("/"))
      throw new Error(`${name} must be an absolute directory without a trailing slash, got "${value}"`);
  }
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
  if (statSync(`/proc/${child}`).uid !== process.getuid?.())
    throw new Error(`managed child ${child} is not owned by this user`);
  if (readlinkSync(`/proc/${child}/exe`).split("/").pop() !== "magpie")
    throw new Error(`process ${child} is not the managed Magpie of installation ${root}`);
  if (!statSync(restart).isFile())
    throw new Error(`E2E_RESTART_COMMAND ${restart} is not a file`);
  return { root, child, project, restart };
}

/** One read of the real API: the boundary where a JSON body becomes a named shape. */
async function readJSON<T>(page: Page, path: string): Promise<T> {
  const response = await page.request.get(path);
  expect(response.status(), path).toBe(200);
  return (await response.json()) as T;
}

function alive(pid: number) {
  try {
    return statSync(`/proc/${pid}`).isDirectory();
  } catch {
    return false;
  }
}

test("reiniciar conserva proyectos y no monta Work ni terminal", async ({ page }) => {
  test.setTimeout(180_000);
  const fixture = requiredLifecycleFixture();
  await signIn(page, "/?section=settings");
  const before = await serverSettings(page);
  await expect(page.request.get("/api/work").then(r => r.status())).resolves.toBe(404);
  const expected = guard?.expected.push(new URL(BASE!).host) ?? -1;
  process.kill(fixture.root, "SIGTERM");
  await expect.poll(() => alive(fixture.root), { timeout: 60_000 }).toBe(false);
  expect(alive(fixture.child), "Magpie no queda huérfano").toBe(false);
  execFileSync(fixture.restart, { stdio: "pipe" });
  await expect.poll(async () => {
    try { return (await page.request.get("/api/bootstrap", { timeout: 1500 })).status(); }
    catch { return 0; }
  }, { timeout: 90_000, intervals: [500] }).toBe(200);
  await page.reload();
  expect(await serverSettings(page)).toEqual(before);
  for (const path of ["/api/work", "/api/terminal"]) {
    expect((await page.request.get(path)).status()).toBe(404);
  }
  if (guard && expected >= 0) guard.expected.splice(expected, 1);
});

test("un hijo Magpie detenido no sobrevive a la parada del servicio", async ({ page }) => {
  test.setTimeout(180_000);
  const fixture = requiredLifecycleFixture();
  await signIn(page);
  interface Diagnostics {
    native_module?: { state?: string; pid?: number; healthy?: boolean };
  }
  const ready = await readJSON<Diagnostics>(page, "/api/diagnostics");
  expect(ready.native_module?.state, "la fixture arranca con su módulo gestionado listo").toBe("ready");
  expect(ready.native_module?.pid).toBe(fixture.child);

  const stopping = guard?.expected.push(new URL(BASE!).host) ?? -1;
  process.kill(fixture.child, "SIGSTOP");
  try {
    await expect
      .poll(async () => (await readJSON<Diagnostics>(page, "/api/diagnostics")).native_module?.state, {
        message: "el hijo detenido deja de responder pero sigue montado",
        timeout: 20_000,
        intervals: [500],
      })
      .toBe("unready");
    const stopped = await readJSON<Diagnostics>(page, "/api/diagnostics");
    expect(stopped.native_module?.healthy, "un módulo que no responde no está sano").toBe(false);

    process.kill(fixture.root, "SIGTERM");
    await expect
      .poll(() => alive(fixture.root), {
        timeout: 60_000,
        intervals: [500],
        message: "el Root termina aunque su hijo no atienda el cierre",
      })
      .toBe(false);
    if (alive(fixture.child)) {
      throw new Error(
        `el hijo gestionado ${fixture.child} sobrevivió a la parada del Root y quedó huérfano`,
      );
    }
  } finally {
    // La fixture de este caso nunca queda detenida: matar lo que quede y reiniciar lo propio.
    if (alive(fixture.child)) process.kill(fixture.child, "SIGKILL");
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
        { message: "la fixture reiniciada vuelve a servir", timeout: 90_000, intervals: [500] },
      )
      .toBe(200);
    if (guard && stopping >= 0) guard.expected.splice(stopping, 1);
  }
});
