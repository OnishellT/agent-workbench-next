import { useEffect, useState } from "react";
import { api, API_ERROR_MESSAGES, ApiError } from "@/app/api";
import type {
  HostHealth,
  HostInputDTO,
  HostSummaryDTO,
  HistoryDTO,
  MagpieStatusDTO,
  WireguardDTO,
} from "./types";

const POLL_MS = 10_000;

/** Root projection of the shell: the frozen `host_gateway.py` envelope `{hosts:[...]}`. The
 *  same call feeds the host selectors and the Equipos screen, so there is one host list. */
type HostProjectionDTO = { hosts?: HostSummaryDTO[] };

/** Older gateways used `status` for "enabled" and reported SSH health as `ssh_status`.
 *  The row keeps its published fields; only the derived health is normalized. */
function normalizeHosts(rows: HostSummaryDTO[]) {
  return rows.flatMap((host) => {
    if (!host || typeof host.id !== "string" || !host.id) return [];
    const ssh = host.ssh_status ?? "";
    const health: HostHealth =
      ssh === "connected" || ssh === "warning"
        ? "online"
        : ssh === "error"
          ? "offline"
          : host.status === "online" || host.status === "connected"
            ? "online"
            : host.status === "offline" || host.status === "error"
              ? "offline"
              : "unknown";
    return [
      {
        ...host,
        name: host.name || host.id,
        enabled:
          typeof host.enabled === "boolean" ? host.enabled : health === "online",
        status: health,
      },
    ];
  });
}

/** The canonical host records of this installation, under the frozen envelope. */
async function fetchHosts(signal?: AbortSignal) {
  const payload = await api<HostProjectionDTO>("/api/hosts", { signal });
  return normalizeHosts(payload.hosts ?? []);
}

export function saveHost(input: HostInputDTO) {
  return input.id
    ? api<HostSummaryDTO>(`/api/hosts/${encodeURIComponent(input.id)}`, {
        method: "PUT",
        data: input,
      })
    : api<HostSummaryDTO>("/api/hosts", { data: input });
}

/** Explicit trust: only the key observed during a blocked handshake of that revision is
 *  accepted, so no host is trusted on first use. */
export function confirmTrust(id: string, revision: number, fingerprint: string) {
  return api<HostSummaryDTO>(`/api/hosts/${encodeURIComponent(id)}/trust`, {
    data: { revision, fingerprint },
  });
}

export function syncHost(id: string) {
  return api<unknown>(`/api/hosts/${encodeURIComponent(id)}/sync`, {
    data: {},
  });
}

/** Host-history collection remains explicit and opt-in. */
export function fetchHistory(id: string, signal?: AbortSignal) {
  return api<HistoryDTO>("/api/hosts/" + encodeURIComponent(id) + "/history", { signal });
}

export function setHistoryConsent(id: string, enabled: boolean) {
  return api<HistoryDTO>("/api/hosts/" + encodeURIComponent(id) + "/history", {
    method: "POST",
    data: { enabled },
  });
}

export function syncHostHistory(id: string) {
  return api<HistoryDTO>("/api/hosts/" + encodeURIComponent(id) + "/history/sync", {
    method: "POST",
    data: {},
  });
}

export function checkWireguard(id: string, signal?: AbortSignal) {
  return api<WireguardDTO>(
    `/api/network/wireguard?host_id=${encodeURIComponent(id)}`,
    { signal },
  );
}

export function magpieStatus(id: string, signal?: AbortSignal) {
  return api<MagpieStatusDTO>(
    `/api/hosts/${encodeURIComponent(id)}/magpie/status`,
    { signal },
  );
}

export async function installMagpie(id: string, revision: number | undefined) {
  let status = await api<MagpieStatusDTO>(
    `/api/hosts/${encodeURIComponent(id)}/magpie/install`,
    { method: "POST", data: { revision } },
  );
  // A successful upload can answer 202 while the native actor is starting.
  const deadline = Date.now() + 30_000;
  while (!status.ready && Date.now() < deadline) {
    await new Promise((resolve) => setTimeout(resolve, 500));
    status = await magpieStatus(id, AbortSignal.timeout(Math.max(1, deadline - Date.now())));
    if (status.revision !== revision)
      throw new ApiError(API_ERROR_MESSAGES.identity_changed, 0, "identity_changed");
  }
  return status;
}

/** Stable identity of what the per-host history depends on (sorted ids, registry
 *  revisions and sync marks): a poll that changes none of it must not refetch histories. */
export function hostsSignature(hosts: HostSummaryDTO[]) {
  return hosts
    .map((host) => `${host.id}:${host.revision ?? ""}:${host.last_sync ?? ""}:${host.files ?? ""}`)
    .sort()
    .join("|");
}

function useHostSource(load: (signal?: AbortSignal) => Promise<HostSummaryDTO[]>) {
  const [hosts, setHosts] = useState<HostSummaryDTO[]>([]);
  const [error, setError] = useState("");
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    let timer: number | undefined;
    async function poll() {
      try {
        const rows = await load(controller.signal);
        if (controller.signal.aborted) return;
        // An identical answer keeps the same array so dependants do not re-run.
        setHosts((previous) =>
          JSON.stringify(previous) === JSON.stringify(rows) ? previous : rows,
        );
        setError("");
      } catch (value) {
        if (!controller.signal.aborted)
          setError(
            value instanceof Error
              ? value.message
              : API_ERROR_MESSAGES.unreachable_hosts,
          );
      } finally {
        if (!controller.signal.aborted) timer = window.setTimeout(poll, POLL_MS);
      }
    }
    void poll();
    return () => {
      controller.abort();
      window.clearTimeout(timer);
    };
  }, [load, revision]);
  return { hosts, error, refresh: () => setRevision((value) => value + 1) };
}

/** Host selector and the Equipos screen: the canonical records, refreshed every 10 s. */
export function useHosts() {
  return useHostSource(fetchHosts);
}
