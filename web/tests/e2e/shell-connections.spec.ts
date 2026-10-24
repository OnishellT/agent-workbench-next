import { expect, test, type Page } from "@playwright/test";
import { mkdtemp, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  MAX_CONNECTIONS,
  MAX_CONNECTIONS_BYTES,
  connectionOrigin,
  exportConnections,
  type ConnectionDTO,
} from "../../src/app/connections-store";
import { requireRelease, signIn, watchConsole, type ConsoleGuard } from "./helpers";

// UI-05/UI-06 against the real release: connections are names + addresses kept by the
// browser, another installation opens as a plain navigation and the exchange file
// carries nothing else. The two-root case also needs E2E_DEST_BASE_URL and
// E2E_DEST_ACCESS_TOKEN; no mock, no skip.
//
//   E2E_BASE_URL=https://workbench.example E2E_ACCESS_TOKEN=... bun run test tests/e2e/shell-connections.spec.ts

let consoleGuard: ConsoleGuard;
const DEST_BASE = process.env.E2E_DEST_BASE_URL;
const DEST_TOKEN = process.env.E2E_DEST_ACCESS_TOKEN;

test.beforeAll(requireRelease);

test.beforeEach(({ page }) => {
  consoleGuard = watchConsole(page);
});

test.afterEach(() => {
  expect(consoleGuard.errors, "consola del navegador").toEqual([]);
});

async function openConnections(page: Page) {
  await page.goto("/?section=connections");
  await expect(
    page.getByRole("heading", { name: "Conexiones", exact: true }),
  ).toBeVisible();
}

async function addConnection(
  page: Page,
  name: string,
  address: string,
) {
  const form = page.locator("form").filter({ has: page.getByLabel("Nombre") });
  await form.getByLabel("Nombre").fill(name);
  await form.getByLabel("Dirección").fill(address);
  await form.getByRole("button", { name: "Guardar conexión", exact: true }).click();
}

async function storedConnections(page: Page) {
  const raw = await page.evaluate(() =>
    localStorage.getItem("agent-workbench.connections.v1"),
  );
  return raw ? (JSON.parse(raw) as ConnectionDTO[]) : [];
}

test("una dirección válida se guarda y persiste por recarga", async ({ page }) => {
  await signIn(page);
  await openConnections(page);
  await addConnection(page, "VPS de prueba", "https://workbench.example.test");
  await expect(page.getByRole("status").filter({ hasText: "Conexión guardada" })).toBeVisible();

  const link = page.getByRole("link", { name: /Abrir/ });
  await expect(link).toBeVisible();
  await expect(link).toHaveAttribute("href", "https://workbench.example.test");
  // Otra instalación se abre navegando a su propio login, no dentro de un marco.
  expect(await page.locator("iframe").count()).toBe(0);

  await page.reload();
  await expect(page.getByRole("link", { name: /Abrir/ })).toHaveAttribute(
    "href",
    "https://workbench.example.test",
  );
  expect(await storedConnections(page)).toEqual([
    { name: "VPS de prueba", origin: "https://workbench.example.test" },
  ]);
});

test("abrir otra instalación exige su propia autenticación y conserva ambos orígenes", async ({ page }) => {
  if (!DEST_BASE || !DEST_TOKEN)
    throw new Error("This case needs its own E2E_DEST_BASE_URL and E2E_DEST_ACCESS_TOKEN.");
  const destinationOrigin = connectionOrigin(DEST_BASE);
  if (!["localhost", "127.0.0.1", "::1", "[::1]"].includes(new URL(destinationOrigin).hostname))
    throw new Error("The destination for this case must be an owned loopback installation.");

  await signIn(page);
  await openConnections(page);

  // Legacy semantics: saving the same origin updates its name; changing an address
  // means removing the old entry and saving the replacement.
  const oldOrigin = "http://127.0.0.1:19100";
  await addConnection(page, "Anterior", oldOrigin);
  await expect(page.getByRole("status").filter({ hasText: "Conexión guardada" })).toBeVisible();
  await addConnection(page, "Renombrada", oldOrigin);
  await expect(page.getByRole("status").filter({ hasText: "Nombre de conexión actualizado" })).toBeVisible();
  expect(await storedConnections(page)).toEqual([{ name: "Renombrada", origin: oldOrigin }]);
  await page.getByRole("button", { name: "Quitar conexión: Renombrada", exact: true }).click();
  expect(await storedConnections(page)).toEqual([]);

  await addConnection(page, "Instalación B", destinationOrigin);
  await expect(page.getByRole("status").filter({ hasText: "Conexión guardada" })).toBeVisible();
  await page.reload();
  expect(await storedConnections(page)).toEqual([
    { name: "Instalación B", origin: destinationOrigin },
  ]);
  const link = page.getByRole("link", { name: /Abrir/ });
  await expect(link).toHaveAttribute("href", destinationOrigin);
  expect(await page.locator("iframe").count()).toBe(0);

  const unauthenticatedRead = page.waitForResponse((response) => {
    const url = new URL(response.url());
    return url.origin === destinationOrigin && url.pathname === "/api/bootstrap";
  });
  await link.click();
  const firstRead = await unauthenticatedRead;
  expect(firstRead.status()).toBe(401);
  const destinationHeaders = await firstRead.request().allHeaders();
  const sentCookie = Object.keys(destinationHeaders).some((key) => key.toLowerCase() === "cookie");
  expect(sentCookie, "A's session cookie is not sent to B before B signs in").toBe(false);
  await expect(page.locator("#access-token")).toBeVisible();
  expect(new URL(page.url()).origin).toBe(destinationOrigin);

  await page.locator("#access-token").fill(DEST_TOKEN);
  await page.getByRole("button", { name: "Entrar", exact: true }).click();
  await expect(page.getByRole("navigation", { name: "Navegación principal" })).toBeVisible();
  const destinationRead = await page.request.get(new URL("/api/bootstrap", destinationOrigin).href);
  expect(destinationRead.status()).toBe(200);
  const destinationHasSettings = Boolean((await destinationRead.json())?.settings);
  expect(destinationHasSettings).toBe(true);
  await page.goto(new URL("/?section=connections", destinationOrigin).href);
  await expect(page.getByRole("heading", { name: "Conexiones", exact: true })).toBeVisible();
  expect(await storedConnections(page)).toEqual([]);

  await openConnections(page);
  await expect(page.getByRole("navigation", { name: "Navegación principal" })).toBeVisible();
  const sourceRead = await page.request.get(new URL("/api/bootstrap", page.url()).href);
  expect(sourceRead.status()).toBe(200);
  expect(await storedConnections(page)).toEqual([
    { name: "Instalación B", origin: destinationOrigin },
  ]);
});

test("HTTP remoto y direcciones con credenciales se rechazan sin tocar la lista", async ({ page }) => {
  await signIn(page);
  await openConnections(page);
  await addConnection(page, "Local", "http://127.0.0.1:18888");
  await expect(page.getByRole("status").filter({ hasText: "Conexión guardada" })).toBeVisible();

  await addConnection(page, "Remoto sin TLS", "http://workbench.example.test");
  await expect(page.getByRole("alert").filter({ hasText: "HTTPS" })).toBeVisible();
  await addConnection(page, "Con credenciales", "https://user:pass@workbench.example.test");
  await expect(page.getByRole("alert").filter({ hasText: "sin rutas, tokens ni credenciales" })).toBeVisible();
  await addConnection(page, "Con ruta", "https://workbench.example.test/api");
  await expect(page.getByRole("alert").filter({ hasText: "sin rutas, tokens ni credenciales" })).toBeVisible();
  await addConnection(page, "Con query", "https://workbench.example.test/?token=abc");
  await expect(page.getByRole("alert").filter({ hasText: "sin rutas, tokens ni credenciales" })).toBeVisible();
  await addConnection(page, "Con fragmento", "https://workbench.example.test/#token");
  await expect(page.getByRole("alert").filter({ hasText: "sin rutas, tokens ni credenciales" })).toBeVisible();
  await addConnection(page, "Nombreinválido", "http://127.0.0.1:18889");
  await expect(page.getByRole("alert").filter({ hasText: "nombre de hasta 64 caracteres" })).toBeVisible();

  expect(await storedConnections(page)).toEqual([
    { name: "Local", origin: "http://127.0.0.1:18888" },
  ]);

  const form = page.locator("form").filter({ has: page.getByLabel("Nombre") });
  await form.getByLabel("Nombre").fill("n".repeat(65));
  await expect(form.getByLabel("Nombre")).toHaveValue("n".repeat(64));
  await form.getByLabel("Nombre").fill("");
  await form.getByLabel("Dirección").fill("");

  for (let i = 0; i < MAX_CONNECTIONS - 1; i++)
    await addConnection(page, "Límite " + i, "http://127.0.0.1:" + (19101 + i));
  const full = await storedConnections(page);
  expect(full).toHaveLength(MAX_CONNECTIONS);
  await addConnection(page, "Exceso", "http://127.0.0.1:19124");
  await expect(page.getByRole("alert").filter({ hasText: "hasta 24 conexiones" })).toBeVisible();
  expect(await storedConnections(page)).toEqual(full);

  await page.getByRole("button", { name: "Quitar conexión: Local", exact: true }).click();
  await addConnection(page, "Recuperada", "http://127.0.0.1:19124");
  await expect(page.getByRole("status").filter({ hasText: "Conexión guardada" })).toBeVisible();
  await page.reload();
  const recovered = await storedConnections(page);
  expect(recovered).toHaveLength(MAX_CONNECTIONS);
  expect(recovered).toContainEqual({ name: "Recuperada", origin: "http://127.0.0.1:19124" });
});

test("exportar publica solo nombres y direcciones", async ({ page }) => {
  await signIn(page);
  await openConnections(page);
  const own = [
    { name: "VPS de prueba", origin: "https://workbench.example.test" },
    { name: "Segunda instalación", origin: "https://segunda.example.test" },
  ];
  for (const entry of own) {
    await addConnection(page, entry.name, entry.origin);
    await expect(page.getByRole("status").filter({ hasText: "Conexión guardada" })).toBeVisible();
  }

  const [download] = await Promise.all([
    page.waitForEvent("download"),
    page.getByRole("button", { name: "Exportar direcciones", exact: true }).click(),
  ]);
  expect(download.suggestedFilename()).toBe("workbench-connections.json");
  const text = await readFile(await download.path(), "utf8");
  const payload = JSON.parse(text);
  expect(Object.keys(payload)).toEqual(["version", "connections"]);
  expect(payload.connections).toEqual(own);
  // Nada de tokens, cuentas, SSH ni proyectos viaja en el archivo.
  expect(text).not.toMatch(/token|password|ssh|account|project|cookie/i);

  // La descarga real vuelve por la UI: se quitan las direcciones propias y el mismo
  // archivo las restaura, incluso tras recargar.
  for (const entry of own)
    await page
      .getByRole("button", { name: `Quitar conexión: ${entry.name}`, exact: true })
      .click();
  expect(await storedConnections(page)).toEqual([]);
  await page.getByLabel("Importar archivo de conexiones").setInputFiles(await download.path());
  await expect(page.getByRole("status").filter({ hasText: "Conexiones importadas" })).toBeVisible();
  expect(await storedConnections(page)).toEqual(own);
  await page.reload();
  for (const entry of own)
    await expect(page.locator(`a[href="${entry.origin}"]`)).toBeVisible();
  expect(await storedConnections(page)).toEqual(own);
});

test("importar valida el archivo antes de tocar la lista y respeta el tope", async ({ page }) => {
  await signIn(page);
  await openConnections(page);
  await addConnection(page, "Actual", page.url().split("/?")[0]);
  await expect(page.getByRole("status").filter({ hasText: "Conexión guardada" })).toBeVisible();
  const before = await storedConnections(page);

  const dir = await mkdtemp(join(tmpdir(), "workbench-connections-"));
  const extra = join(dir, "extra.json");
  await writeFile(
    extra,
    JSON.stringify({ version: 1, connections: [], tokens: ["secreto"] }),
  );
  const leaky = join(dir, "leaky.json");
  await writeFile(
    leaky,
    JSON.stringify({
      version: 1,
      connections: [
        { name: "Con secreto", origin: "https://secreto.example.test", token: "secreto" },
      ],
    }),
  );
  const broken = join(dir, "broken.json");
  await writeFile(broken, '{ "version": 1, "connections": [ }');
  // El relleno es JSON válido: sólo el tamaño lo hace fallar.
  const huge = join(dir, "huge.json");
  await writeFile(
    huge,
    " ".repeat(80000) +
      JSON.stringify({
        version: 1,
        connections: [{ name: "Grande", origin: "https://grande.example.test" }],
      }),
  );
  const over = join(dir, "over.json");
  await writeFile(
    over,
    JSON.stringify({
      version: 1,
      connections: Array.from({ length: MAX_CONNECTIONS + 1 }, (_, i) => ({
        name: `vps-${i}`,
        origin: `https://vps-${i}.example.test`,
      })),
    }),
  );
  const good = join(dir, "good.json");
  await writeFile(
    good,
    exportConnections([
      { name: "Nueva", origin: "https://nueva.example.test" },
      { name: "Renombrada", origin: before[0]?.origin ?? "https://actual.example.test" },
    ]),
  );

  const input = page.getByLabel("Importar archivo de conexiones");
  await input.setInputFiles(extra);
  await expect(page.getByRole("alert").filter({ hasText: "exportado desde Conexiones" })).toBeVisible();
  expect(await storedConnections(page), "un archivo inválido no pisa la lista").toEqual(before);

  await input.setInputFiles(leaky);
  await expect(page.getByRole("alert").filter({ hasText: "sólo un nombre y una dirección" })).toBeVisible();
  expect(await storedConnections(page), "una entrada con secreto no pisa la lista").toEqual(before);

  await input.setInputFiles(broken);
  await expect(page.getByRole("alert").filter({ hasText: "no contiene un JSON válido" })).toBeVisible();
  expect(await storedConnections(page), "un JSON roto no pisa la lista").toEqual(before);

  await input.setInputFiles(huge);
  await expect(page.getByRole("alert").filter({ hasText: "menos de 64 KiB" })).toBeVisible();
  expect(await storedConnections(page), "un archivo enorme no pisa la lista").toEqual(before);

  await input.setInputFiles(over);
  await expect(page.getByRole("alert").filter({ hasText: "más de 24 conexiones" })).toBeVisible();
  expect(await storedConnections(page)).toEqual(before);

  await input.setInputFiles(good);
  await expect(page.getByRole("status").filter({ hasText: "Conexiones importadas" })).toBeVisible();
  await page.reload();
  await expect(page.getByText("https://nueva.example.test", { exact: true })).toBeVisible();
  await expect(page.getByText("Renombrada", { exact: true })).toBeVisible();
});

// El tope se mide sobre el archivo real: un nombre multibyte dentro de un JSON válido
// puede superar 64 KiB en bytes aunque su texto tenga menos de 65536 caracteres
// (el legado comprobaba File.size antes de leer el contenido).
test("un archivo de más de 64 KiB en bytes no pisa la lista y se recupera", async ({ page }) => {
  await signIn(page);
  await openConnections(page);
  await addConnection(page, "Actual", page.url().split("/?")[0]);
  await expect(page.getByRole("status").filter({ hasText: "Conexión guardada" })).toBeVisible();
  const before = await storedConnections(page);

  const payload = JSON.stringify({
    version: 1,
    connections: [{ name: "ñ".repeat(64), origin: "https://ui06.example.test" }],
  });
  const text = " ".repeat(65500 - payload.length) + payload;
  expect(text.length).toBeLessThanOrEqual(MAX_CONNECTIONS_BYTES);
  expect(Buffer.byteLength(text, "utf8")).toBeGreaterThan(MAX_CONNECTIONS_BYTES);

  const dir = await mkdtemp(join(tmpdir(), "workbench-connections-"));
  const oversized = join(dir, "multibyte.json");
  await writeFile(oversized, text);
  const good = join(dir, "good.json");
  await writeFile(
    good,
    exportConnections([{ name: "Nueva", origin: "https://nueva.example.test" }]),
  );

  const input = page.getByLabel("Importar archivo de conexiones");
  await input.setInputFiles(oversized);
  await expect(page.getByRole("alert").filter({ hasText: "menos de 64 KiB" })).toBeVisible();
  expect(await storedConnections(page), "un archivo grande no pisa la lista").toEqual(before);

  await input.setInputFiles(good);
  await expect(page.getByRole("status").filter({ hasText: "Conexiones importadas" })).toBeVisible();
  await page.reload();
  await expect(page.getByText("https://nueva.example.test", { exact: true })).toBeVisible();
  expect(await storedConnections(page)).toEqual([
    ...before,
    { name: "Nueva", origin: "https://nueva.example.test" },
  ]);
});
