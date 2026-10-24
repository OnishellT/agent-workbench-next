import { expect, test, type Page } from "@playwright/test";
import { requireRelease, signIn, watchConsole, type ConsoleGuard } from "./helpers";
import type { HostSummaryDTO } from "../../src/modules/hosts/types";

// Atlas retired: the shell must expose the host registry as its own section, send the old
// deep links to the screen that owns the data now, and never reach the retired namespace.
// Real release only: E2E_BASE_URL + E2E_ACCESS_TOKEN, no mock, no route fulfill, no skip.
//
//   E2E_BASE_URL=http://127.0.0.1:18913 E2E_ACCESS_TOKEN=... \
//     bun run test tests/e2e/shell-atlas-retired-release.spec.ts

let guard: ConsoleGuard | null = null;

test.beforeAll(requireRelease);

test.beforeEach(({ page }) => {
  guard = watchConsole(page);
  // The remote-history receipt is still answered 501 by this release (Foundry M05/M06) and
  // the Equipos screen reads it for every registered host: an expected answer, not a failure.
  guard.expected.push("/history");
  // Modelos reaches the selected remote host through the published proxy. In this file the only
  // such host is unreachable on purpose, so its refusal is an expected answer of that case.
  guard.expected.push("/api/hosts/");
});

test.afterEach(() => {
  expect(guard?.errors ?? [], "consola del navegador").toEqual([]);
});

/** Every request the page makes while navigating: the retired namespace must not appear. */
function watchAtlas(page: Page) {
  const hits: string[] = [];
  page.on("request", (request) => {
    const path = new URL(request.url()).pathname;
    if (path.startsWith("/api/atlas")) hits.push(path);
  });
  return hits;
}

test("Equipos se abre en Conexiones y Consumo ya no existe en la navegación", async ({ page }) => {
  await signIn(page);
  const nav = page.getByRole("navigation", { name: "Navegación principal" });
  await expect(nav.getByRole("button", { name: "Conexiones", exact: true })).toBeVisible();
  await expect(nav.getByRole("button", { name: "Consumo", exact: true })).toHaveCount(0);
  await expect(nav.getByRole("button", { name: /Atlas/ })).toHaveCount(0);
  await nav.getByRole("button", { name: "Conexiones", exact: true }).click();
  await expect(page).toHaveURL(/section=connections/);
  await expect(
    page.getByRole("heading", { name: "Equipos SSH · Magpie e historial" }),
  ).toBeVisible();
  // The screen and the host selectors share the frozen envelope {hosts:[...]} at /api/hosts:
  // not an Atlas route, and the same call for every consumer.
  const registry = await page.request.get("/api/hosts");
  expect(registry.status()).toBe(200);
  const body = (await registry.json()) as { hosts?: unknown };
  expect(Array.isArray(body.hosts), "GET /api/hosts responde {hosts:[...]}").toBe(true);
  // Reload keeps the section, and the history stays on the section the operator chose.
  await page.reload();
  await expect(
    page.getByRole("heading", { name: "Equipos SSH · Magpie e historial" }),
  ).toBeVisible();
  await expect(nav.getByRole("button", { name: "Conexiones", exact: true })).toHaveAttribute(
    "aria-current",
    "page",
  );
});

test("el uso vive en Modelos → Uso: ida, vuelta atrás y recarga", async ({ page }) => {
  await signIn(page);
  await page.goto("/?section=hosts");
  await page.getByRole("button", { name: "Ver uso por equipo", exact: true }).click();
  await expect(page).toHaveURL(/section=models&view=usage/);
  await expect(
    page.frameLocator('iframe[title^="Magpie"]').locator("#view-usage"),
  ).toHaveJSProperty("hidden", false);
  await page.reload();
  await expect(page).toHaveURL(/section=models&view=usage/);
  await expect(
    page.frameLocator('iframe[title^="Magpie"]').locator("#view-usage"),
  ).toHaveJSProperty("hidden", false);
  await page.goBack();
  await expect(page).toHaveURL(/section=hosts/);
  await expect(
    page.getByRole("heading", { name: "Equipos SSH · Magpie e historial" }),
  ).toBeVisible();
});

test("los deep links antiguos de Atlas aterrizan sin llamar al namespace retirado", async ({ page }) => {
  const hits = watchAtlas(page);
  await signIn(page);
  // The registry keeps its screen.
  await page.goto("/?section=atlas&view=hosts");
  await expect(
    page.getByRole("heading", { name: "Equipos SSH · Magpie e historial" }),
  ).toBeVisible();
  // Consumption is the selected host's Magpie usage, not a revived Atlas view.
  await page.goto("/?section=atlas&view=activity&period=30&dataset=demo");
  await expect(
    page.frameLocator('iframe[title^="Magpie"]').locator("#view-usage"),
  ).toHaveJSProperty("hidden", false);
  // The Uso tab belongs to the selected host of Modelos: its own screen, not a revived Atlas.
  await expect(page.getByLabel("Host de los modelos")).toBeVisible();
  expect(hits, "peticiones al namespace Atlas retirado").toEqual([]);
});

test("el panel de un equipo abre Modelos con ese host y lo conserva al recargar", async ({ page }) => {
  await signIn(page, "/?section=hosts");
  // A real record of this installation, created through the authorized root API: paused and
  // with a sentinel password, so no connection is attempted and no credential is involved.
  const marker = "regresion-panel-host";
  const list = (await (await page.request.get("/api/hosts")).json()) as {
    hosts?: HostSummaryDTO[];
  };
  // The row's actions stay blocked while a host is paused or its key is unconfirmed, so the
  // record is enabled against a port where nothing listens: no handshake, no observation, and
  // the button under test is the real one.
  let host = (list.hosts ?? []).find((row) => row.name === marker && row.enabled === true);
  if (!host) {
    const created = await page.request.post("/api/hosts", {
      data: {
        name: marker,
        address: "127.0.0.1",
        user: "e2e",
        port: 18999,
        auth: "password",
        password: "e2e-sentinel-not-a-credential",
        enabled: true,
      },
    });
    expect([200, 201], "alta del equipo de la regresión").toContain(created.status());
    host = (await created.json()) as HostSummaryDTO;
  }
  expect(host.id, "id del equipo").toBeTruthy();
  const id = host.id;

  await page.goto("/?section=hosts");
  const row = page.locator(`[data-host="${id}"]`);
  await expect(row).toBeVisible();
  await row.getByRole("button", { name: "Abrir panel", exact: true }).click();
  // The selection travels in the same ?host= the sections already read: opening Modelos must
  // land on that host, never on "Este servidor".
  await expect(page).toHaveURL(/section=models/);
  await expect(page).toHaveURL(new RegExp(`host=${id}`));
  await expect(page.getByLabel("Host de los modelos")).toHaveValue(id);
  await page.reload();
  await expect(page.getByLabel("Host de los modelos")).toHaveValue(id);

});

test("las rutas retiradas de Atlas responden 404, no un 200 vacío", async ({ page }) => {
  await signIn(page);
  for (const path of ["/api/atlas/api/summary", "/api/atlas/captura", "/api/atlas/api/prices"]) {
    const response = await page.request.get(path);
    expect(response.status(), path).toBe(404);
  }
});
