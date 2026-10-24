// JSON contracts of the frozen Workbench host surface (host_gateway.py, host_history.py,
// wireguard.py, hosts.go). Every field stays optional: an absent field means the
// installation did not report it, never an invented default.

export type HostHealth = "online" | "offline" | "unknown";

export interface HostSummaryDTO {
  id: string;
  revision?: number;
  name?: string;
  enabled?: boolean;
  status?: string;
  paired?: boolean;
  transport?: string;
  ssh_status?: string;
  error?: string;
  // Registry fields, present when the endpoint answers with host records (Equipos).
  address?: string;
  user?: string;
  port?: number;
  auth?: string;
  key_path?: string;
  password_set?: boolean;
  /** Trusted pin of this host; empty until the operator confirms the observed key. */
  fingerprint?: string;
  /** Key the handshake saw and no one confirmed yet: operations stay blocked. */
  observed_fingerprint?: string;
  home?: string;
  files?: number;
  last_sync?: number | string;
  last_attempt?: string;
}

export interface HostInputDTO {
  id?: string;
  name: string;
  address: string;
  user: string;
  port: number;
  auth: string;
  password?: string;
  key_path?: string;
  enabled: boolean;
}

/** Consent and sync receipt for one host's remote conversation history. */
export interface HistoryDTO {
  host_id?: string;
  enabled?: boolean;
  state?: string;
  files?: number;
  last_sync?: number | string;
  error?: string;
  unsupported?: string[];
  imported?: number;
}

export interface WireguardDTO {
  host_id?: string;
  host_name?: string;
  interface?: string;
  mtu?: number;
  wireguard?: boolean;
  main_default_uses_wireguard?: boolean;
  error?: string;
}

/** Read-only readiness of the native service on the host (magpie_ssh.go status). */
export interface MagpieStatusDTO {
  revision?: number;
  installed?: boolean;
  ready?: boolean;
  state?: string;
  version?: string;
}
