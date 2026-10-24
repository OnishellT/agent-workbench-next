import { expect, test } from "@playwright/test";
import { requireRelease, signIn } from "./helpers";

test("un chunk desconectado no derriba el shell y reintentar no repite acciones", async ({ page, context }) => {
  requireRelease();
  const pageErrors: string[] = [];
  const mutations: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(String(error)));
  page.on("request", (request) => {
    if (request.method() === "POST" && /\/api\/(settings|work|terminal)(\/|$)/.test(new URL(request.url()).pathname))
      mutations.push(request.url());
  });
  // Enters on Ajustes on purpose: Conversaciones' chunk must not be loaded yet, or going
  // offline could not disconnect it (Conversaciones is the landing section of the shell).
  await signIn(page, "/?section=settings");
  await expect(page.getByRole("heading", { name: "Proyectos", exact: true })).toBeVisible();

  await context.setOffline(true);
  await page.getByRole("button", { name: "Conversaciones", exact: true }).click();
  const failure = page.getByRole("alert").filter({ hasText: "No se pudo abrir esta sección." });
  await expect(failure).toBeVisible();
  await expect(page.getByRole("navigation", { name: "Navegación principal" })).toBeVisible();
  await page.getByRole("button", { name: "Ajustes", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Proyectos", exact: true })).toBeVisible();

  await context.setOffline(false);
  await page.getByRole("button", { name: "Conversaciones", exact: true }).click();
  await expect(failure).toBeVisible();
  await failure.getByRole("button", { name: "Reintentar", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Conversaciones", level: 1, exact: true })).toBeVisible();
  await expect(page).toHaveURL(/section=conversations/);
  expect(mutations).toEqual([]);
  expect(pageErrors).toEqual([]);
});
