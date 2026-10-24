import { execFileSync } from "node:child_process";
import { readFileSync, readlinkSync, statSync } from "node:fs";
import { expect, test, type Locator, type Page } from "@playwright/test";
import type { DiagnosticsDTO } from "../../src/app/api";
import {
  BASE,
  cardByHeading,
  requireRelease,
  serverSettings,
  signIn,
  TOKEN,
  watchConsole,
  type ConsoleGuard,
} from "./helpers";

// M02 PL-09: Ajustes abre el estado real y el registro acotado del módulo gestionado
// (el Magpie nativo de esta instalación). Estar montado no es estar sano: cada lectura
// sondea de verdad y una lectura fallida no deja ningún estado anterior en pantalla.
// Release real: E2E_BASE_URL + E2E_ACCESS_TOKEN, sin mock, sin route.fulfill, sin skip.
//
//   E2E_BASE_URL=http://127.0.0.1:19012 E2E_ACCESS_TOKEN=... \
//     bun run test tests/e2e/shell-diagnostics-release.spec.ts
//
// El caso de fallo/reintento/recuperación necesita la fixture de quien lo ejecuta:
// E2E_MAGPIE_CHILD_PID, E2E_WORKBENCH_PID, E2E_FIXTURE_DIR (cwd de esa instalación) y
// E2E_RESTART_COMMAND (ejecutable que la reinicia). Sin ellos el caso falla en voz alta;
// nunca se detiene un proceso que esta prueba no pueda justificar desde /proc.

let guard: ConsoleGuard | null = null;

test.beforeAll(requireRelease);

test.beforeEach(({ page }) => {
  guard = watchConsole(page);
});

test.afterEach(() => {
  expect(guard?.errors ?? [], "consola del navegador").toEqual([]);
  guard = null;
});

interface CardState {
  state: string;
  pid: string;
  probes: string;
  failures: string;
  events: string[];
}

async function cardState(card: Locator): Promise<CardState> {
  // The report renders at once: waiting for its state first keeps the log read from
  // racing the card's own read of the endpoint.
  await expect(card.locator("[data-managed-state]")).toBeVisible();
  const rows = await card.locator("li[data-event]").all();
  return {
    state: (await card.locator("[data-managed-state]").getAttribute("data-managed-state")) ?? "",
    pid: ((await card.locator("[data-managed-pid]").textContent()) ?? "").trim(),
    probes: ((await card.locator("[data-probes]").textContent()) ?? "").trim(),
    failures: ((await card.locator("[data-failures]").textContent()) ?? "").trim(),
    events: await Promise.all(rows.map(async (row) => (await row.getAttribute("data-event")) ?? "")),
  };
}

/** Authoritative report: every call is one real, authenticated read of the module. */
async function diagnostics(page: Page): Promise<DiagnosticsDTO> {
  const response = await page.request.get("/api/diagnostics");
  expect(response.status(), "GET /api/diagnostics").toBe(200);
  const body = (await response.json()) as DiagnosticsDTO;
  if (!body.native_module)
    throw new Error(
      "this release does not report the managed module: PL-09 needs the real diagnostics endpoint, not a placeholder",
    );
  return body;
}

function expectNoSecret(text: string, where: string) {
  for (const secret of [TOKEN ?? "", "magpie_web_", "native-web.key", "access.token", "MAGPIE_WEB_KEY"]) {
    if (!secret) continue;
    expect(text, `${where} published ${secret}`).not.toContain(secret);
  }
}

test("Ajustes muestra el estado y el registro reales del módulo gestionado", async ({ page }) => {
  await signIn(page);
  await page.goto("/?section=settings");
  const card = cardByHeading(page, "Diagnóstico");
  await expect(card.locator("[data-managed-state]")).toBeVisible();
  const initial = await cardState(card);
  expect(["ready", "unready", "stopped", "unavailable"]).toContain(initial.state);
  if (initial.pid !== "—") expect(Number(initial.pid)).toBeGreaterThan(0);

  // Reintentar relee el mismo endpoint autenticado y pinta exactamente su respuesta.
  const [answered] = await Promise.all([
    page.waitForResponse(
      (result) =>
        result.url().endsWith("/api/diagnostics") && result.request().method() === "GET",
    ),
    card.getByRole("button", { name: "Reintentar" }).click(),
  ]);
  const report = (await answered.json()) as DiagnosticsDTO;
  const managed = report.native_module!;
  const expectedEvents = (managed.events ?? []).map((event) => event.event);
  await expect.poll(async () => (await cardState(card)).events).toEqual(expectedEvents);
  const after = await cardState(card);
  expect(after.state).toBe(managed.state);
  expect(after.pid).toBe(managed.pid === undefined ? "—" : String(managed.pid));
  expect(after.probes).toBe(typeof managed.probes === "number" ? String(managed.probes) : "—");
  expect(after.failures).toBe(
    typeof managed.probe_failures === "number" ? String(managed.probe_failures) : "—",
  );
  expect(after.events).toEqual(expectedEvents);
  expect(after.events.length).toBeLessThanOrEqual(64);

  // Los módulos montados y las carencias salen del mismo informe, no de una suposición local.
  const mounted = Object.entries(report.modules ?? {})
    .filter(([, state]) => state === "mounted")
    .map(([name]) => name)
    .join(",");
  expect(await card.locator("[data-diagnostics-modules]").getAttribute("data-diagnostics-modules")).toBe(
    mounted,
  );
  const text = (await card.textContent()) ?? "";
  for (const gap of report.routes_not_ported ?? []) expect(text).toContain(gap);

  // Una lectura más es exactamente una sonda más: el contador no es decorativo.
  const extra = await diagnostics(page);
  expect(extra.native_module?.probes).toBe((managed.probes ?? 0) + 1);
  expect(extra.native_module?.state).toBe(managed.state);
  expectNoSecret(text, "la tarjeta de diagnóstico");
});

test("el diagnóstico no publica tokens, claves privadas ni volcados", async ({ page }) => {
  await signIn(page);
  const response = await page.request.get("/api/diagnostics");
  expect(response.status()).toBe(200);
  const body = await response.text();
  const report = JSON.parse(body) as DiagnosticsDTO;
  expect(report.native_module).toBeTruthy();
  expectNoSecret(body, "GET /api/diagnostics");
  for (const dump of ['"env"', '"stdout"', '"stderr"', '"headers"', "Cookie:", "HTTP_COOKIE"]) {
    expect(body, `diagnostics published ${dump}`).not.toContain(dump);
  }

  await page.goto("/?section=settings");
  const card = cardByHeading(page, "Diagnóstico");
  await expect(card.locator("[data-managed-state]")).toBeVisible();
  expectNoSecret((await card.textContent()) ?? "", "la tarjeta de diagnóstico");
});

interface FailureFixture {
  pid: number;
  restart: string;
}

/** La única fixture que este caso puede detener, verificada desde /proc. */
function requiredFailureFixture(): FailureFixture {
  const pid = Number(process.env.E2E_MAGPIE_CHILD_PID);
  const owner = Number(process.env.E2E_WORKBENCH_PID);
  const dir = process.env.E2E_FIXTURE_DIR;
  const restart = process.env.E2E_RESTART_COMMAND;
  if (!pid || !owner || !dir || !restart)
    throw new Error(
      "This case stops and restarts the fixture installation: set E2E_MAGPIE_CHILD_PID, E2E_WORKBENCH_PID, E2E_FIXTURE_DIR and E2E_RESTART_COMMAND (an executable that restarts the installation under test). No default, no fallback.",
    );
  if (!dir.startsWith("/") || dir.endsWith("/"))
    throw new Error(`E2E_FIXTURE_DIR must be an absolute directory without a trailing slash, got "${dir}"`);
  const ownerUid = process.getuid?.();
  if (statSync(`/proc/${pid}`).uid !== ownerUid)
    throw new Error(`managed child ${pid} is not owned by this user`);
  if (readlinkSync(`/proc/${pid}/cwd`) !== dir)
    throw new Error(`managed child ${pid} does not run in the fixture directory ${dir}`);
  if (readlinkSync(`/proc/${owner}/cwd`) !== dir)
    throw new Error(`installation ${owner} does not run in the fixture directory ${dir}`);
  const children = readFileSync(`/proc/${owner}/task/${owner}/children`, "utf8").trim().split(/\s+/);
  if (!children.includes(String(pid)))
    throw new Error(`process ${pid} is not a managed child of installation ${owner}`);
  if (!statSync(restart).isFile())
    throw new Error(`E2E_RESTART_COMMAND ${restart} is not a file`);
  return { pid, restart };
}

test("un módulo que no responde no queda sano y vuelve al responder", async ({ page }) => {
  test.setTimeout(120_000);
  const fixture = requiredFailureFixture();
  await signIn(page);
  const before = await diagnostics(page);
  expect(before.native_module?.state, "the fixture must start with its managed module ready").toBe(
    "ready",
  );
  expect(before.modules?.models, "el módulo debe estar montado para probar montado≠sano").toBe(
    "mounted",
  );

  // El hijo se detiene sin matarlo: sigue montado y deja de responder. Las lecturas
  // del navegador durante esa ventana las provoca este caso a propósito.
  const paused = guard?.expected.push(new URL(BASE!).host) ?? -1;
  process.kill(fixture.pid, "SIGSTOP");
  const card = cardByHeading(page, "Diagnóstico");
  try {
    await page.goto("/?section=settings");
    await expect(card.locator("[data-managed-state]")).toHaveAttribute(
      "data-managed-state",
      "unready",
    );
    await expect(card.getByText("Sin responder", { exact: true })).toBeVisible();
    const silent = await cardState(card);
    expect(silent.events.at(-1)).toBe("probe_failed");
    const silentReport = await diagnostics(page);
    expect(silentReport.modules?.models).toBe("mounted");
    expect(silentReport.native_module?.healthy).toBe(false);

    // Reintentar vuelve a sondear de verdad: el contador crece y sigue sin responder.
    // La base es la última lectura autoritativa, no lo que la tarjeta pintó antes.
    const probesBefore = silentReport.native_module?.probes ?? 0;
    const [answered] = await Promise.all([
      page.waitForResponse(
        (result) =>
          result.url().endsWith("/api/diagnostics") && result.request().method() === "GET",
      ),
      card.getByRole("button", { name: "Reintentar" }).click(),
    ]);
    const retry = (await answered.json()) as DiagnosticsDTO;
    expect(retry.native_module?.state).toBe("unready");
    expect(retry.native_module?.healthy).toBe(false);
    expect(retry.native_module?.probes).toBe(probesBefore + 1);
    await expect(card.getByRole("button", { name: "Reintentar" })).toBeEnabled();
  } finally {
    // La fixture nunca queda detenida, ni siquiera si un assert falla antes.
    process.kill(fixture.pid, "SIGCONT");
  }

  // Al volver a responder, la misma lectura se recupera sin reiniciar nada y el
  // registro conserva el fallo observado.
  await expect
    .poll(async () => (await diagnostics(page)).native_module?.state, {
      message: "el módulo debe volver a responder",
      timeout: 30_000,
      intervals: [500],
    })
    .toBe("ready");
  if (guard && paused >= 0) guard.expected.splice(paused, 1);
  await expect(card.getByRole("button", { name: "Reintentar" })).toBeEnabled();
  await card.getByRole("button", { name: "Reintentar" }).click();
  await expect(card.locator("[data-managed-state]")).toHaveAttribute("data-managed-state", "ready");
  const recovered = await cardState(card);
  expect(recovered.events).toContain("probe_failed");
  expect(recovered.events.at(-1)).toBe("probe_ok");
  expectNoSecret((await card.textContent()) ?? "", "la tarjeta tras recuperar la respuesta");
});

test("un módulo detenido no queda sano: fallo, reintento y recuperación", async ({ page }) => {
  test.setTimeout(180_000);
  const fixture = requiredFailureFixture();
  await signIn(page);
  const before = await diagnostics(page);
  expect(before.native_module?.state, "the fixture must start with its managed module ready").toBe(
    "ready",
  );
  expect(before.native_module?.pid).toBe(fixture.pid);
  const projects = (await serverSettings(page)).projects ?? [];

  // El único proceso que se detiene es el hijo verificado de esta instalación. Su
  // caída hace que las lecturas del Magpie nativo (Trabajo/Modelos) respondan 502:
  // es la consecuencia esperada de este caso, no un fallo que se tape.
  guard?.expected.push("/api/models/");
  process.kill(fixture.pid, "SIGKILL");
  await expect
    .poll(async () => (await diagnostics(page)).native_module?.state, {
      message: "el módulo detenido debe reportarse como detenido",
      timeout: 20_000,
      intervals: [500],
    })
    .toBe("stopped");

  await page.goto("/?section=settings");
  const card = cardByHeading(page, "Diagnóstico");
  await expect(card.locator("[data-managed-state]")).toHaveAttribute("data-managed-state", "stopped");
  await expect(card.getByText("Detenido", { exact: true })).toBeVisible();
  await expect(card.locator("[data-managed-detail]")).toContainText("signal: killed");
  const failed = await cardState(card);
  // La salida real queda registrada, con su estado, y nada la vuelve a poner "listo".
  expect(failed.events.filter((event) => event === "exit")).toHaveLength(1);
  expect(failed.events.filter((event) => event === "ready")).toHaveLength(1);
  expect(failed.events.at(-1)).not.toBe("ready");

  // Reintentar relee: un hijo muerto no se sondea, no se inventa sonda y no vuelve a "listo".
  const [answered] = await Promise.all([
    page.waitForResponse(
      (result) =>
        result.url().endsWith("/api/diagnostics") && result.request().method() === "GET",
    ),
    card.getByRole("button", { name: "Reintentar" }).click(),
  ]);
  const retry = (await answered.json()) as DiagnosticsDTO;
  expect(retry.native_module?.healthy).toBe(false);
  expect(retry.native_module?.state).toBe("stopped");
  // La relectura terminó cuando el control vuelve a su estado de reposo.
  await expect(card.getByRole("button", { name: "Reintentar" })).toBeEnabled();
  const after = await cardState(card);
  expect(after.state).toBe("stopped");
  expect(after.events).toEqual(failed.events);
  expect(after.probes).toBe(failed.probes);
  expect(after.failures).toBe(failed.failures);
  expectNoSecret((await card.textContent()) ?? "", "la tarjeta tras el fallo");

  // La recuperación es un reinicio de la instalación, no un botón del navegador.
  // Mientras está parada por diseño, las peticiones del propio navegador fallan:
  // ese origen queda excusado sólo durante la ventana de reinicio y se retira al volver.
  const restarting = guard?.expected.push(new URL(BASE!).host) ?? -1;
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
  if (guard && restarting >= 0) guard.expected.splice(restarting, 1);
  await page.reload();
  const recovered = await diagnostics(page);
  expect(recovered.native_module?.state).toBe("ready");
  expect(recovered.native_module?.pid).not.toBe(fixture.pid);
  expect((await serverSettings(page)).projects).toEqual(projects);
  const restored = await cardState(cardByHeading(page, "Diagnóstico"));
  expect(restored.state).toBe("ready");
  expect(restored.pid).toBe(String(recovered.native_module?.pid));
  expect(restored.events[0]).toBe("start");
  expect(restored.events.filter((event) => event === "start")).toHaveLength(1);
  expect(restored.events.filter((event) => event === "exit")).toHaveLength(0);
  expectNoSecret((await card.textContent()) ?? "", "la tarjeta tras la recuperación");
});
