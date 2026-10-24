import { readFile } from "node:fs/promises";
import { homedir } from "node:os";
import { join } from "node:path";

/** Where the Workbench binary writes its token by default (internal/config: defaultStateDir). */
export function defaultTokenFile(env: NodeJS.ProcessEnv = process.env, home = homedir()): string {
	const state = env.XDG_STATE_HOME || join(home, ".local", "state");
	return join(state, "agent-workbench-next", "access.token");
}

export interface WorkbenchConfig {
	/** Must equal the server's --public-url: the API checks Origin on mutations. */
	baseUrl: string;
	tokenFile: string;
}

export function resolveConfig(env: NodeJS.ProcessEnv = process.env): WorkbenchConfig {
	const raw = (env.WORKBENCH_URL || "http://127.0.0.1:18888").replace(/\/+$/, "");
	return { baseUrl: raw, tokenFile: env.WORKBENCH_TOKEN_FILE || defaultTokenFile(env) };
}

export interface ApiResponse {
	status: number;
	body: unknown;
}

/** True when the path stays under /api/ or /foundry/api/ (or is /health) after percent-decoding, with no
 *  "." or ".." segment at any decoding depth (so %2e%2e and %252e%252e are refused too). */
export function isSafePath(path: string): boolean {
	let current = path.split(/[?#]/, 1)[0] ?? "";
	for (let depth = 0; depth < 4; depth++) {
		if (current.includes("\\") || current.includes("\0")) return false;
		if (!/^\/(api\/|foundry\/api\/|health$)/.test(current)) return false;
		if (current.split("/").some((segment) => segment === "." || segment === "..")) return false;
		let decoded: string;
		try {
			decoded = decodeURIComponent(current);
		} catch {
			return false;
		}
		if (decoded === current) return true;
		current = decoded;
	}
	return false;
}

const METHODS = new Set(["GET", "POST", "PUT", "DELETE"]);

/**
 * Minimal session client. The token is read from disk only to log in and is never
 * returned to callers; requests carry the random session cookie Workbench issues.
 */
export class WorkbenchClient {
	private cookie = "";
	private loginInFlight: Promise<void> | null = null;
	constructor(
		readonly config: WorkbenchConfig,
		private readonly fetchImpl: typeof fetch = fetch,
	) {}

	/** Concurrent callers share one login so they never race on the session cookie. */
	login(): Promise<void> {
		this.loginInFlight ??= this.doLogin().finally(() => {
			this.loginInFlight = null;
		});
		return this.loginInFlight;
	}

	private async doLogin(): Promise<void> {
		let token: string;
		try {
			token = (await readFile(this.config.tokenFile, "utf8")).trim();
		} catch {
			throw new Error(`Workbench token file not readable: ${this.config.tokenFile} (set WORKBENCH_TOKEN_FILE)`);
		}
		const response = await this.fetchImpl(`${this.config.baseUrl}/api/auth`, {
			method: "POST",
			headers: { "Content-Type": "application/json", Origin: this.config.baseUrl },
			body: JSON.stringify({ token }),
		});
		if (response.status !== 200) throw new Error(`Workbench login failed: HTTP ${response.status}`);
		const setCookie = response.headers.get("set-cookie") ?? "";
		const pair = setCookie.split(";")[0]?.trim() ?? "";
		if (!pair.includes("=")) throw new Error("Workbench login returned no session cookie");
		this.cookie = pair;
	}

	async request(method: string, path: string, body?: unknown): Promise<ApiResponse> {
		const verb = method.toUpperCase();
		if (!METHODS.has(verb)) throw new Error(`Unsupported method ${method}`);
		if (!isSafePath(path)) {
			throw new Error("path must start with /api/ or /foundry/api/ (or be /health)");
		}
		if (!this.cookie) await this.login();
		let response = await this.send(verb, path, body);
		if (response.status === 401) {
			// The session lives in server memory: a restart invalidates it, so log in once more.
			await this.login();
			response = await this.send(verb, path, body);
		}
		const text = await response.text();
		let parsed: unknown = text;
		try {
			parsed = text ? JSON.parse(text) : null;
		} catch {
			/* non-JSON body stays as text */
		}
		return { status: response.status, body: parsed };
	}

	private send(verb: string, path: string, body: unknown) {
		const headers: Record<string, string> = { Cookie: this.cookie };
		if (verb !== "GET") headers.Origin = this.config.baseUrl;
		if (body !== undefined) headers["Content-Type"] = "application/json";
		return this.fetchImpl(`${this.config.baseUrl}${path}`, {
			method: verb,
			headers,
			body: body === undefined ? undefined : JSON.stringify(body),
		});
	}
}

/** One-line summary of a request body for the approval prompt: top-level keys with
 *  short values, secret-looking fields masked, bounded in length. */
export function summarizeBody(body: unknown, limit = 300): string {
	if (body === undefined || body === null) return "";
	let text: string;
	if (typeof body === "object" && !Array.isArray(body)) {
		text = Object.entries(body as Record<string, unknown>)
			.map(([key, value]) => {
				if (/pass|token|secret|key|credential/i.test(key)) return `${key}=***`;
				const shown = typeof value === "string" ? value : JSON.stringify(value);
				return `${key}=${shown.length > 60 ? `${shown.slice(0, 60)}…` : shown}`;
			})
			.join(", ");
	} else {
		text = JSON.stringify(body) ?? "";
	}
	return `\nBody: ${text.length > limit ? `${text.slice(0, limit)}…` : text}`;
}
