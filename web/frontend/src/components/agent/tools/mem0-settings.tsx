import { useTranslation } from "react-i18next"

import type { Mem0Config } from "@/api/tools"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"

import { SettingRow } from "./honcho-settings"

interface Mem0SettingsProps {
  draft: Mem0Config
  onUpdate: (partial: Partial<Mem0Config>) => void
  onTest: () => void
  isTesting: boolean
}

export function Mem0Settings({
  draft,
  onUpdate,
  onTest,
  isTesting,
}: Mem0SettingsProps) {
  const { t } = useTranslation()

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h3 className="text-foreground/80 text-[13px] font-bold tracking-widest uppercase">
          {t("pages.agent.tools.memory.mem0.title", "Mem0")}
        </h3>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={onTest}
          disabled={isTesting}
          className="rounded-xl"
        >
          {t("pages.agent.tools.memory.mem0.test", "Test Connection")}
        </Button>
      </div>

      <div className="bg-card border-border/40 divide-border/40 divide-y overflow-hidden rounded-2xl border shadow-sm">
        <SettingRow
          label={t("pages.agent.tools.memory.mem0.base_url", "Server URL")}
          description={t(
            "pages.agent.tools.memory.mem0.base_url_hint",
            "Mem0 server base URL. Defaults to the managed api.mem0.ai.",
          )}
        >
          <Input
            className="bg-muted/40 hover:bg-muted/60 focus-visible:bg-background focus-visible:border-border/80 focus-visible:ring-foreground/5 w-full rounded-xl border-transparent shadow-none transition-all duration-300 sm:w-64"
            value={draft.base_url ?? ""}
            onChange={(event) => onUpdate({ base_url: event.target.value })}
            placeholder="https://api.mem0.ai"
          />
        </SettingRow>

        <SettingRow
          label={t("pages.agent.tools.memory.mem0.api_key", "API Key")}
          description={t(
            "pages.agent.tools.memory.mem0.api_key_hint",
            "Token used to authenticate. Leave blank to keep the current key.",
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
                      "pages.agent.tools.memory.mem0.api_key_set",
                      "•••••••• (set)",
                    )
                  : t("pages.agent.tools.memory.mem0.api_key_empty", "Not set")
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
                {t("pages.agent.tools.memory.mem0.clear_api_key", "Clear key")}
              </label>
            )}
          </div>
        </SettingRow>

        <SettingRow
          label={t("pages.agent.tools.memory.mem0.user_id", "User ID")}
          description={t(
            "pages.agent.tools.memory.mem0.user_id_hint",
            "Scopes stored memories to the human user.",
          )}
        >
          <Input
            className="bg-muted/40 hover:bg-muted/60 focus-visible:bg-background focus-visible:border-border/80 focus-visible:ring-foreground/5 w-full rounded-xl border-transparent shadow-none transition-all duration-300 sm:w-64"
            value={draft.user_id ?? ""}
            onChange={(event) => onUpdate({ user_id: event.target.value })}
            placeholder="user"
          />
        </SettingRow>

        <SettingRow
          label={t("pages.agent.tools.memory.mem0.agent_id", "Agent ID")}
          description={t(
            "pages.agent.tools.memory.mem0.agent_id_hint",
            "Scopes stored memories to the agent. Empty uses the agent ID.",
          )}
        >
          <Input
            className="bg-muted/40 hover:bg-muted/60 focus-visible:bg-background focus-visible:border-border/80 focus-visible:ring-foreground/5 w-full rounded-xl border-transparent shadow-none transition-all duration-300 sm:w-64"
            value={draft.agent_id ?? ""}
            onChange={(event) => onUpdate({ agent_id: event.target.value })}
            placeholder={t(
              "pages.agent.tools.memory.mem0.agent_id_placeholder",
              "Agent ID",
            )}
          />
        </SettingRow>

        <SettingRow
          label={t(
            "pages.agent.tools.memory.mem0.sync_every_turns",
            "Sync Cadence",
          )}
          description={t(
            "pages.agent.tools.memory.mem0.sync_every_turns_hint",
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
      </div>
    </div>
  )
}
