import type { ReactNode } from "react"
import { useTranslation } from "react-i18next"

import type { MemoryConfigResponse } from "@/api/tools"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"

import type { MemoryDraftUpdater } from "./types"

interface MemorySettingsProps {
  draft: MemoryConfigResponse
  onUpdateDraft: MemoryDraftUpdater
}

export function MemorySettings({ draft, onUpdateDraft }: MemorySettingsProps) {
  const { t } = useTranslation()
  const patch = (partial: Partial<MemoryConfigResponse>) =>
    onUpdateDraft((current) => ({ ...current, ...partial }))

  return (
    <div className="space-y-4">
      <h3 className="text-foreground/80 text-[13px] font-bold tracking-widest uppercase">
        {t("pages.agent.tools.memory.local_title", "Local Memory")}
      </h3>

      <div className="bg-card border-border/40 divide-border/40 divide-y overflow-hidden rounded-2xl border shadow-sm">
        <SettingRow
          label={t("pages.agent.tools.memory.enabled", "Enable Memory")}
          description={t(
            "pages.agent.tools.memory.enabled_hint",
            "Inject long-term memory into the prompt and register the memory tool.",
          )}
        >
          <Switch
            checked={draft.enabled}
            onCheckedChange={(checked) => patch({ enabled: checked })}
            className="data-[state=checked]:shadow-xs"
          />
        </SettingRow>

        <SettingRow
          label={t(
            "pages.agent.tools.memory.long_term_char_limit",
            "Long-Term Memory Limit",
          )}
          description={t(
            "pages.agent.tools.memory.long_term_char_limit_hint",
            "Maximum characters of MEMORY.md; writes beyond this are rejected.",
          )}
        >
          <NumberInput
            value={draft.long_term_char_limit}
            onChange={(value) => patch({ long_term_char_limit: value })}
          />
        </SettingRow>

        <SettingRow
          label={t(
            "pages.agent.tools.memory.daily_notes_enabled",
            "Daily Notes",
          )}
          description={t(
            "pages.agent.tools.memory.daily_notes_enabled_hint",
            "Write and read dated daily note files.",
          )}
        >
          <Switch
            checked={draft.daily_notes_enabled}
            onCheckedChange={(checked) => patch({ daily_notes_enabled: checked })}
            className="data-[state=checked]:shadow-xs"
          />
        </SettingRow>

        <SettingRow
          label={t("pages.agent.tools.memory.daily_notes_days", "Notes Window")}
          description={t(
            "pages.agent.tools.memory.daily_notes_days_hint",
            "How many recent days of notes are injected into the prompt.",
          )}
        >
          <NumberInput
            value={draft.daily_notes_days}
            onChange={(value) => patch({ daily_notes_days: value })}
          />
        </SettingRow>

        <SettingRow
          label={t(
            "pages.agent.tools.memory.daily_note_char_limit",
            "Daily Note Limit",
          )}
          description={t(
            "pages.agent.tools.memory.daily_note_char_limit_hint",
            "Maximum characters per daily note file.",
          )}
        >
          <NumberInput
            value={draft.daily_note_char_limit}
            onChange={(value) => patch({ daily_note_char_limit: value })}
          />
        </SettingRow>

        <SettingRow
          label={t(
            "pages.agent.tools.memory.extract_enabled",
            "Auto Extraction",
          )}
          description={t(
            "pages.agent.tools.memory.extract_enabled_hint",
            "Summarize durable facts from conversation at the end of a turn.",
          )}
        >
          <Switch
            checked={draft.extract_enabled}
            onCheckedChange={(checked) => patch({ extract_enabled: checked })}
            className="data-[state=checked]:shadow-xs"
          />
        </SettingRow>

        <SettingRow
          label={t(
            "pages.agent.tools.memory.extract_every_turns",
            "Extraction Cadence",
          )}
          description={t(
            "pages.agent.tools.memory.extract_every_turns_hint",
            "Run extraction once every N completed turns.",
          )}
        >
          <NumberInput
            value={draft.extract_every_turns}
            onChange={(value) => patch({ extract_every_turns: value })}
          />
        </SettingRow>

        <SettingRow
          label={t(
            "pages.agent.tools.memory.extract_model",
            "Extraction Model",
          )}
          description={t(
            "pages.agent.tools.memory.extract_model_hint",
            "Optional cheaper model override used for extraction.",
          )}
        >
          <Input
            className="bg-muted/40 hover:bg-muted/60 focus-visible:bg-background focus-visible:border-border/80 focus-visible:ring-foreground/5 w-full rounded-xl border-transparent shadow-none transition-all duration-300 sm:w-64"
            value={draft.extract_model ?? ""}
            onChange={(event) => patch({ extract_model: event.target.value })}
            placeholder={t(
              "pages.agent.tools.memory.extract_model_placeholder",
              "Default model",
            )}
          />
        </SettingRow>
      </div>
    </div>
  )
}

function NumberInput({
  value,
  onChange,
  min = 1,
}: {
  value: number
  onChange: (value: number) => void
  min?: number
}) {
  return (
    <Input
      type="number"
      min={min}
      className="bg-muted/40 hover:bg-muted/60 focus-visible:bg-background focus-visible:border-border/80 focus-visible:ring-foreground/5 w-full rounded-xl border-transparent shadow-none transition-all duration-300 sm:w-32"
      value={Number.isFinite(value) ? value : ""}
      onChange={(event) => onChange(Number(event.target.value))}
    />
  )
}

function SettingRow({
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
