import { execFileSync } from "node:child_process";
import { expect, test, type Page } from "@playwright/test";
import { BASE, signIn, watchConsole, type ConsoleGuard } from "./helpers";

// M02 PL-06: la lista de carpetas nuevas de esta instalación se gestiona con alta y
// quita, como el contrato (sin editor aparte), y el registro sirve exactamente esas carpetas.
// El caso escribe y siempre restaura lo que encontró; las carpetas y el fichero de la
// fixture son propios del ejecutante (con espacios y acentos), nunca del operador.
//
//   E2E_BASE_URL=http://127.0.0.1:19012 E2E_ACCESS_TOKEN=... \
//   E2E_PROJECT_DIR=/.../a/projects/alfa\ ñandú E2E_PROJECT_DIR_B=/.../a/projects/beta\ gamma \
//   E2E_PROJECTS_FILE=/.../a/projects/not-a-dir.txt E2E_PROJECTS_BULK_DIR=/.../a/projects/bulk \
//   E2E_RESTART_COMMAND=/.../restart-a.sh
//     bun run test tests/e2e/shell-projects-release.spec.ts
//
// E2E_PROJECTS_BULK_DIR contiene cap-001…cap-100 (carpetas propias) para comprobar el tope real.

interface ProjectsFixture {
  alfa: string;
  beta: string;
  file: string;
  bulk: string;
  restart: string;
}

let guard: ConsoleGuard | null = null;
test.beforeEach(({ page }) => {
  guard = watchConsole(page);
});
test.afterEach(() => {
  expect(guard?.errors ?? [], "consola del navegador").toEqual([]);
  guard = null;
});

function requiredProjectsFixture(): ProjectsFixture {
  const alfa = process.env.E2E_PROJECT_DIR;
  const beta = process.env.E2E_PROJECT_DIR_B;
  const file = process.env.E2E_PROJECTS_FILE;
  const bulk = process.env.E2E_PROJECTS_BULK_DIR;
  const restart = process.env.E2E_RESTART_COMMAND;
  if (!alfa || !beta || !file || !bulk || !restart)
    throw new Error(
      "This case needs its own folders: set E2E_PROJECT_DIR, E2E_PROJECT_DIR_B, E2E_PROJECTS_FILE, E2E_PROJECTS_BULK_DIR and E2E_RESTART_COMMAND (an executable that restarts this installation). No default, no fallback.",
    );
  for (const [name, value] of [
    ["E2E_PROJECT_DIR", alfa],
    ["E2E_PROJECT_DIR_B", beta],
    ["E2E_PROJECTS_FILE", file],
    ["E2E_PROJECTS_BULK_DIR", bulk],
  ] as const) {
    if (!value.startsWith("/") || value.endsWith("/"))
      throw new Error(`${name} must be an absolute path without a trailing slash, got "${value}"`);
  }
  return { alfa, beta, file, bulk, restart };
}

/** Las 100 carpetas propias del tope, con el nombre que prepara la fixture. */
function bulkPaths(bulk: string) {
  return Array.from({ length: 100 }, (_, index) => `${bulk}/cap-${String(index + 1).padStart(3, "0")}`);
}

async function readProjects(page: Page): Promise<{ path: string; name?: string }[]> {
  const response = await page.request.get("/api/bootstrap");
  expect(response.status()).toBe(200);
  const body = (await response.json()) as { settings?: { projects?: { path: string; name?: string }[] } };
  return body.settings?.projects ?? [];
}

/** La lista que la pantalla muestra, leída de sus propios controles de quita. */
async function screenProjects(page: Page) {
  await page.goto("/?section=settings");
  const buttons = await page.getByRole("button", { name: /^Quitar: / }).all();
  return Promise.all(
    buttons.map(async (button) => ((await button.getAttribute("aria-label")) ?? "").replace("Quitar: ", "")),
  );
}

async function addProject(page: Page, path: string) {
  await page.getByLabel("Ruta de la carpeta").fill(path);
  await page.getByRole("button", { name: "Añadir", exact: true }).click();
}

async function expectRejected(page: Page, what: string, before: string[]) {
  await expect(page.getByRole("alert"), `rechazo de ${what}`).toBeVisible();
  expect(await screenProjects(page), `la lista cambió con ${what}`).toEqual(before);
  expect((await readProjects(page)).map((row) => row.path), `el registro cambió con ${what}`).toEqual(before);
}

async function restoreProjects(page: Page, found: { path: string; name?: string }[]) {
  const response = await page.request.post("/api/settings", { data: { projects: found } });
  expect([200, 201], "restaurar la lista de carpetas de la fixture").toContain(response.status());
}

test("PL-06: una carpeta nueva se añade, sobrevive al reinicio y se sustituye", async ({
  page,
}) => {
  test.setTimeout(300_000);
  const fixture = requiredProjectsFixture();
  await signIn(page);
  const found = await readProjects(page);
  const before = found.map((row) => row.path);
  try {
    // Alta desde Ajustes, con espacios y acentos, y la carpeta queda registrada.
    await screenProjects(page);
    await addProject(page, fixture.alfa);
    await expect(page.getByText("Proyecto añadido.")).toBeVisible();
    await expect
      .poll(async () => (await readProjects(page)).map((row) => row.path))
      .toEqual([...before, fixture.alfa]);

    // La carpeta se publica para los consumidores actuales; Work permanece retirado.
    expect((await readProjects(page)).map((row) => row.path)).toContain(fixture.alfa);
    expect((await page.request.get("/api/work/runs")).status()).toBe(404);
    await page.reload();
    expect(await screenProjects(page)).toContain(fixture.alfa);
    execFileSync(fixture.restart, { stdio: "pipe" });
    await expect
      .poll(
        async () => {
          try {
            return (await page.request.get(new URL("/api/bootstrap", BASE!).href, { timeout: 2000 })).status();
          } catch {
            return 0;
          }
        },
        { message: "la instalación reiniciada vuelve a servir", timeout: 90_000, intervals: [500] },
      )
      .toBe(200);
    await page.reload();
    expect(await screenProjects(page)).toContain(fixture.alfa);

    // Quitar y sustituir por otra carpeta propia: sin registros fantasma.
    await page.getByRole("button", { name: `Quitar: ${fixture.alfa}`, exact: true }).click();
    await expect(page.getByText("Proyecto quitado.")).toBeVisible();
    await expect(page.getByRole("button", { name: `Quitar: ${fixture.alfa}`, exact: true })).toHaveCount(0);
    await addProject(page, fixture.beta);
    await expect(page.getByText("Proyecto añadido.")).toBeVisible();
    expect(await screenProjects(page)).toEqual([...before, fixture.beta]);
    expect((await readProjects(page)).map((row) => row.path)).toEqual([...before, fixture.beta]);
  } finally {
    await restoreProjects(page, found);
  }
});

test("PL-06: rutas ausentes, archivos, duplicados y el tope no mutan la lista buena", async ({
  page,
}) => {
  test.setTimeout(300_000);
  const fixture = requiredProjectsFixture();
  await signIn(page);
  const found = await readProjects(page);
  const before = found.map((row) => row.path);
  // Cada rechazo que este caso provoca deja su 400 en la consola: es lo que se mide.
  guard?.expected.push("/api/settings");
  try {
    await screenProjects(page);
    await addProject(page, fixture.alfa);
    await expect(page.getByText("Proyecto añadido.")).toBeVisible();
    const good = [...before, fixture.alfa];
    await expect.poll(async () => (await readProjects(page)).map((row) => row.path)).toEqual(good);

    // Tres negativos reales: la carpeta buena sigue intacta y la pantalla lo dice.
    await addProject(page, `${fixture.alfa}/no-existe-jamas`);
    await expectRejected(page, "una ruta ausente", good);
    await addProject(page, fixture.file);
    await expectRejected(page, "un archivo en lugar de carpeta", good);
    await addProject(page, `${fixture.alfa}/`);
    await expectRejected(page, "un duplicado normalizado", good);

    // Recuperación: con una ruta válida la lista vuelve a crecer.
    await addProject(page, fixture.beta);
    await expect(page.getByText("Proyecto añadido.")).toBeVisible();
    await expect
      .poll(async () => (await readProjects(page)).map((row) => row.path))
      .toEqual([...good, fixture.beta]);

    // Tope real: 100 carpetas propias registradas por el API y la interfaz intenta la 101.
    const bulk = bulkPaths(fixture.bulk);
    const prepared = await page.request.post("/api/settings", {
      data: { projects: bulk.map((path) => ({ path })) },
    });
    expect(prepared.status(), "preparar 100 carpetas propias").toBe(200);
    expect(await screenProjects(page)).toHaveLength(100);
    await addProject(page, fixture.alfa);
    await expectRejected(page, "el proyecto 101", bulk);
  } finally {
    await restoreProjects(page, found);
  }
});
