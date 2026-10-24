import { afterEach, test } from "node:test";
import assert from "node:assert/strict";
import { ApiError } from "../../src/app/api";
import { runExample } from "../../src/modules/conversations/client";

const originalFetch = globalThis.fetch;
afterEach(() => { globalThis.fetch = originalFetch; });

function answer(body: unknown, status: number) {
  globalThis.fetch = (async () => Response.json(body, { status })) as typeof fetch;
}

test("a 200 receipt with checks is succeeded", async () => {
  answer({ checks: [{ name: "a", ok: true }] }, 200);
  assert.equal((await runExample()).status, "succeeded");
});

test("a 500 receipt with checks is shown as failed, not thrown", async () => {
  answer({ checks: [{ name: "a", ok: false }] }, 500);
  assert.equal((await runExample()).status, "failed");
});

test("a 500 error body without checks is a rejected call with the server message", async () => {
  answer({ error: "boom" }, 500);
  await assert.rejects(runExample(), (e: unknown) => e instanceof ApiError && e.message === "boom" && e.status === 500);
});

test("a connection failure maps to a coded ApiError", async () => {
  globalThis.fetch = (async () => { throw new TypeError("down"); }) as typeof fetch;
  await assert.rejects(runExample(), (e: unknown) => e instanceof ApiError && e.code === "connection");
});
