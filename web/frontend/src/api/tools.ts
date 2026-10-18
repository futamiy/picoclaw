import { launcherFetch } from "@/api/http"

export interface ToolSupportItem {
  name: string
  description: string
  category: string
  config_key: string
  status: "enabled" | "disabled" | "blocked"
  reason_code?: string
}

interface ToolsResponse {
  tools: ToolSupportItem[]
}

interface ToolActionResponse {
  status: string
}

export interface WebSearchProviderOption {
  id: string
  label: string
  configured: boolean
  current: boolean
  requires_auth: boolean
}

export interface WebSearchProviderConfig {
  enabled: boolean
  max_results: number
  base_url?: string
  api_key?: string
  api_keys?: string[]
  model?: string
  api_key_set?: boolean
}

export interface WebSearchConfigResponse {
  provider: string
  current_service: string
  prefer_native: boolean
  proxy?: string
  providers: WebSearchProviderOption[]
  settings: Record<string, WebSearchProviderConfig>
}

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await launcherFetch(path, options)
  if (!res.ok) {
    let message = `API error: ${res.status} ${res.statusText}`
    try {
      const body = (await res.json()) as {
        error?: string
        errors?: string[]
      }
      if (Array.isArray(body.errors) && body.errors.length > 0) {
        message = body.errors.join("; ")
      } else if (typeof body.error === "string" && body.error.trim() !== "") {
        message = body.error
      }
    } catch {
      // ignore invalid body
    }
    throw new Error(message)
  }
  return res.json() as Promise<T>
}

export async function getTools(): Promise<ToolsResponse> {
  return request<ToolsResponse>("/api/tools")
}

export async function setToolEnabled(
  name: string,
  enabled: boolean,
): Promise<ToolActionResponse> {
  return request<ToolActionResponse>(
    `/api/tools/${encodeURIComponent(name)}/state`,
    {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ enabled }),
    },
  )
}

export async function getWebSearchConfig(): Promise<WebSearchConfigResponse> {
  return request<WebSearchConfigResponse>("/api/tools/web-search-config")
}

export async function updateWebSearchConfig(
  payload: WebSearchConfigResponse,
): Promise<WebSearchConfigResponse> {
  return request<WebSearchConfigResponse>("/api/tools/web-search-config", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  })
}

export type MemoryProvider = "local" | "honcho" | "mem0" | "retaindb"

export interface HonchoConfig {
  enabled: boolean
  base_url?: string
  api_key_set: boolean
  api_key?: string
  clear_api_key?: boolean
  workspace_id?: string
  user_peer_id?: string
  trigger: "auto" | "ask"
  sync_every_turns: number
}

export type MemoryRecallMode = "hybrid" | "context" | "tools" | "off"

export interface Mem0Config {
  enabled: boolean
  base_url?: string
  api_key_set: boolean
  api_key?: string
  clear_api_key?: boolean
  user_id?: string
  agent_id?: string
  sync_every_turns: number
  recall_mode: MemoryRecallMode
  context_cadence: number
  recall_limit: number
}

export interface RetainDBConfig {
  enabled: boolean
  base_url?: string
  api_key_set: boolean
  api_key?: string
  clear_api_key?: boolean
  namespace?: string
  user_id?: string
  sync_every_turns: number
  recall_mode: MemoryRecallMode
  context_cadence: number
  recall_limit: number
}

export interface MemoryBackupConfig {
  enabled: boolean
  dir?: string
  keep: number
  interval_hours: number
  drift_keep: number
}

export interface MemoryConfigResponse {
  enabled: boolean
  provider: MemoryProvider
  long_term_char_limit: number
  daily_notes_enabled: boolean
  daily_notes_days: number
  daily_note_char_limit: number
  extract_enabled: boolean
  extract_every_turns: number
  extract_model?: string
  profile_enabled: boolean
  profile_char_limit: number
  backup: MemoryBackupConfig
  honcho: HonchoConfig
  mem0: Mem0Config
  retaindb: RetainDBConfig
}

export interface HonchoPendingItem {
  agent_id: string
  session_key: string
  message_count: number
  scanned_turns: number
  created_at: string
  preview?: string
}

export async function getMemoryConfig(): Promise<MemoryConfigResponse> {
  return request<MemoryConfigResponse>("/api/tools/memory-config")
}

export async function updateMemoryConfig(
  payload: MemoryConfigResponse,
): Promise<ToolActionResponse> {
  return request<ToolActionResponse>("/api/tools/memory-config", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  })
}

export async function getHonchoPending(): Promise<{
  pending: HonchoPendingItem[]
}> {
  return request<{ pending: HonchoPendingItem[] }>(
    "/api/tools/memory/honcho/pending",
  )
}

export async function approveHoncho(
  agentId: string,
  sessionKey: string,
): Promise<ToolActionResponse> {
  return request<ToolActionResponse>("/api/tools/memory/honcho/approve", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ agent_id: agentId, session_key: sessionKey }),
  })
}

export async function dismissHoncho(
  agentId: string,
  sessionKey: string,
): Promise<ToolActionResponse> {
  return request<ToolActionResponse>("/api/tools/memory/honcho/dismiss", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ agent_id: agentId, session_key: sessionKey }),
  })
}

export async function testHoncho(
  baseUrl?: string,
  apiKey?: string,
): Promise<{ ok: boolean; error?: string }> {
  return testMemoryProvider("honcho", baseUrl, apiKey)
}

export async function testMemoryProvider(
  provider: MemoryProvider,
  baseUrl?: string,
  apiKey?: string,
): Promise<{ ok: boolean; error?: string }> {
  const res = await launcherFetch("/api/tools/memory/test", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      provider,
      base_url: baseUrl ?? "",
      api_key: apiKey ?? "",
    }),
  })
  const body = (await res.json().catch(() => ({ ok: false }))) as {
    ok: boolean
    error?: string
  }
  return body
}
