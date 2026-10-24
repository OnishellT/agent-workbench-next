import { defineConfig } from "@playwright/test";

// One runner for the whole web app (shared browser run owner: coordinator).
// Tests live in web/tests/e2e so the single web/node_modules resolution applies.
// No default target: browser specs require E2E_BASE_URL and fail loudly without it,
// so a run can never navigate to another user's service. Contract specs need no target.
// PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH points at a host-provided Chromium (optional).
const chromium = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH;

export default defineConfig({
  testDir: "./tests/e2e",
  timeout: 60_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  workers: 1,
  reporter: [["list"], ["html", { open: "never", outputFolder: "./tests/e2e/report" }]],
  use: {
    baseURL: process.env.E2E_BASE_URL,
    launchOptions: chromium ? { executablePath: chromium } : undefined,
    trace: "off",
    video: "off",
    screenshot: "only-on-failure",
  },
});
