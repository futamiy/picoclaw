import type { ReactNode } from "react"
import { useTranslation } from "react-i18next"

import type {
  HonchoConfig,
  HonchoDialecticLevel,
  MemoryRecallMode,
} from "@/api/tools"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"

interface HonchoSettingsProps {
  draft: HonchoConfig
  onUpdate: (partial: Partial<HonchoConfig>) => void
  onTest: () => void
  isTesting: boolean
}

export function HonchoSettings({
  draft,
  onUpdate,
  onTest,
  isTesting,
}: HonchoSettingsProps) {
  const { t } = useTranslation()

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h3 className="text-foreground/80 text-[13px] font-bold tracking-widest uppercase">
          {t("pages.agent.tools.memory.honcho.title", "Honcho")}
        </h3>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={onTest}
          disabled={isTesting}
          className="rounded-xl"
        >
          {t(
            "pages.agent.tools.memory.honcho.test",
            "Test Connection",
          )}
        </Button>
      </div>

      <div className="bg-card border-border/40 divide-border/40 divide-y overflow-hidden rounded-2xl border shadow-sm">
        <SettingRow
          label={t(
            "pages.agent.tools.memory.honcho.enabled",
            "Sync to Honcho",
          )}
          description={t(
            "pages.agent.tools.memory.honcho.enabled_hint",
            "Push conversation transcripts to a Honcho server for background reasoning.",
          )}
        >
          <Switch
            checked={draft.enabled}
            onCheckedChange={(checked) => onUpdate({ enabled: checked })}
            className="data-[state=checked]:shadow-xs"
          />
        </SettingRow>

        <SettingRow
          label={t("pages.agent.tools.memory.honcho.base_url", "Server URL")}
          description={t(
            "pages.agent.tools.memory.honcho.base_url_hint",
            "Honcho server base URL. Defaults to the managed api.honcho.dev.",
          )}
        >
          <Input
            className="bg-muted/40 hover:bg-muted/60 focus-visible:bg-background focus-visible:border-border/80 focus-visible:ring-foreground/5 w-full rounded-xl border-transparent shadow-none transition-all duration-300 sm:w-64"
            value={draft.base_url ?? ""}
            onChange={(event) => onUpdate({ base_url: event.target.value })}
            placeholder="https://api.honcho.dev"
          />
        </SettingRow>

        <SettingRow
          label={t("pages.agent.tools.memory.honcho.api_key", "API Key")}
          description={t(
            "pages.agent.tools.memory.honcho.api_key_hint",
            "Bearer token. Leave blank to keep the current key.",
          )}
        >
          <div className="flex w-full flex-col gap-2 sm:w-64">
            <Input
              type="password"
              className="bg-muted/40 hover:bg-muted/60 focus-visible:bg-background focus-visible:border-border/80 focus-visible:ring-foreground/5 w-full rounded-xl border-transparent shadow-none transition-all duration-300"
              value={draft.api_key ?? ""}
              onChange={(event) => onUpdate({ api_key: event.target.value })}
              placeholder={
                draft.api_key_set
                  ? t(
                      "pages.agent.tools.memory.honcho.api_key_set",
                      "•••••••• (set)",
                    )
                  : t("pages.agent.tools.memory.honcho.api_key_empty", "Not set")
              }
            />
            {draft.api_key_set && (
              <label className="text-muted-foreground/80 flex items-center gap-2 text-[12px]">
                <Switch
                  checked={draft.clear_api_key ?? false}
                  onCheckedChange={(checked) =>
                    onUpdate({ clear_api_key: checked })
                  }
                  className="data-[state=checked]:shadow-xs"
                />
                {t("pages.agent.tools.memory.honcho.clear_api_key", "Clear key")}
              </label>
            )}
          </div>
        </SettingRow>

        <SettingRow
          label={t(
            "pages.agent.tools.memory.honcho.workspace_id",
            "Workspace ID",
          )}
          description={t(
            "pages.agent.tools.memory.honcho.workspace_id_hint",
            "Scopes stored data. Empty uses the agent ID.",
          )}
        >
          <Input
            className="bg-muted/40 hover:bg-muted/60 focus-visible:bg-background focus-visible:border-border/80 focus-visible:ring-foreground/5 w-full rounded-xl border-transparent shadow-none transition-all duration-300 sm:w-64"
            value={draft.workspace_id ?? ""}
            onChange={(event) => onUpdate({ workspace_id: event.target.value })}
            placeholder={t(
              "pages.agent.tools.memory.honcho.workspace_id_placeholder",
              "Agent ID",
            )}
          />
        </SettingRow>

        <SettingRow
          label={t("pages.agent.tools.memory.honcho.user_peer_id", "User Peer")}
          description={t(
            "pages.agent.tools.memory.honcho.user_peer_id_hint",
            "Honcho peer that represents the human user.",
          )}
        >
          <Input
            className="bg-muted/40 hover:bg-muted/60 focus-visible:bg-background focus-visible:border-border/80 focus-visible:ring-foreground/5 w-full rounded-xl border-transparent shadow-none transition-all duration-300 sm:w-64"
            value={draft.user_peer_id ?? ""}
            onChange={(event) => onUpdate({ user_peer_id: event.target.value })}
            placeholder="user"
          />
        </SettingRow>

        <SettingRow
          label={t("pages.agent.tools.memory.honcho.trigger", "Send Mode")}
          description={t(
            "pages.agent.tools.memory.honcho.trigger_hint",
            "Auto sends transcripts automatically; Ask stages them for approval.",
          )}
        >
          <Select
            value={draft.trigger}
            onValueChange={(value) =>
              onUpdate({ trigger: value as HonchoConfig["trigger"] })
            }
          >
            <SelectTrigger className="bg-muted/40 hover:bg-muted/60 focus:ring-foreground/5 focus:border-border/80 w-full rounded-xl border-transparent shadow-none transition-all sm:w-64">
              <SelectValue />
            </SelectTrigger>
            <SelectContent className="border-border/40 rounded-xl shadow-lg">
              <SelectItem value="auto" className="rounded-lg">
                {t("pages.agent.tools.memory.honcho.trigger_auto", "Auto")}
              </SelectItem>
              <SelectItem value="ask" className="rounded-lg">
                {t(
                  "pages.agent.tools.memory.honcho.trigger_ask",
                  "Ask first",
                )}
              </SelectItem>
            </SelectContent>
          </Select>
        </SettingRow>

        <SettingRow
          label={t(
            "pages.agent.tools.memory.honcho.sync_every_turns",
            "Sync Cadence",
          )}
          description={t(
            "pages.agent.tools.memory.honcho.sync_every_turns_hint",
            "Send transcripts once every N completed turns.",
          )}
        >
          <Input
            type="number"
            min={1}
            className="bg-muted/40 hover:bg-muted/60 focus-visible:bg-background focus-visible:border-border/80 focus-visible:ring-foreground/5 w-full rounded-xl border-transparent shadow-none transition-all duration-300 sm:w-32"
            value={draft.sync_every_turns}
            onChange={(event) =>
              onUpdate({ sync_every_turns: Number(event.target.value) })
            }
          />
        </SettingRow>

        <SettingRow
          label={t("pages.agent.tools.memory.honcho.recall_mode", "Recall Mode")}
          description={t(
            "pages.agent.tools.memory.honcho.recall_mode_hint",
            "Hybrid injects context and exposes tools; Context injects only; Tools exposes only; Off disables recall.",
          )}
        >
          <Select
            value={draft.recall_mode || "hybrid"}
            onValueChange={(value) =>
              onUpdate({ recall_mode: value as MemoryRecallMode })
            }
          >
            <SelectTrigger className="bg-muted/40 hover:bg-muted/60 focus:ring-foreground/5 focus:border-border/80 w-full rounded-xl border-transparent shadow-none transition-all sm:w-64">
              <SelectValue />
            </SelectTrigger>
            <SelectContent className="border-border/40 rounded-xl shadow-lg">
              <SelectItem value="hybrid" className="rounded-lg">
                {t(
                  "pages.agent.tools.memory.honcho.recall_mode_hybrid",
                  "Hybrid",
                )}
              </SelectItem>
              <SelectItem value="context" className="rounded-lg">
                {t(
                  "pages.agent.tools.memory.honcho.recall_mode_context",
                  "Context only",
                )}
              </SelectItem>
              <SelectItem value="tools" className="rounded-lg">
                {t(
                  "pages.agent.tools.memory.honcho.recall_mode_tools",
                  "Tools only",
                )}
              </SelectItem>
              <SelectItem value="off" className="rounded-lg">
                {t("pages.agent.tools.memory.honcho.recall_mode_off", "Off")}
              </SelectItem>
            </SelectContent>
          </Select>
        </SettingRow>

        <SettingRow
          label={t(
            "pages.agent.tools.memory.honcho.context_cadence",
            "Recall Cadence",
          )}
          description={t(
            "pages.agent.tools.memory.honcho.context_cadence_hint",
            "Fetch memory context once every N completed turns. 0 uses the default.",
          )}
        >
          <Input
            type="number"
            min={0}
            className="bg-muted/40 hover:bg-muted/60 focus-visible:bg-background focus-visible:border-border/80 focus-visible:ring-foreground/5 w-full rounded-xl border-transparent shadow-none transition-all duration-300 sm:w-32"
            value={draft.context_cadence ?? 0}
            onChange={(event) =>
              onUpdate({ context_cadence: Number(event.target.value) })
            }
          />
        </SettingRow>

        <SettingRow
          label={t(
            "pages.agent.tools.memory.honcho.dialectic_level",
            "Reasoning Level",
          )}
          description={t(
            "pages.agent.tools.memory.honcho.dialectic_level_hint",
            "Depth of Honcho's background reasoning over your conversations.",
          )}
        >
          <Select
            value={draft.dialectic_level || "low"}
            onValueChange={(value) =>
              onUpdate({ dialectic_level: value as HonchoDialecticLevel })
            }
          >
            <SelectTrigger className="bg-muted/40 hover:bg-muted/60 focus:ring-foreground/5 focus:border-border/80 w-full rounded-xl border-transparent shadow-none transition-all sm:w-64">
              <SelectValue />
            </SelectTrigger>
            <SelectContent className="border-border/40 rounded-xl shadow-lg">
              <SelectItem value="minimal" className="rounded-lg">
                {t(
                  "pages.agent.tools.memory.honcho.dialectic_level_minimal",
                  "Minimal",
                )}
              </SelectItem>
              <SelectItem value="low" className="rounded-lg">
                {t(
                  "pages.agent.tools.memory.honcho.dialectic_level_low",
                  "Low",
                )}
              </SelectItem>
              <SelectItem value="medium" className="rounded-lg">
                {t(
                  "pages.agent.tools.memory.honcho.dialectic_level_medium",
                  "Medium",
                )}
              </SelectItem>
              <SelectItem value="high" className="rounded-lg">
                {t(
                  "pages.agent.tools.memory.honcho.dialectic_level_high",
                  "High",
                )}
              </SelectItem>
              <SelectItem value="max" className="rounded-lg">
                {t(
                  "pages.agent.tools.memory.honcho.dialectic_level_max",
                  "Max",
                )}
              </SelectItem>
            </SelectContent>
          </Select>
        </SettingRow>

        <SettingRow
          label={t(
            "pages.agent.tools.memory.honcho.dialectic_cadence",
            "Reasoning Cadence",
          )}
          description={t(
            "pages.agent.tools.memory.honcho.dialectic_cadence_hint",
            "Run background reasoning once every N completed turns. 0 uses the default.",
          )}
        >
          <Input
            type="number"
            min={0}
            className="bg-muted/40 hover:bg-muted/60 focus-visible:bg-background focus-visible:border-border/80 focus-visible:ring-foreground/5 w-full rounded-xl border-transparent shadow-none transition-all duration-300 sm:w-32"
            value={draft.dialectic_cadence ?? 0}
            onChange={(event) =>
              onUpdate({ dialectic_cadence: Number(event.target.value) })
            }
          />
        </SettingRow>

        <SettingRow
          label={t(
            "pages.agent.tools.memory.honcho.context_tokens",
            "Memory Token Cap",
          )}
          description={t(
            "pages.agent.tools.memory.honcho.context_tokens_hint",
            "Maximum tokens for the injected memory block. 0 leaves it uncapped.",
          )}
        >
          <Input
            type="number"
            min={0}
            className="bg-muted/40 hover:bg-muted/60 focus-visible:bg-background focus-visible:border-border/80 focus-visible:ring-foreground/5 w-full rounded-xl border-transparent shadow-none transition-all duration-300 sm:w-32"
            value={draft.context_tokens ?? 0}
            onChange={(event) =>
              onUpdate({ context_tokens: Number(event.target.value) })
            }
          />
        </SettingRow>
      </div>
    </div>
  )
}

export function SettingRow({
  label,
  description,
  children,
}: {
  label: string
  description: string
  children: ReactNode
}) {
  return (
    <div className="hover:bg-muted/10 flex flex-col justify-between gap-4 p-5 transition-colors sm:flex-row sm:items-center">
      <div className="w-full space-y-1 sm:max-w-md">
        <label className="text-foreground/90 text-[15px] font-semibold tracking-tight">
          {label}
        </label>
        <p className="text-muted-foreground/80 text-[13px] leading-relaxed">
          {description}
        </p>
      </div>
      {children}
    </div>
  )
}
