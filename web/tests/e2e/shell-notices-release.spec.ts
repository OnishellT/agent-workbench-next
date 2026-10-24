import { expect, request, test } from "@playwright/test";
import { BASE, cardByHeading, requireRelease, signIn, watchConsole, type ConsoleGuard } from "./helpers";

// M10 OP-12: versión y aviso real de procedencia/licencias/fuente, servido sin sesión.
//
//   E2E_BASE_URL=http://127.0.0.1:19074 E2E_ACCESS_TOKEN=... \
//     bun run test tests/e2e/shell-notices-release.spec.ts

let guard: ConsoleGuard | null = null;

test.beforeAll(requireRelease);

test.beforeEach(({ page }) => {
  guard = watchConsole(page);
});

test.afterEach(() => {
  expect(guard?.errors ?? [], "consola del navegador").toEqual([]);
  guard = null;
});

test("el release muestra su versión y abre avisos, licencias y fuente", async ({ page }) => {
  await signIn(page);
  const footer = page.locator("footer");
  const reported = await page.request.get("/api/bootstrap");
  expect(reported.status()).toBe(200);
  const boot: unknown = await reported.json();
  if (!boot || typeof boot !== "object" || !("version" in boot) || typeof boot.version !== "string")
    throw new Error("bootstrap did not report a release version for this installation");
  const version = boot.version;
  // The footer must show the identity this installation reports, not a label of its own.
  expect(version).not.toBe("");
  await expect(footer.locator("[data-release-version]")).toHaveAttribute(
    "data-release-version",
    version,
  );
  // Exact text: the shell publishes the reported identity, it does not add a label of its own.
  expect(((await footer.locator("[data-release-version]").innerText()) ?? "").trim()).toBe(
    `Agent Workbench ${version}`,
  );

  const link = footer.getByRole("link", { name: "Avisos, licencias y fuente correspondiente" });
  await expect(link).toHaveAttribute("href", "/NOTICES.txt");
  // Chromium pide /favicon.ico para un text/plain: esta instalación sólo publica el .svg.
  guard?.expected.push("/favicon.ico");
  await link.click();
  await expect(page).toHaveURL(/\/NOTICES\.txt$/);
  const notice = await page.locator("body").innerText();
  expect(notice).toContain("AGPL-3.0-only");
  expect(notice).toContain("Apache-2.0");

  // Volver deja la shell autenticada en pie: el aviso no sustituye ninguna pantalla.
  await page.goBack();
  await expect(page.getByRole("navigation", { name: "Navegación principal" })).toBeVisible();
  await page.goto("/?section=settings");
  await expect(cardByHeading(page, "Diagnóstico")).toBeVisible();
});

test("el aviso es público, íntegro y una ruta equivocada no se sirve como shell", async ({
  page,
}) => {
  await signIn(page);
  const anonymous = await request.newContext({ baseURL: BASE });
  try {
    const publicNotice = await anonymous.get("/NOTICES.txt");
    expect(publicNotice.status()).toBe(200);
    expect(publicNotice.headers()["content-type"] ?? "").toContain("text/plain");
    const notice = await publicNotice.text();
    // Per component, never one invented global license: SIM keeps Apache-2.0 with its pin,
    // Magpie keeps MIT with its source, the ported Foundry declares AGPL-3.0-only, and
    // Orbit is declared retired and not distributed.
    expect(notice).toContain("https://github.com/yetone/magpie");
    expect(notice).toContain("https://github.com/OnishellT/agent-workbench");
    expect(notice).toContain("8df65ad39a10a6c34c61ff0674e0e8a7ad2164f0");
    expect(notice).toMatch(/Orbit:[^\n]*no distribuido/);
    // The dependency texts are the ones of the exact installation, not a hand-written list.
    expect(notice).toContain("Avisos de las dependencias del paquete web que este release distribuye");
    expect(notice).toMatch(/@base-ui\/react@\d+\.\d+\.\d+\nLicense: MIT/);
    expect(notice).toContain("lucide-react@");
    // Go modules really linked into both distributed binaries, read from the module cache.
    expect(notice).toContain("avisos de las dependencias Go enlazadas");
    expect(notice).toContain("modernc.org/sqlite@");
    expect(notice).toContain("github.com/yetone/magpie");

    // The same document reaches a session: the notice is not a second, diverging copy.
    const sessionNotice = await page.request.get("/NOTICES.txt");
    expect(sessionNotice.status()).toBe(200);
    expect(await sessionNotice.text()).toBe(notice);

    // Wrong paths stay wrong: a different name and a trailing slash on the file both
    // answer the JSON error instead of the shell document.
    for (const wrong of ["/NOTICES.txt.bak", "/NOTICES.txt/", "/notices.txt"]) {
      const response = await anonymous.get(wrong);
      expect(response.status(), wrong).toBe(404);
      expect(await response.text(), wrong).toContain("not_found");
    }
    // Recovery: the valid path answers again after the rejected ones.
    const recovered = await anonymous.get("/NOTICES.txt");
    expect(recovered.status()).toBe(200);
    expect(await recovered.text()).toBe(notice);
  } finally {
    await anonymous.dispose();
  }
});
