import { expect, test } from "@playwright/test";
import { mkdir, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { requireRelease, requiredProjectDir, signIn } from "./helpers";

// Las conversaciones nuevas llegan desde fuentes nativas habilitadas, sin pulsar
// Sincronizar (Work está retirado: no hay sesión Work que abrir).
test.beforeAll(requireRelease);
test("captura una fuente habilitada automáticamente y conserva citas al recapturar", async ({ page }) => {
  test.setTimeout(180_000);
  const id = `native-auto-${Date.now()}`;
  const source = join(requiredProjectDir(), id);
  await mkdir(source, { mode: 0o700 });
  await writeFile(join(source, "conversation.jsonl"), JSON.stringify({
    session_id: id, role: "user", kind: "message", text: "evidencia de ensayo para captura automática",
  }) + "\n", { mode: 0o600 });
  await signIn(page, "/?section=conversations&view=sources");
  const created = await page.request.post("/api/conversations/sources", { data: { path: source, adapter: "canonical" } });
  expect(created.status()).toBe(201);
  const { source: registered } = await created.json();
  try {
    await expect.poll(async () => {
      const response = await page.request.get(`/api/conversations/${id}`);
      return response.status();
    }, { timeout: 120_000, intervals: [1000] }).toBe(200);
    const first = await (await page.request.get(`/api/conversations/${id}`)).json();
    expect(first.conversation.source_id).toBe(registered.id);
    expect(first.conversation.host_id).toBe("local");
    const revision = first.conversation.revision;
    expect((await (await page.request.get(`/api/conversations/${id}/revisions/${revision}`)).json()).events.some((event: { text: string }) => event.text.includes("captura automática"))).toBe(true);
    const scan = await page.request.post(`/api/conversations/sources/${registered.id}/scan`);
    expect([200, 202]).toContain(scan.status());
    await expect.poll(async () => (await (await page.request.get(`/api/conversations/${id}`)).json()).conversation.revision).toBe(revision);
    const paused = await page.request.patch(`/api/conversations/sources/${registered.id}`, { data: { enabled: false } });
    expect(paused.status()).toBe(200);
  } finally {
    await page.request.patch(`/api/conversations/sources/${registered.id}`, { data: { enabled: false } });
  }
});
