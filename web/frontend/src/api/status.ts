import { launcherFetch } from "@/api/http"

export interface ChannelHealth {
  enabled: boolean
  running: boolean
  last_sent_at?: string
  last_error_at?: string
  last_error?: string
  probe_error?: string
  probed_at?: string
  probe_latency_ms?: number
}

export interface ModelProbeResult {
  ok: boolean
  name?: string
  model?: string
  latency_ms?: number
  error?: string
}

export interface GatewayConnectivity {
  ok: boolean
  status?: string
  uptime?: string
  pid?: number
  latency_ms?: number
  error?: string
}

export interface ConnectivityProbe {
  checked_at?: string
  model?: ModelProbeResult
  channels?: Record<string, ChannelHealth>
  error?: string
}

export interface ConnectivityStatus {
  checked_at: string
  gateway: GatewayConnectivity
  probe?: ConnectivityProbe
}

export async function getConnectivityStatus(
  probe: boolean,
): Promise<ConnectivityStatus> {
  const query = probe ? "?probe=1" : ""
  const res = await launcherFetch(`/api/status/connectivity${query}`)
  if (!res.ok) {
    throw new Error(`Failed to fetch connectivity status: ${res.status}`)
  }
  return res.json()
}
