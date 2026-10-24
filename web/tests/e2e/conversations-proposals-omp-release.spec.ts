import { expect, test, type Page } from "@playwright/test";
import { requireRelease, signIn, watchConsole, type ConsoleGuard } from "./helpers";

// The proposal analysis bridge to this host's own OMP (kind `omp`): the screen stores
// the engine, the server normalizes it to the engine it really resolves, reload keeps
// it, and the diagnostics never present a configuration this host cannot run as
// available. No inference and no account are needed: the negative is the contract.
//
//   E2E_BASE_URL=http://127.0.0.1:18917 E2E_ACCESS_TOKEN=... \
//     bun run test tests/e2e/conversations-proposals-omp-release.spec.ts

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

function route(base: string, view?: string) {
  const url = new URL(base, "http://workbench.invalid");
  if (view) url.searchParams.set("view", view);
  return url.pathname + url.search;
}

interface Provider {
  kind: string;
  command: string[];
  model: string;
  timeout: number;
  real_inference: boolean;
  provider?: string;
  reasoning?: string;
}

interface Config {
  config?: { provider: Provider; replay: Provider };
  diagnostics?: {
    provider: { kind: string; model: string; available: boolean; reason?: string };
    replay: { available: boolean; reason?: string };
    ready: boolean;
    reason?: string;
  };
}

async function tasksConfig(page: Page) {
  const response = await page.request.get("/api/tasks/config");
  expect(response.status(), "GET /api/tasks/config").toBe(200);
  return (await response.json()) as Config;
}

async function patchProvider(page: Page, provider: Provider) {
  const response = await page.request.patch("/api/tasks/config", { data: { config: { provider } } });
  expect(response.status(), `PATCH provider ${provider.kind}`).toBe(200);
}

test("MP-01: el motor OMP se guarda por UI, sobrevive al reload y su diagnóstico no miente", async ({
  page,
}) => {
  requireRelease();
  guard = watchConsole(page);
  await signIn(page);

  const found = await tasksConfig(page);
  expect(found.config?.provider, "la instalación publica su proveedor de análisis").toBeTruthy();
  const original = found.config?.provider as Provider;

  // Deterministic pre-state: this case needs a starting state its own UI save really
  // changes, so the shipped default engine is planted through the same route. What
  // this instance had is restored at the end.
  await patchProvider(page, {
    kind: "pi",
    command: ["pi"],
    model: "",
    timeout: original.timeout,
    real_inference: false,
    reasoning: "medium",
  });

  await page.goto(route(TASKS_ROUTE, "proposals"));
  await expect(page.getByText("Análisis de tareas", { exact: true })).toBeVisible();
  await expect(page.locator("#cfg-kind")).toHaveValue("pi");
  // An engine without a model override really has none: that is said, not hidden.
  await expect(page.getByText("sin modelo", { exact: false }).first()).toBeVisible();

  // Selecting OMP realigns the fields that belong to one engine: the screen shows the
  // engine this installation resolves and stops claiming an inherited model/thinking.
  await page.locator("#cfg-kind").fill("omp");
  await expect(page.locator("#cfg-command")).toHaveValue("omp");
  await expect(page.locator("#cfg-reasoning")).toHaveValue("");
  await expect(
    page.getByText("omp · modelo nativo de Magpie", { exact: false }).first(),
    "OMP sin override ejecuta el modelo nativo de la Magpie de este host",
  ).toBeVisible();

  await page.locator("#cfg-model").fill("");
  await page.getByRole("button", { name: "Guardar cambios", exact: true }).click();
  await expect(page.getByText("Configuración confirmada por la instalación.")).toBeVisible();

  const saved = await tasksConfig(page);
  expect(saved.config?.provider.kind, "el motor guardado es OMP").toBe("omp");
  expect(saved.config?.provider.command, "el ejecutable es el del motor, no otro").toEqual(["omp"]);
  expect(saved.config?.provider.model, "sin override no se escribe un modelo").toBe("");
  expect(saved.config?.provider.reasoning ?? "", "sin override el razonamiento es nativo").toBe("");
  // An installed engine is not yet a route: on this instance (no account, no wired
  // Magpie) the row must not claim it can run, and the reason is the host's own.
  const routeReason = saved.diagnostics?.provider.reason ?? "";
  expect(
    saved.diagnostics?.provider.available,
    "sin la Magpie del host el motor no puede presentarse disponible",
  ).toBe(false);
  expect(routeReason, "el motivo es el de la instalación, no uno inventado").toContain("Magpie");
  expect(
    JSON.stringify(saved.config?.provider),
    "el contrato no publica credenciales",
  ).not.toMatch(/auth|token|key/i);

  // Reload: the stored engine is what the screen shows again, with its real label.
  await page.reload();
  await expect(page.locator("#cfg-kind")).toHaveValue("omp");
  await expect(page.locator("#cfg-command")).toHaveValue("omp");
  await expect(page.locator("#cfg-model")).toHaveValue("");
  await expect(
    page.getByText("omp · modelo nativo de Magpie", { exact: false }).first(),
  ).toBeVisible();

  // Negative without an account: a model that is not this host's Magpie route is never
  // presented as available, and the reason is the installation's own.
  await page.locator("#cfg-model").fill("gpt-6-luna");
  await page.getByRole("button", { name: "Guardar cambios", exact: true }).click();
  await expect(page.getByText("Configuración confirmada por la instalación.")).toBeVisible();
  const unscoped = await tasksConfig(page);
  expect(unscoped.config?.provider.model).toBe("gpt-6-luna");
  const reason = unscoped.diagnostics?.provider.reason ?? "";
  expect(
    unscoped.diagnostics?.provider.available,
    "un modelo fuera de la Magpie de este host no puede estar disponible",
  ).toBe(false);
  expect(reason, "el motivo es el de la instalación, no uno inventado").toContain("Magpie");
  await expect(page.getByText("no disponible", { exact: true }).first()).toBeVisible();
  await expect(page.getByText("Análisis no disponible:", { exact: false })).toBeVisible();
  await expect(page.getByText(reason, { exact: false }).first()).toBeVisible();

  // The instance keeps saying the same after a reload: no vanished reason.
  await page.reload();
  await expect(page.locator("#cfg-model")).toHaveValue("gpt-6-luna");
  await expect(page.getByText("Análisis no disponible:", { exact: false })).toBeVisible();

  await patchProvider(page, original);
  const final = await tasksConfig(page);
  expect(final.config?.provider.kind).toBe(original.kind);
  expect(final.config?.provider.model).toBe(original.model);
  expect(final.config?.provider.command).toEqual(original.command);
});
