import { expect, type Page } from "@playwright/test";
import { readFileSync } from "node:fs";

/** Shared preconditions of the real-release browser flows (no mocks). Every spec
 *  that imports this needs E2E_BASE_URL pointing at the release under test and, when
 *  that instance asks for one, E2E_ACCESS_TOKEN from its launcher — or, when the token
 *  must not travel in the launcher's command line, E2E_ACCESS_TOKEN_FILE, read here
 *  inside the test process and never printed. */
export const BASE = process.env.E2E_BASE_URL;
export const TOKEN = process.env.E2E_ACCESS_TOKEN ?? tokenFromFile();

function tokenFromFile() {
  const path = process.env.E2E_ACCESS_TOKEN_FILE;
  return path ? readFileSync(path, "utf8").trim() : undefined;
}

export function requireRelease() {
  if (!BASE)
    throw new Error(
      "E2E_BASE_URL is not set: point it at the real release under test (no default target, no mock server).",
    );
}

/** Console guard: collects page errors and failed requests, ignoring the expected
 *  401 of a fresh tab and any URL the case itself registered as an expected failure. */
export interface ConsoleGuard {
  errors: string[];
  /** URL fragments of failures the case triggers on purpose (4xx/5xx + offline). */
  expected: string[];
}

export function watchConsole(page: Page): ConsoleGuard {
  const errors: string[] = [];
  const expected: string[] = [];
  page.on("console", (message) => {
    if (message.type() !== "error") return;
    const url = message.location().url;
    if (url.includes("/api/bootstrap")) return;
    // A resource error reports the page as its location in some browsers, so the case's own
    // expected fragments are matched against the message text too.
    if (expected.some((fragment) => url.includes(fragment) || message.text().includes(fragment)))
      return;
    errors.push(`${message.text()} @ ${url}`);
  });
  page.on("pageerror", (error) => errors.push(String(error)));
  return { errors, expected };
}

/** Entry route matters: a case that needs a section's chunk NOT loaded yet must land on a
 *  screen that does not mount it (the shell only loads the section it renders). */
export async function signIn(page: Page, path = "/") {
  await page.goto(path);
  const form = page.locator("#access-token");
  const shell = page.getByRole("navigation", { name: "Navegación principal" });
  await expect(form.or(shell).first()).toBeVisible();
  if (!(await form.isVisible())) return;
  if (!TOKEN)
    throw new Error("This instance asks for a token: set E2E_ACCESS_TOKEN from its launcher.");
  await form.fill(TOKEN);
  await page.getByRole("button", { name: "Entrar", exact: true }).click();
  await expect(shell).toBeVisible();
}

/** Folder that really exists on the instance under test, prepared by the
 *  coordinator. The browser cannot create directories there, so a missing or
 *  relative value fails loudly instead of inventing a path. */
export function requiredProjectDir() {
  const dir = process.env.E2E_PROJECT_DIR;
  if (!dir)
    throw new Error(
      "E2E_PROJECT_DIR is not set: point it at a project folder prepared on the server under test (no default, no fallback).",
    );
  if (!dir.startsWith("/") || dir.endsWith("/") || dir.length < 2)
    throw new Error(
      `E2E_PROJECT_DIR must be an absolute server path without a trailing slash, got "${dir}".`,
    );
  return dir;
}

/** A house card located by its own heading, so a control is never picked by position. */
export function cardByHeading(page: Page, heading: string) {
  return page
    .locator('[data-slot="card"]')
    .filter({ has: page.getByRole("heading", { name: heading, exact: true }) });
}

export interface ServerSettings {
  capture?: boolean;
  projects?: { path: string; name: string }[];
}

/** Authoritative state: the same body the screens must reflect. */
export async function serverSettings(page: Page): Promise<ServerSettings> {
  const response = await page.request.get("/api/bootstrap");
  expect(response.status()).toBe(200);
  const body: unknown = await response.json();
  if (!body || typeof body !== "object" || !("settings" in body) || !body.settings)
    throw new Error("bootstrap did not report settings for this slice");
  return body.settings as ServerSettings;
}

/** POST /api/settings straight from the test: used only to restore the state the
 *  case found (merged with what the instance reports now), never to prove that the
 *  screen writes it. */
export async function restoreSettings(page: Page, patch: ServerSettings) {
  const current = await serverSettings(page);
  const response = await page.request.post("/api/settings", {
    data: { ...current, ...patch },
  });
  expect([200, 201]).toContain(response.status());
}
