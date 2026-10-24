import { afterEach, test } from "node:test";
import assert from "node:assert/strict";
import { api, ApiError, API_ERROR_MESSAGES, apiResponse, translateError } from "../../src/app/api";
import { parseLaunchToken } from "../../src/app/hooks";
import { hostsSignature } from "../../src/modules/hosts/client";
import { conflict, notServed } from "../../src/modules/conversations/client";

const originalFetch = globalThis.fetch;
afterEach(() => { globalThis.fetch = originalFetch; });

test("parseLaunchToken reads the hash token and ignores other hashes", () => {
  assert.equal(parseLaunchToken("#token=abc"), "abc");
  assert.equal(parseLaunchToken("#a=1&token=xyz"), "xyz");
  assert.equal(parseLaunchToken("#other=1"), null);
  assert.equal(parseLaunchToken(""), null);
});

test("hostsSignature ignores order and volatile fields, but sees revisions", () => {
  const a = { id: "a", revision: 1, status: "ok" };
  const b = { id: "b", revision: 2 };
  assert.equal(hostsSignature([a, b]), hostsSignature([b, { ...a, status: "down" }]));
  assert.notEqual(hostsSignature([a, b]), hostsSignature([a, { ...b, revision: 3 }]));
});

test("translateError maps client codes through the translator and keeps server text", () => {
  const upper = (text: string) => text.toUpperCase();
  assert.equal(
    translateError(new ApiError("x", 0, "connection"), upper),
    API_ERROR_MESSAGES.connection.toUpperCase(),
  );
  assert.equal(translateError(new ApiError("server says no", 400), upper), "server says no");
  assert.equal(translateError("weird", upper, "fallback"), "fallback");
});

test("api carries the server code and apiResponse does not throw on error statuses", async () => {
  globalThis.fetch = (async () => Response.json({ error: "stale", code: "revision_moved" }, { status: 409 })) as typeof fetch;
  await assert.rejects(api("/x"), (e: unknown) => e instanceof ApiError && e.code === "revision_moved" && e.status === 409);
  const raw = await apiResponse<{ error: string }>("/x");
  assert.equal(raw.ok, false);
  assert.equal(raw.status, 409);
  assert.equal(raw.body.error, "stale");
});

test("a non-JSON body is an invalid_response error", async () => {
  globalThis.fetch = (async () => new Response("<html>", { status: 502 })) as typeof fetch;
  await assert.rejects(api("/x"), (e: unknown) => e instanceof ApiError && e.code === "invalid_response");
});

test("notServed and conflict classify statuses", () => {
  assert.equal(notServed(new ApiError("m", 404)), true);
  assert.equal(notServed(new ApiError("m", 500)), false);
  assert.equal(conflict(new ApiError("m", 409)), true);
  assert.equal(conflict(new Error("m")), false);
});

test("mergePage appends only rows not seen before", async () => {
  const { mergePage } = await import("../../src/modules/conversations/domain");
  const merged = mergePage([{ id: "a" }, { id: "b" }], [{ id: "b" }, { id: "c" }], (row) => row.id);
  assert.deepEqual(merged.map((row) => row.id), ["a", "b", "c"]);
  assert.deepEqual(mergePage([{ id: "a" }], undefined, (row) => row.id), [{ id: "a" }]);
});
