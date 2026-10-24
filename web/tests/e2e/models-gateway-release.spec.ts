import { expect, test } from "@playwright/test";
import { BASE, requireRelease, signIn, watchConsole, type ConsoleGuard } from "./helpers";

const callerKey = process.env.E2E_MODELS_CALLER_KEY;
test.use({ ignoreHTTPSErrors: process.env.E2E_TLS_SELF_SIGNED === "1" });

let consoleGuard: ConsoleGuard;

test.beforeAll(() => {
  requireRelease();
  if (new URL(BASE!).protocol !== "https:")
    throw new Error("The Models gateway release flow requires its actual HTTPS origin.");
  if (!callerKey)
    throw new Error("Set E2E_MODELS_CALLER_KEY for the separately authorized test gateway.");
});

test.beforeEach(({ page }) => {
  consoleGuard = watchConsole(page);
});

test.afterEach(() => {
  expect(consoleGuard.errors, "consola del navegador").toEqual([]);
});

test("HTTPS conserva auth separada y entrega un stream largo con uso real", async ({ page, context }, testInfo) => {
  await signIn(page);
  const sessionCookies = (await context.cookies(BASE!)).filter((cookie) => cookie.httpOnly);
  expect(sessionCookies.length).toBeGreaterThan(0);
  expect(sessionCookies.every((cookie) => cookie.secure)).toBe(true);
  consoleGuard.expected.push("/v1/responses");
  const rejected = await page.evaluate(async () => {
    const body = JSON.stringify({ model: "gpt-6-luna", input: "No ejecutar", stream: true });
    const cookieOnly = await fetch("/v1/responses", {
      method: "POST", headers: { "Content-Type": "application/json" }, body,
    });
    const wrongKey = await fetch("/v1/responses", {
      method: "POST", headers: { "Content-Type": "application/json", Authorization: "Bearer not-a-test-key" }, body,
    });
    return { cookieOnly: cookieOnly.status, wrongKey: wrongKey.status };
  });
  expect(rejected).toEqual({ cookieOnly: 401, wrongKey: 401 });

  const result = await page.evaluate(async (key) => {
    const started = performance.now();
    const response = await fetch("/v1/responses", {
      method: "POST",
      headers: { "Content-Type": "application/json", Authorization: `Bearer ${key}` },
      body: JSON.stringify({
        model: "gpt-6-luna", stream: true, store: false, reasoning: { effort: "xhigh" },
        input: "Escribe los enteros del 1 al 600, exactamente uno por línea, sin código, explicación, herramientas ni abreviaturas.",
      }),
      signal: AbortSignal.timeout(50_000),
    });
    if (!response.ok || !response.body)
      return { status: response.status, completed: false };
    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let body = "", reads = 0, firstChunkMs = 0, textBeforeTerminalChunk = false;
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      if (++reads === 1) firstChunkMs = performance.now() - started;
      if (body.length + value.byteLength > 1_048_576) throw new Error("Stream test size limit exceeded");
      const chunk = decoder.decode(value, { stream: true });
      if (chunk.includes("response.output_text.delta") && !chunk.includes("response.completed"))
        textBeforeTerminalChunk = true;
      body += chunk;
    }
    body += decoder.decode();
    const events: { type: string; delta?: string; response?: { model?: string; status?: string; usage?: { input_tokens?: number; output_tokens?: number; total_tokens?: number }; output?: { content?: { type: string; text?: string }[] }[] } }[] = body
      .split("\n").filter((line) => line.startsWith("data: ") && line !== "data: [DONE]")
      .map((line) => JSON.parse(line.slice(6)));
    const completed = events.find((event) => event.type === "response.completed")?.response;
    const output = events.filter((event) => event.type === "response.output_text.delta").map((event) => event.delta ?? "").join("").trim();
    const completedText = completed?.output?.flatMap((item) => item.content ?? []).filter((item) => item.type === "output_text").map((item) => item.text ?? "").join("").trim();
    return {
      status: response.status, contentType: response.headers.get("content-type"), reads,
      firstChunkMs: Math.round(firstChunkMs), durationMs: Math.round(performance.now() - started),
      textBeforeTerminalChunk, completed: completed?.status === "completed", model: completed?.model,
      outputLength: output.length, streamMatchesCompleted: output === completedText,
      usage: completed?.usage ? {
        input: completed.usage.input_tokens, output: completed.usage.output_tokens, total: completed.usage.total_tokens,
      } : null,
    };
  }, callerKey!);
  await testInfo.attach("real-long-stream.json", { body: JSON.stringify(result, null, 2), contentType: "application/json" });
  expect(result.status).toBe(200);
  expect(result.contentType).toContain("text/event-stream");
  expect(result.completed).toBe(true);
  expect(result.model).toBe("gpt-6-luna");
  expect(result.outputLength).toBeGreaterThan(1_000);
  expect(result.streamMatchesCompleted).toBe(true);
  expect(result.reads).toBeGreaterThan(1);
  expect(result.textBeforeTerminalChunk).toBe(true);
  expect(result.firstChunkMs).toBeLessThan(result.durationMs!);
  expect(result.usage?.input).toBeGreaterThan(0);
  expect(result.usage?.output).toBeGreaterThan(0);
  expect(result.usage?.total).toBe(result.usage!.input! + result.usage!.output!);
});

test("cancelar desde Chromium interrumpe una respuesta real y conserva el servidor", async ({ page }, testInfo) => {
  await signIn(page);
  const result = await page.evaluate(async (key) => {
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 40_000);
    const started = performance.now();
    let sawText = false, completed = false, aborted = false, reads = 0, pending = "";
    try {
      const response = await fetch("/v1/responses", {
        method: "POST",
        headers: { "Content-Type": "application/json", Authorization: `Bearer ${key}` },
        body: JSON.stringify({
          model: "gpt-6-luna", stream: true, store: false, reasoning: { effort: "xhigh" },
          input: "Escribe una lista de 5000 enteros consecutivos empezando en 1, uno por línea, sin herramientas, código, explicación ni abreviar.",
        }),
        signal: controller.signal,
      });
      if (!response.ok || !response.body) return { status: response.status, sawText, completed, aborted };
      const reader = response.body.getReader(), decoder = new TextDecoder();
      try {
        for (;;) {
          const { done, value } = await reader.read();
          if (done) break;
          reads++;
          pending += decoder.decode(value, { stream: true });
          if (pending.length > 1_048_576) throw new Error("Cancellation test size limit exceeded");
          const lines = pending.split("\n");
          pending = lines.pop()!;
          for (const line of lines) {
            if (!line.startsWith("data: ") || line === "data: [DONE]") continue;
            const event: { type: string; delta?: string } = JSON.parse(line.slice(6));
            if (event.type === "response.completed") completed = true;
            if (event.type === "response.output_text.delta" && event.delta) sawText = true;
          }
          if (sawText && !completed) controller.abort();
        }
      } catch (error) {
        if (error instanceof DOMException && error.name === "AbortError") aborted = true;
        else throw error;
      }
      const bootstrap = await fetch("/api/bootstrap");
      return { status: response.status, sawText, completed, aborted, reads, bootstrap: bootstrap.status, durationMs: Math.round(performance.now() - started) };
    } finally {
      clearTimeout(timeout);
    }
  }, callerKey!);
  await testInfo.attach("real-cancellation.json", { body: JSON.stringify(result, null, 2), contentType: "application/json" });
  expect(result.status).toBe(200);
  expect(result.sawText).toBe(true);
  expect(result.completed).toBe(false);
  expect(result.aborted).toBe(true);
  expect(result.bootstrap).toBe(200);
});
