import type {
  MemoryConfigResponse,
  ToolSupportItem,
  WebSearchConfigResponse,
} from "@/api/tools"

export type ToolsPageTab = "library" | "web-search" | "memory"
export type ToolStatusFilter = "all" | ToolSupportItem["status"]
export type GroupedTools = Array<[string, ToolSupportItem[]]>

export type WebSearchDraftUpdater = (
  updater: (current: WebSearchConfigResponse) => WebSearchConfigResponse,
) => void

export type MemoryDraftUpdater = (
  updater: (current: MemoryConfigResponse) => MemoryConfigResponse,
) => void
