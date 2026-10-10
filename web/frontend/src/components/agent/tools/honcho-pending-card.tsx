import { IconSend, IconX } from "@tabler/icons-react"
import { useTranslation } from "react-i18next"

import type { HonchoPendingItem } from "@/api/tools"
import { Button } from "@/components/ui/button"

interface HonchoPendingCardProps {
  items: HonchoPendingItem[]
  isBusy: boolean
  onApprove: (agentId: string, sessionKey: string) => void
  onDismiss: (agentId: string, sessionKey: string) => void
}

export function HonchoPendingCard({
  items,
  isBusy,
  onApprove,
  onDismiss,
}: HonchoPendingCardProps) {
  const { t } = useTranslation()

  if (items.length === 0) {
    return null
  }

  return (
    <div className="space-y-4">
      <h3 className="text-foreground/80 text-[13px] font-bold tracking-widest uppercase">
        {t(
          "pages.agent.tools.memory.honcho.pending_title",
          "Awaiting Approval",
        )}
      </h3>

      <div className="bg-card border-border/40 divide-border/40 divide-y overflow-hidden rounded-2xl border shadow-sm">
        {items.map((item) => (
          <div
            key={`${item.agent_id}:${item.session_key}`}
            className="flex flex-col gap-4 p-5 sm:flex-row sm:items-center sm:justify-between"
          >
            <div className="min-w-0 space-y-1">
              <div className="flex items-center gap-2">
                <span className="text-foreground/90 truncate font-mono text-[13px] font-semibold">
                  {item.session_key}
                </span>
                <span className="text-muted-foreground/70 shrink-0 text-[12px]">
                  {t(
                    "pages.agent.tools.memory.honcho.pending_messages",
                    "{{count}} messages",
                    { count: item.message_count },
                  )}
                </span>
              </div>
              <p className="text-muted-foreground/80 line-clamp-2 text-[13px] leading-relaxed">
                {item.preview}
              </p>
            </div>

            <div className="flex shrink-0 items-center gap-2">
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={isBusy}
                onClick={() => onDismiss(item.agent_id, item.session_key)}
                className="rounded-xl"
              >
                <IconX className="size-4" />
                {t("pages.agent.tools.memory.honcho.skip", "Skip")}
              </Button>
              <Button
                type="button"
                size="sm"
                disabled={isBusy}
                onClick={() => onApprove(item.agent_id, item.session_key)}
                className="rounded-xl"
              >
                <IconSend className="size-4" />
                {t("pages.agent.tools.memory.honcho.send", "Send")}
              </Button>
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
