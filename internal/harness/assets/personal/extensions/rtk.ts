// Official RTK Pi adapter, profile-scoped for OMP personal.
// Source: https://github.com/rtk-ai/rtk/blob/develop/hooks/pi/rtk.ts
// Requires rtk >= 0.23.0 on PATH. All failures pass through unchanged.
import type {
  BashToolCallEvent,
  ExtensionAPI,
  ToolCallEvent,
} from "@earendil-works/pi-coding-agent"

const REWRITE_TIMEOUT_MS = 2_000
const MIN_SUPPORTED_RTK_MINOR = 23

function isBashToolCallEvent(event: ToolCallEvent): event is BashToolCallEvent {
  return event.toolName === "bash"
}

function parseSemver(raw: string): [number, number, number] | null {
  const match = raw.trim().match(/(\d+)\.(\d+)\.(\d+)/)
  if (!match) return null
  return [
    Number.parseInt(match[1], 10),
    Number.parseInt(match[2], 10),
    Number.parseInt(match[3], 10),
  ]
}

async function rewriteCommand(
  pi: ExtensionAPI,
  command: string,
  signal?: AbortSignal,
): Promise<string | null> {
  const result = await pi.exec("rtk", ["rewrite", command], {
    timeout: REWRITE_TIMEOUT_MS,
    signal,
  })
  if (result.killed) return null
  if (result.code !== 0 && result.code !== 3) return null
  return result.stdout.trim() || null
}

export default async function (pi: ExtensionAPI) {
  const version = await pi.exec("rtk", ["--version"], {
    timeout: REWRITE_TIMEOUT_MS,
  })
  if (version.code !== 0) {
    console.warn("[rtk] binary not found in PATH; extension disabled")
    return
  }

  const parsed = parseSemver(version.stdout.replace(/^rtk\s+/, ""))
  if (parsed) {
    const [major, minor] = parsed
    if (major === 0 && minor < MIN_SUPPORTED_RTK_MINOR) {
      console.warn(`[rtk] ${version.stdout.trim()} is too old; need >= 0.23.0`)
      return
    }
  }

  pi.on("tool_call", async (event, context) => {
    try {
      if (!isBashToolCallEvent(event)) return
      const command = event.input.command
      if (typeof command !== "string" || command.trim() === "") return
      if (command.startsWith("rtk ") || process.env.RTK_DISABLED === "1") return

      const rewritten = await rewriteCommand(pi, command, context.signal)
      if (rewritten && rewritten !== command) event.input.command = rewritten
    } catch (error) {
      console.warn("[rtk] rewrite failed; passing command through unchanged", error)
    }
  })
}
