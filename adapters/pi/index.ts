import { Type } from "@earendil-works/pi-ai";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { resolveConfig, summarizeBody, WorkbenchClient } from "./client.ts";

const TOOL = "workbench_api";

const ENDPOINTS = `Workbench HTTP API (all paths under /api, JSON):
- GET /api/bootstrap, GET /api/diagnostics, GET /health
- Hosts: GET /api/hosts, POST /api/hosts {name,address,user,port,auth,...}, PUT /api/hosts/{id},
  POST /api/hosts/{id}/trust, GET /api/hosts/{id}/magpie/status, POST /api/hosts/{id}/magpie/install,
  GET|POST /api/hosts/{id}/dependencies, GET /api/hosts/{id}/history, POST /api/hosts/{id}/history/sync,
  GET /api/hosts/{id}/conversations, GET /api/network/wireguard
- Conversations: GET /api/conversations, GET|POST /api/conversations/sources, GET /api/conversations/{id}
- Tasks: GET /api/tasks, /api/tasks/config, POST /api/tasks/proposals, /api/tasks/{id}/(label|completion|feedback)
- Foundry (conversation learning loop, own API under /foundry/api/): GET /foundry/api/state?compact=1,
  GET /foundry/api/session?id=…, GET /foundry/api/episodes?session_id=…, GET /foundry/api/candidate/readiness?id=…,
  POST /foundry/api/action {action,...} (scan, task/label/completion decisions, derive, evaluate, adopt are the
  user's: ask before any POST). Adoption of a learned capability is always a human decision.
Mutations (POST/PUT/DELETE) ask the user for approval. Never pass secrets in bodies you did not get from the user.`;

export default function (pi: ExtensionAPI) {
	const client = new WorkbenchClient(resolveConfig());

	pi.registerTool({
		name: TOOL,
		label: "Workbench API",
		description: "Call the local Agent Workbench dashboard API (hosts, conversations, tasks, Foundry, diagnostics).",
		promptSnippet: ENDPOINTS,
		namespace: { name: "workbench", description: "Agent Workbench dashboard", instructions: ENDPOINTS },
		annotations: { readOnlyHint: false, destructiveHint: true, openWorldHint: false },
		parameters: Type.Object({
			method: Type.Union([Type.Literal("GET"), Type.Literal("POST"), Type.Literal("PUT"), Type.Literal("DELETE")]),
			path: Type.String({ description: "Path starting with /api/ (or /foundry/api/ for Foundry), e.g. /api/hosts" }),
			body: Type.Optional(Type.Unknown({ description: "JSON body for POST/PUT" })),
		}),
		async execute(_id, params) {
			const response = await client.request(params.method, params.path, params.body);
			const text = typeof response.body === "string" ? response.body : JSON.stringify(response.body, null, 1);
			const shown = text.length > 20000 ? `${text.slice(0, 20000)}\n…(truncated; narrow the query)` : text;
			return {
				content: [{ type: "text", text: `HTTP ${response.status}\n${shown}` }],
				details: { status: response.status },
				isError: response.status >= 400,
			};
		},
	});

	// Mutations go through the user, like Codex tool approval: the dashboard installs
	// software on hosts and adopts rules, so a model must not do that unattended.
	pi.on("tool_call", async (event, ctx) => {
		if (event.toolName !== TOOL) return;
		const input = event.input as { method?: string; path?: string; body?: unknown };
		if ((input.method ?? "GET").toUpperCase() === "GET") return;
		if (!ctx.hasUI) return { block: true, reason: "Workbench mutations need an interactive session to approve them" };
		const ok = await ctx.ui.confirm("Workbench", `Allow ${input.method} ${input.path}?${summarizeBody(input.body)}`);
		if (!ok) return { block: true, reason: `${input.method} ${input.path} was not approved` };
	});

	pi.registerCommand("workbench", {
		description: "Workbench dashboard: /workbench status | open | login",
		getArgumentCompletions: (prefix) =>
			["status", "open", "login"].filter((a) => a.startsWith(prefix)).map((value) => ({ value, label: value })),
		handler: async (args, ctx) => {
			const action = args.trim() || "status";
			try {
				if (action === "open") {
					ctx.ui.notify(client.config.baseUrl, "info");
				} else if (action === "login") {
					await client.login();
					ctx.ui.notify(`Logged in to ${client.config.baseUrl}`, "info");
				} else if (action === "status") {
					const health = await client.request("GET", "/health");
					const body = health.body as { status?: string; version?: string; modules?: Record<string, string> };
					const modules = Object.entries(body.modules ?? {})
						.filter(([, state]) => state !== "mounted")
						.map(([name, state]) => `${name}=${state}`);
					ctx.ui.notify(
						`${client.config.baseUrl}: ${body.status ?? health.status} (${body.version ?? "?"})${modules.length ? ` · ${modules.join(", ")}` : ""}`,
						body.status === "ok" ? "info" : "warning",
					);
				} else {
					ctx.ui.notify("Usage: /workbench status | open | login", "warning");
				}
			} catch (error) {
				ctx.ui.notify(error instanceof Error ? error.message : String(error), "error");
			}
		},
	});
}
