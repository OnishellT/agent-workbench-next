import { expect, test, type Page } from "@playwright/test";

const first = process.env.E2E_BASE_URL;
const second = process.env.E2E_SECOND_BASE_URL;
const firstToken = process.env.E2E_ACCESS_TOKEN;
const secondToken = process.env.E2E_SECOND_ACCESS_TOKEN;

test.beforeAll(() => {
  if (!first || !second || !firstToken || !secondToken)
    throw new Error("Two real installations and their access tokens must be configured explicitly.");
  if (new URL(first).origin === new URL(second).origin)
    throw new Error("The second installation must have a different origin.");
});

async function unlock(page: Page, token: string) {
  await expect(page.locator("#access-token")).toBeVisible();
  await page.locator("#access-token").fill(token);
  await page.getByRole("button", { name: "Entrar", exact: true }).click();
  await expect(page.getByRole("navigation", { name: "Navegación principal", exact: true })).toBeVisible();
}

/** One record of an installation, named with a marker the other installation must never see. */
interface RegistryHost {
  id?: string;
  name?: string;
  enabled?: boolean;
}

async function registry(page: Page, origin: string) {
  const response = await page.request.get(new URL("/api/hosts", origin).href);
  expect(response.status()).toBe(200);
  const body = (await response.json()) as { hosts?: RegistryHost[] };
  return body.hosts ?? [];
}

/** A new, paused host record of that installation: no credential and no connection is
 *  attempted, so the record itself is the state under test. Created once, reused after. */
async function markedHost(page: Page, origin: string, marker: string) {
  const found = (await registry(page, origin)).find((row) => row.name === marker);
  if (found) return found;
  const response = await page.request.post(new URL("/api/hosts", origin).href, {
    data: {
      name: marker,
      address: "127.0.0.1",
      user: "e2e",
      port: 22,
      auth: "password",
      // The record is paused, so this sentinel is never used to open a connection: it only
      // satisfies the "password" auth the registry stores sealed.
      password: "e2e-sentinel-not-a-credential",
      enabled: false,
    },
  });
  expect([200, 201], `alta de ${marker} en ${origin}`).toContain(response.status());
  const created = (await registry(page, origin)).find((row) => row.name === marker);
  expect(created, `${marker} registrado en ${origin}`).toBeTruthy();
  return created!;
}

test("dos instalaciones sirven la SPA y mantienen sesiones y datos aislados", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.goto(first!);
  await unlock(page, firstToken!);
  // Real records of each installation, not empty lists: the markers prove that neither
  // registry leaks into the other one.
  const markerA = "aislamiento-frontend-a";
  const markerB = "aislamiento-frontend-b";
  const hostA = await markedHost(page, first!, markerA);

  await page.goto(new URL("/?section=connections", first).href);
  const form = page.locator("form").filter({ has: page.getByLabel("Nombre") });
  await form.getByLabel("Nombre").fill("Instalación aislada de pruebas");
  await form.getByLabel("Dirección").fill(second!);
  await form.getByRole("button", { name: "Guardar conexión", exact: true }).click();
  const open = page.getByRole("link", { name: "Abrir", exact: true });
  await expect(open).toHaveAttribute("href", new URL(second!).origin);
  expect(await page.locator("iframe").count()).toBe(0);
  await open.click();
  await expect.poll(() => new URL(page.url()).origin).toBe(new URL(second!).origin);
  await unlock(page, secondToken!);
  const hostB = await markedHost(page, second!, markerB);

  const listA = await registry(page, first!);
  const listB = await registry(page, second!);
  expect(listA.some((row) => row.name === markerA), `${markerA} en su instalación`).toBe(true);
  expect(listB.some((row) => row.name === markerB), `${markerB} en su instalación`).toBe(true);
  expect(listA.some((row) => row.name === markerB), `registro de B visto desde A`).toBe(false);
  expect(listB.some((row) => row.name === markerA), `registro de A visto desde B`).toBe(false);

  // The screens, not only the API: Equipos of each installation shows its own record and
  // never the other one's marker.
  await page.goto(new URL("/?section=hosts", first).href);
  await expect(page.locator(`[data-host="${hostA.id}"]`)).toBeVisible();
  await expect(page.getByText(markerB, { exact: true })).toHaveCount(0);
  await page.goto(new URL("/?section=hosts", second).href);
  await expect(page.locator(`[data-host="${hostB.id}"]`)).toBeVisible();
  await expect(page.getByText(markerA, { exact: true })).toHaveCount(0);

  await page.goto(first!);
  await expect(page.getByRole("navigation", { name: "Navegación principal", exact: true })).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Equipos", exact: true }),
  ).toBeVisible();
  await page.goto(second!);
  await expect(page.getByRole("navigation", { name: "Navegación principal", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Salir", exact: true }).click();
  await expect(page.locator("#access-token")).toBeVisible();
  await page.goto(first!);
  await expect(page.getByRole("navigation", { name: "Navegación principal", exact: true })).toBeVisible();
  // Durability after the reload and the foreign session: the same record, still only here.
  const afterA = await registry(page, first!);
  expect(afterA.find((row) => row.name === markerA)?.id, `${markerA} tras recargar`).toBe(hostA.id);
  expect(afterA.some((row) => row.name === markerB), `registro de B tras la otra sesión`).toBe(false);
  // B was logged out above: its registry is read again only through a new session, and the
  // record of B is still there while A's is not.
  await page.goto(second!);
  await unlock(page, secondToken!);
  const afterB = await registry(page, second!);
  expect(afterB.find((row) => row.name === markerB)?.id, `${markerB} en su instalación`).toBe(hostB.id);
  expect(afterB.some((row) => row.name === markerA), `registro de A en B`).toBe(false);
  expect(errors).toEqual([]);
});
