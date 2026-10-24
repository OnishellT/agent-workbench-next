import { afterEach, test } from "node:test";
import assert from "node:assert/strict";
import { installMagpie } from "../../src/modules/hosts/client";

const originalFetch = globalThis.fetch;
afterEach(() => { globalThis.fetch = originalFetch; });

test("a 202 installation waits for actual Magpie readiness", async () => {
  const calls: string[] = [];
  globalThis.fetch = (async (path: string, options: RequestInit) => {
    calls.push(`${options.method} ${path}`);
    const ready = calls.length === 3;
    return Response.json({ installed: true, ready, revision: 4 }, { status: calls.length === 1 ? 202 : 200 });
  }) as typeof fetch;
  assert.equal((await installMagpie("new-host", 4)).ready, true);
  assert.deepEqual(calls, [
    "POST /api/hosts/new-host/magpie/install",
    "GET /api/hosts/new-host/magpie/status",
    "GET /api/hosts/new-host/magpie/status",
  ]);
});

test("an already ready actor needs no extra status requests", async () => {
  let calls = 0;
  globalThis.fetch = (async () => {
    calls++;
    return Response.json({ ready: true, revision: 4 });
  }) as typeof fetch;
  await installMagpie("existing", 4);
  assert.equal(calls, 1);
});

test("changing host identity during startup cannot produce success", async () => {
  let calls = 0;
  globalThis.fetch = (async () => Response.json({
    ready: ++calls > 1, revision: calls === 1 ? 4 : 5,
  })) as typeof fetch;
  await assert.rejects(installMagpie("changed", 4), /identidad del host cambió/);
});

test("SSH or authentication errors during readiness stay visible", async () => {
  let calls = 0;
  globalThis.fetch = (async () => ++calls === 1
    ? Response.json({ ready: false, revision: 4 }, { status: 202 })
    : Response.json({ error: "SSH unavailable" }, { status: 503 })) as typeof fetch;
  await assert.rejects(installMagpie("offline", 4), /SSH unavailable/);
});
