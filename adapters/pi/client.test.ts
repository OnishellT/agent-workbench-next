import { expect, test } from "bun:test";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { defaultTokenFile, isSafePath, summarizeBody, WorkbenchClient } from "./client.ts";

// Fake Workbench: /api/auth issues a session that is not the token; a restart
// (invalidate) makes the old cookie answer 401 until the client logs in again.
function fakeServer() {
	let session = "s1";
	const log: string[] = [];
	const fetchImpl: typeof fetch = async (input, init) => {
		const url = new URL(String(input));
		const headers = new Headers(init?.headers);
		log.push(`${init?.method} ${url.pathname}`);
		if (url.pathname === "/api/auth") {
			const { token } = JSON.parse(String(init?.body));
			if (token !== "secret-token-value-0123456789abcdef" || headers.get("origin") !== url.origin) {
				return new Response("{}", { status: 401 });
			}
			return new Response('{"ok":true}', { status: 200, headers: { "set-cookie": `wb=${session}; Path=/; HttpOnly` } });
		}
		if (headers.get("cookie") !== `wb=${session}`) return new Response('{"error":"authentication_required"}', { status: 401 });
		if (init?.method !== "GET" && headers.get("origin") !== url.origin) return new Response("{}", { status: 403 });
		return new Response(JSON.stringify({ path: url.pathname, body: init?.body ?? null }), { status: 200 });
	};
	return { fetchImpl, log, invalidate: () => (session = "s2") };
}

function tokenFile() {
	const dir = mkdtempSync(join(tmpdir(), "wb-pi-"));
	const file = join(dir, "access.token");
	writeFileSync(file, "secret-token-value-0123456789abcdef\n");
	return file;
}

test("logs in lazily, re-logs in after a server restart, and sends Origin on mutations", async () => {
	const server = fakeServer();
	const client = new WorkbenchClient({ baseUrl: "http://127.0.0.1:18888", tokenFile: tokenFile() }, server.fetchImpl);
	expect((await client.request("GET", "/api/hosts")).status).toBe(200);
	server.invalidate();
	const after = await client.request("POST", "/api/hosts", { name: "x" });
	expect(after.status).toBe(200);
	expect((after.body as { body: string }).body).toBe('{"name":"x"}');
	expect(server.log).toEqual(["POST /api/auth", "GET /api/hosts", "POST /api/hosts", "POST /api/auth", "POST /api/hosts"]);
});

test("refuses paths outside /api and unknown methods, and a missing token file", async () => {
	const server = fakeServer();
	const client = new WorkbenchClient({ baseUrl: "http://127.0.0.1:18888", tokenFile: tokenFile() }, server.fetchImpl);
	await expect(client.request("GET", "/v1/models")).rejects.toThrow("/api/");
	await expect(client.request("GET", "/api/../etc")).rejects.toThrow("/api/");
	await expect(client.request("PATCH", "/api/hosts")).rejects.toThrow("Unsupported");
	const missing = new WorkbenchClient({ baseUrl: "http://127.0.0.1:18888", tokenFile: "/nonexistent/token" }, server.fetchImpl);
	await expect(missing.request("GET", "/api/hosts")).rejects.toThrow("WORKBENCH_TOKEN_FILE");
	expect(defaultTokenFile({ XDG_STATE_HOME: "/s" }, "/h")).toBe("/s/agent-workbench-next/access.token");
	expect(defaultTokenFile({}, "/h")).toBe("/h/.local/state/agent-workbench-next/access.token");
});

test("path check decodes before looking for dot segments", () => {
	for (const bad of ["/api/%2e%2e/etc", "/api/%2E%2E/x", "/api/%252e%252e/x", "/api/a/../b", "/api/a%2f..%2fb", "/api/./x", "/api/%zz", "/v1/x", "/api\\..\\x"]) {
		expect(isSafePath(bad)).toBe(false);
	}
	for (const good of ["/api/hosts", "/api/hosts/a%20b/history?x=..", "/health", "/foundry/api/state?compact=1", "/foundry/api/action"]) expect(isSafePath(good)).toBe(true);
	// Foundry's UI and package files are not API: only /foundry/api/ is reachable.
	for (const bad of ["/foundry/", "/foundry/health", "/foundry/assets/x.js", "/foundry/api/../x", "/foundry/api/%2e%2e/x", "/foundryx/api/x"]) expect(isSafePath(bad)).toBe(false);
});

test("approval summary masks secrets and bounds length", () => {
	expect(summarizeBody(undefined)).toBe("");
	const text = summarizeBody({ name: "h", password: "hunter2", note: "x".repeat(200) });
	expect(text).toContain("name=h");
	expect(text).toContain("password=***");
	expect(text).not.toContain("hunter2");
	expect(text.length).toBeLessThan(400);
});

test("concurrent first requests share a single login", async () => {
	const server = fakeServer();
	const client = new WorkbenchClient({ baseUrl: "http://127.0.0.1:18888", tokenFile: tokenFile() }, server.fetchImpl);
	await Promise.all([client.request("GET", "/api/a"), client.request("GET", "/api/b")]);
	expect(server.log.filter((l) => l === "POST /api/auth")).toHaveLength(1);
});
