import { randomUUID } from "node:crypto";
import { readFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { expect, test, type Page } from "@playwright/test";
import { sections } from "../../src/app/navigation";
import { simEnglish, translate } from "../../src/modules/sim/i18n";
import { BASE, requireRelease, signIn, watchConsole, type ConsoleGuard } from "./helpers";

// SIM es la interfaz ORIGINAL que sirve esta instalación, enmarcada tal cual: la sección
// no dibuja panel propio, no habla con su API y no inventa un origen. Release real:
// E2E_BASE_URL + E2E_ACCESS_TOKEN, sin mock, sin route.fulfill, sin skip. La misma
// batería sirve para las dos instalaciones que hay que comprobar: una con sim_url
// publicada (positivo con el SIM real) y otra sin ella (negativo honesto).
//
//   E2E_BASE_URL=http://127.0.0.1:18996 E2E_ACCESS_TOKEN=... \
//     bun run test tests/e2e/sim-release.spec.ts
//
// El recorrido V3 (crear/editar/desplegar/ejecutar/resultado por la API oficial
// del SIM desde dos clientes distintos) necesita además dos API keys del SIM con
// acceso al mismo workspace, una por cliente:
//
//   E2E_SIM_API_KEY_A=... E2E_SIM_API_KEY_B=...
//
// Las crea quien opere el SIM (Settings > API Keys); el spec no inventa
// credenciales: sin ellas el caso V3 falla en voz alta en vez de fingir cobertura.

let guard: ConsoleGuard | null = null;

test.beforeAll(requireRelease);

test.beforeEach(({ page }) => {
  guard = watchConsole(page);
  // El Magpie de la instancia local puede no estar levantado: su negativa es respuesta
  // esperada de la regresión de Modelos, no ruido de este archivo.
  guard.expected.push("/api/hosts/");
  guard.expected.push("/magpie/open");
});

test.afterEach(() => {
  expect(guard?.errors ?? [], "consola del navegador").toEqual([]);
});

/** Authoritative value the shell must reflect, straight from bootstrap. */
async function simURL(page: Page) {
  const response = await page.request.get("/api/bootstrap");
  expect(response.status(), "bootstrap de la instalación").toBe(200);
  const body: unknown = await response.json();
  if (!body || typeof body !== "object" || !("sim_url" in body)) return "";
  const value = body.sim_url;
  return typeof value === "string" ? value : "";
}

test("las cadenas del marco SIM tienen inglés y la sección no inventa pestañas", () => {
  const missing: string[] = [];
  for (const text of [
    "Panel original del SIM",
    "Recargar",
    "Abrir en pestaña",
    "Abriendo el SIM…",
    "El SIM no está configurado",
  ])
    if (translate(text, "en") === text) missing.push(text);
  expect(missing, "cadenas sin entrada en inglés").toEqual([]);
  expect(simEnglish["Recargar"]).toBe("Reload");
  expect(translate("Orbit", "en")).toBe("Orbit");
  const sim = sections.find((section) => section.id === "sim");
  expect(sim?.label).toBe("SIM");
  expect(sim?.views).toEqual([]);
});

test("la barra y Ajustes ya no ofrecen Orbit y la paleta sólo ofrece SIM", async ({ page }) => {
  await signIn(page);
  const nav = page.getByRole("navigation", { name: "Navegación principal" });
  const sim = nav.getByRole("button", { name: "SIM", exact: true });
  await expect(sim).toBeVisible();
  await expect(nav.getByRole("button", { name: "Orbit", exact: true })).toHaveCount(0);

  await page.goto("/?section=settings");
  await expect(
    page.getByRole("heading", { name: "Proyectos", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "Ejecución de Orbit", exact: true }),
  ).toHaveCount(0);
  await expect(page.getByLabel("Imagen de Orbit")).toHaveCount(0);

  await page.getByRole("button", { name: /Ir a…/ }).click();
  const palette = page.getByRole("dialog");
  await palette.getByRole("combobox").fill("sim");
  await expect(palette.getByRole("option", { name: /SIM/ }).first()).toBeVisible();
  await palette.getByRole("combobox").fill("orbit");
  await expect(palette.getByText("No hay coincidencias con ese nombre.")).toBeVisible();
  await page.keyboard.press("Escape");
});

test("el marcador antiguo ?section=orbit abre SIM sin segundo router", async ({ page }) => {
  await signIn(page, "/?section=orbit");
  const heading = page.getByRole("heading", { level: 1, name: "SIM", exact: true });
  await expect(heading).toBeVisible();
  expect(new URL(page.url()).searchParams.get("section"), "la URL no se reescribe").toBe(
    "orbit",
  );
  await page.reload();
  await expect(heading).toBeVisible();

  // Ida y vuelta con el historial: la sección sigue siendo la misma pantalla.
  await page.getByRole("navigation", { name: "Navegación principal" })
    .getByRole("button", { name: "Conexiones", exact: true })
    .click();
  await expect(page.getByRole("heading", { level: 1, name: "Conexiones", exact: true })).toBeVisible();
  await page.goBack();
  await expect(heading).toBeVisible();
  await page.goForward();
  await expect(page.getByRole("heading", { level: 1, name: "Conexiones", exact: true })).toBeVisible();
});

test("el panel SIM refleja exactamente sim_url o dice que falta", async ({ page }) => {
  await signIn(page);
  const url = await simURL(page);
  const frame = page.locator('iframe[title^="SIM"]');
  await page.goto("/?section=sim");

  if (!url) {
    // Instalación sin --sim-url: la pantalla lo dice y no finge un panel.
    await expect(
      page.getByRole("heading", { name: "El SIM no está configurado", exact: true }),
    ).toBeVisible();
    await expect(page.getByRole("status")).toContainText("WORKBENCH_SIM_URL");
    await expect(frame).toHaveCount(0);
    await expect(page.getByRole("link", { name: "Abrir en pestaña", exact: true })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Recargar", exact: true })).toHaveCount(0);
    return;
  }

  const origin = new URL(url).origin;
  // El SIM original es otra aplicación: su propia consola no es el contrato de este archivo.
  guard?.expected.push(origin);
  await expect(frame).toHaveAttribute("src", url);
  await expect(
    page.getByRole("link", { name: "Abrir en pestaña", exact: true }),
  ).toHaveAttribute("href", url);
  await expect(page.getByText(`${origin}`, { exact: false })).toBeVisible();

  // Positivo real: el marco navegó al origen publicado y muestra contenido de verdad.
  const inner = page.frameLocator('iframe[title^="SIM"]');
  await expect(inner.locator("body")).toContainText(/\S/, { timeout: 30_000 });
  const loaded = page.frames().find((candidate) => candidate.url().startsWith(origin));
  expect(loaded, `ningún marco en ${origin}`).toBeTruthy();
  expect((await loaded!.locator("body").innerText()).trim().length).toBeGreaterThan(0);

  // Recargar vuelve a pedir el SIM original: una navegación nueva del marco, no una copia.
  let navigations = 0;
  page.on("framenavigated", (navigated) => {
    if (navigated.url().startsWith(origin)) navigations += 1;
  });
  await page.getByRole("button", { name: "Recargar", exact: true }).click();
  await expect.poll(() => navigations, { timeout: 30_000 }).toBeGreaterThan(0);
  await expect(inner.locator("body")).toContainText(/\S/, { timeout: 30_000 });
  await page.reload();
  await expect(frame).toHaveAttribute("src", url);
  await expect(inner.locator("body")).toContainText(/\S/, { timeout: 30_000 });
});

test("Abrir en pestaña lleva el SIM original fuera del marco, sin opener", async ({ page }) => {
  await signIn(page);
  const url = await simURL(page);
  test.skip(url === "", "esta instalación no publica sim_url: el caso positivo necesita el SIM real");
  guard?.expected.push(new URL(url).origin);
  await page.goto("/?section=sim");
  const link = page.getByRole("link", { name: "Abrir en pestaña", exact: true });
  await expect(link).toHaveAttribute("target", "_blank");
  await expect(link).toHaveAttribute("rel", /noopener/);
  const [popup] = await Promise.all([page.waitForEvent("popup"), link.click()]);
  const target = new URL(url).origin;
  await popup.waitForURL((candidate) => candidate.origin === target, { timeout: 30_000 });
  expect(await popup.evaluate(() => window.opener), "la pestaña nueva no hereda el opener").toBeNull();
  expect((await popup.locator("body").innerText()).trim().length).toBeGreaterThan(0);
  await popup.close();
});

test("sin sesión no hay panel SIM: 401 y formulario de token", async ({ browser }) => {
  const context = await browser.newContext();
  const page = await context.newPage();
  try {
    await page.goto("/?section=sim");
    await expect(page.locator("#access-token")).toBeVisible();
    await expect(page.locator("iframe")).toHaveCount(0);
    const anonymous = await page.request.get("/api/bootstrap");
    expect(anonymous.status(), "bootstrap sin sesión").toBe(401);
    expect(anonymous.headers()["content-type"] ?? "").toContain("application/json");
  } finally {
    await context.close();
  }
});

test("V3: dos clientes crean/editan/despliegan/ejecutan y leen el resultado por la API oficial", async ({
  page,
  playwright,
  browser,
}) => {
  test.setTimeout(300_000);
  await signIn(page);
  const sim = await simURL(page);
  if (!sim)
    throw new Error(
      "V3 necesita una instalación con sim_url publicada (WORKBENCH_SIM_URL/--sim-url): sin el SIM real no hay recorrido entre clientes distintos.",
    );
  const keyA = process.env.E2E_SIM_API_KEY_A;
  const keyB = process.env.E2E_SIM_API_KEY_B;
  if (!keyA || !keyB)
    throw new Error(
      "V3 necesita E2E_SIM_API_KEY_A y E2E_SIM_API_KEY_B: dos API keys del SIM con acceso al mismo workspace. Las crea quien opere el SIM; sin ellas no hay recorrido entre clientes distintos.",
    );
  const credentialsFile = process.env.E2E_SIM_CREDENTIALS_FILE;
  const restart = process.env.E2E_SIM_RESTART_COMMAND;
  if (!credentialsFile || !restart)
    throw new Error("V3 completo necesita E2E_SIM_CREDENTIALS_FILE de una cuenta de ensayo y E2E_SIM_RESTART_COMMAND para su propio servidor Sim.");
  const credentials = JSON.parse(readFileSync(credentialsFile, "utf8")) as { email: string; password: string };
  const nativeContext = await browser.newContext();
  const nativePage = await nativeContext.newPage();
  const origin = new URL(sim).origin;
  const clientA = await playwright.request.newContext({
    baseURL: origin,
    extraHTTPHeaders: { "x-api-key": keyA },
  });
  const clientB = await playwright.request.newContext({
    baseURL: origin,
    extraHTTPHeaders: { "x-api-key": keyB },
  });
  const marker = `wb-sim-v3-${Date.now()}`;
  let workflowId = "";
  try {
    // Sin credencial no hay lectura: la API oficial responde 401.
    const anon = await playwright.request.newContext({ baseURL: origin });
    try {
      const denied = await anon.get("/api/v2/meta");
      expect(denied.status(), "v2 sin credencial").toBe(401);
    } finally {
      await anon.dispose();
    }

    // Cliente A: resuelve un workspace y crea el workflow.
    const ws = await clientA.get("/api/v2/workspaces?limit=10");
    expect(ws.status(), "listar workspaces").toBe(200);
    const listed = (await ws.json()) as { data?: Array<{ id: string }> };
    expect(listed.data?.length ?? 0, "el SIM necesita un workspace").toBeGreaterThan(0);
    const workspaceId = listed.data![0].id;

    const created = await clientA.post("/api/v2/workflows", {
      data: { workspaceId, name: marker },
    });
    expect(created.status(), "crear workflow").toBe(201);
    workflowId = ((await created.json()) as { data: { id: string } }).data.id;
    expect(workflowId, "id del workflow creado").toBeTruthy();

    // Cliente A: edita metadatos y el grafo (starter -> function determinista).
    const renamed = await clientA.patch(`/api/v2/workflows/${workflowId}`, {
      data: { description: `Recorrido V3 de Workbench (${marker})` },
    });
    expect(renamed.status(), "editar metadatos").toBe(200);

    const startID = randomUUID();
    const functionID = randomUUID();
    const graph = {
      blocks: {
        [startID]: {
          id: startID,
          type: "starter",
          name: "Start",
          position: { x: 0, y: 0 },
          subBlocks: {},
          outputs: {},
          enabled: true,
        },
        [functionID]: {
          id: functionID,
          type: "function",
          name: "Marker",
          position: { x: 300, y: 0 },
          subBlocks: {
            code: {
              id: "code",
              type: "code",
              value: `return { marker: ${JSON.stringify(marker)} };`,
            },
          },
          outputs: {},
          enabled: true,
        },
      },
      edges: [
        { id: randomUUID(), source: startID, target: functionID, sourceHandle: "source", targetHandle: null },
      ],
      loops: {},
      parallels: {},
      variables: {},
    };
    const edited = await clientA.put(`/api/v2/workflows/${workflowId}/state`, { data: graph });
    expect(edited.status(), "editar grafo: "+await edited.text()).toBe(200);

    // La edicion persiste al releer: el mismo grafo que la UI original muestra.
    const reread = await clientA.get(`/api/v2/workflows/${workflowId}/state`);
    expect(reread.status(), "releer grafo").toBe(200);
    const state = (await reread.json()) as { data: { blocks: Record<string, { name: string }> } };
    expect(Object.keys(state.data.blocks).sort(), "el grafo conserva la edicion").toEqual([functionID, startID].sort());

    // Cliente A: despliega y espera la activacion (puede ser asincrona).
    const deployed = await clientA.post(`/api/v2/workflows/${workflowId}/deploy`, { data: {} });
    expect([200, 202], "desplegar aceptado").toContain(deployed.status());
    await expect
      .poll(
        async () => {
          const status = await clientA.get(`/api/v2/workflows/${workflowId}`);
          if (status.status() !== 200) return false;
          const body = (await status.json()) as { data: { isDeployed: boolean } };
          return body.data.isDeployed === true;
        },
        { timeout: 120_000 },
      )
      .toBe(true);

    // El workflow creado aparece en el listado que alimenta la UI original.
    const list = await clientA.get(
      `/api/v2/workflows?workspaceId=${workspaceId}&search=${encodeURIComponent(marker)}`,
    );
    expect(list.status(), "listar workflows").toBe(200);
    const items = (await list.json()) as { data: Array<{ id: string }> };
    expect(
      items.data.map((item) => item.id),
      "el workflow es visible en el listado",
    ).toContain(workflowId);

    // Cliente B (distinto): ejecuta el despliegue y lee el resultado.
    const run = await clientB.post(`/api/v2/workflows/${workflowId}/execute`, { data: {} });
    expect(run.status(), "ejecucion sincrona").toBe(200);
    const result = (await run.json()) as {
      data: { runId: string; status: string; output: unknown; error: unknown };
    };
    expect(result.data.status, "ejecucion completada").toBe("completed");
    expect(result.data.error, "sin error en la ejecucion").toBeNull();
    expect(JSON.stringify(result.data.output ?? {}), "la salida lleva el marcador").toContain(
      marker,
    );

    const runId = result.data.runId;
    await expect
      .poll(
        async () => {
          const poll = await clientB.get(
            `/api/v2/workflows/${workflowId}/runs/${runId}?includeOutput=true`,
          );
          if (poll.status() !== 200) return "";
          return JSON.stringify(await poll.json());
        },
        { timeout: 60_000 },
      )
      .toContain(marker);

    // Error real con recuperacion: un selector desconocido es 400; reintentar limpio completa.
    const bad = await clientB.post(`/api/v2/workflows/${workflowId}/execute`, {
      data: { selectedOutputs: ["noexiste.result"] },
    });
    expect(bad.status(), "selector desconocido").toBe(400);
    const retry = await clientB.post(`/api/v2/workflows/${workflowId}/execute`, { data: {} });
    expect(retry.status(), "reintento tras el error").toBe(200);
    expect(((await retry.json()) as { data: { status: string } }).data.status).toBe("completed");

    // Ver la edición en el canvas original, con la sesión propia de Sim.
    const login = await nativePage.request.post(`${origin}/api/auth/sign-in/email`, {
      headers: { Origin: origin }, data: credentials,
    });
    expect(login.status(), "sesión nativa de Sim").toBe(200);
    await nativePage.goto(`${origin}/workspace/${workspaceId}/w/${workflowId}`);
    const markerNode = nativePage.locator(".react-flow__node").filter({ hasText: "Marker" });
    await expect(markerNode).toBeVisible({ timeout: 30_000 });
    await nativePage.reload();
    await expect(markerNode).toBeVisible({ timeout: 30_000 });

    // Reiniciar exclusivamente el servidor de ensayo; el mismo despliegue y run persisten.
    execFileSync(restart, { stdio: "pipe", timeout: 90_000 });
    await expect.poll(async () => {
      try { return (await clientB.get(`/api/v2/workflows/${workflowId}`)).status(); }
      catch { return 0; }
    }, { timeout: 90_000 }).toBe(200);
    const persisted = await clientB.get(`/api/v2/workflows/${workflowId}/state`);
    expect(persisted.status()).toBe(200);
    expect(JSON.stringify(await persisted.json())).toContain(marker);
    const oldRun = await clientB.get(`/api/v2/workflows/${workflowId}/runs/${runId}?includeOutput=true`);
    expect(oldRun.status()).toBe(200);
    expect(JSON.stringify(await oldRun.json())).toContain(marker);
    const again = await clientB.post(`/api/v2/workflows/${workflowId}/execute`, { data: {} });
    expect(again.status()).toBe(200);
    expect(JSON.stringify(await again.json())).toContain(marker);
    await nativePage.reload();
    await expect(markerNode).toBeVisible({ timeout: 30_000 });
  } finally {
    await nativeContext.close();
    if (workflowId) await clientA.delete(`/api/v2/workflows/${workflowId}`).catch(() => {});
    await clientA.dispose();
    await clientB.dispose();
  }
});

test("el marco SIM sólo delega la escritura: leer queda denegado por su política", async ({
  browser,
}) => {
  // El permiso del navegador se acepta aquí (lo simula el contexto): aunque el usuario acepte
  // leer y escribir, el marco del SIM sólo delega la escritura. Sólo se usa contenido propio.
  const context = await browser.newContext({
    baseURL: BASE,
    permissions: ["clipboard-read", "clipboard-write"],
  });
  try {
    const page = await context.newPage();
    await signIn(page);
    const url = await simURL(page);
    test.skip(url === "", "esta instalación no publica sim_url: el caso necesita el SIM real");
    const origin = new URL(url).origin;
    const marker = `wb-sim-clipboard-fixture-${Date.now()}`;
    await page.goto("/?section=sim");
    const frameElement = page.locator('iframe[title^="SIM"]');
    const inner = page.frameLocator('iframe[title^="SIM"]');
    await expect(inner.locator("body")).toContainText(/\S/, { timeout: 30_000 });
    const allow = (await frameElement.getAttribute("allow")) ?? "";
    expect(allow, "la sección delega la escritura que el SIM usa").toContain("clipboard-write");
    expect(allow, "la sección no pide permiso de lectura").not.toContain("clipboard-read");
    let frame = page.frames().find((candidate) => candidate.url().startsWith(origin));
    for (let attempt = 0; attempt < 30 && !frame; attempt += 1) {
      await page.waitForTimeout(1000);
      frame = page.frames().find((candidate) => candidate.url().startsWith(origin));
    }
    expect(frame, `sin marco en ${origin}`).toBeTruthy();
    if (!(await frame!.evaluate(() => document.hasFocus()))) {
      await inner.locator("body").click({ position: { x: 4, y: 4 }, force: true }).catch(() => {});
    }
    expect(
      await frame!.evaluate(() => document.hasFocus()),
      "el marco del SIM debe recibir el foco para usar el portapapeles",
    ).toBe(true);
    const written = await frame!.evaluate(async (value) => {
      try {
        await navigator.clipboard.writeText(value);
        return "escrito";
      } catch (error) {
        return error instanceof Error ? error.name : String(error);
      }
    }, marker);
    expect(written, "escritura delegada al marco del SIM").toBe("escrito");
    const readDenied = await frame!.evaluate(() =>
      navigator.clipboard
        .readText()
        .then(() => "leido")
        .catch((error) => error.name),
    );
    expect(readDenied, "lectura no delegada al marco del SIM").toBe("NotAllowedError");
  } finally {
    await context.close();
  }
});

test("la API de Orbit retirada no sirve nada: 404 JSON", async ({ page }) => {
  await signIn(page);
  for (const [method, path] of [
    ["GET", "/api/orbit"],
    ["GET", "/api/orbit/runs"],
    ["POST", "/api/orbit/workflows"],
  ] as const) {
    const response = await page.request.fetch(path, { method, data: method === "POST" ? {} : undefined });
    expect(response.status(), `${method} ${path}`).toBe(404);
    expect(response.headers()["content-type"] ?? "", `${method} ${path}`).toContain("application/json");
    expect(await response.text(), `${method} ${path} no devuelve HTML`).not.toContain("<html");
  }
});

test("las secciones que siguen existiendo abren después de la retirada", async ({ page }) => {
  await signIn(page, "/?section=settings");
  await expect(page.getByRole("heading", { name: "Proyectos", exact: true })).toBeVisible();

  await page.goto("/?section=models");
  await expect(page.getByLabel("Host de los modelos")).toBeVisible();

  await page.goto("/?section=usage");
  await expect(page.getByRole("heading", { name: "Configuración de agentes", exact: true })).toBeVisible();

  await page.goto("/?section=work");
  await expect(page.getByRole("heading", { level: 1, name: "Conversaciones", exact: true })).toBeVisible();
});
