import { useTranslation } from "react-i18next"

import type { MemoryProvider } from "@/api/tools"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

import { SettingRow } from "./honcho-settings"

const PROVIDERS: MemoryProvider[] = ["local", "honcho", "mem0", "retaindb"]

interface MemoryProviderPickerProps {
  value: MemoryProvider
  onChange: (provider: MemoryProvider) => void
}

export function MemoryProviderPicker({
  value,
  onChange,
}: MemoryProviderPickerProps) {
  const { t } = useTranslation()
  return (
    <div className="bg-card border-border/40 overflow-hidden rounded-2xl border shadow-sm">
      <SettingRow
        label={t("pages.agent.tools.memory.provider.label", "Memory Provider")}
        description={t(
          "pages.agent.tools.memory.provider.hint",
          "Choose where conversation transcripts are synced. Local uses built-in memory only.",
        )}
      >
        <Select
          value={value}
          onValueChange={(v) => onChange(v as MemoryProvider)}
        >
          <SelectTrigger className="bg-muted/40 hover:bg-muted/60 focus:ring-foreground/5 focus:border-border/80 w-full rounded-xl border-transparent shadow-none transition-all sm:w-64">
            <SelectValue />
          </SelectTrigger>
          <SelectContent className="border-border/40 rounded-xl shadow-lg">
            {PROVIDERS.map((id) => (
              <SelectItem key={id} value={id} className="rounded-lg">
                {t(`pages.agent.tools.memory.provider.${id}`, id)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </SettingRow>
    </div>
  )
}
