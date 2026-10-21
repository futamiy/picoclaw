import { IconRefresh } from "@tabler/icons-react"
import { useTranslation } from "react-i18next"

import type { ChannelHealth } from "@/api/status"
import { PageHeader } from "@/components/page-header"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { useConnectivityStatus } from "@/hooks/use-connectivity-status"
import { cn } from "@/lib/utils"

function StatusDot({ ok }: { ok: boolean }) {
  return (
    <span
      className={cn(
        "inline-block size-2.5 shrink-0 rounded-full",
        ok ? "bg-emerald-500" : "bg-destructive",
      )}
    />
  )
}

function formatTimestamp(value?: string) {
  if (!value) return "—"
  const parsed = Date.parse(value)
  if (Number.isNaN(parsed)) return value
  return new Date(parsed).toLocaleString()
}

function channelDetail(health: ChannelHealth) {
  if (health.probe_error) return health.probe_error
  if (health.last_error) return health.last_error
  if (!health.running) return "channel not running"
  return ""
}

export function StatusPage() {
  const { t } = useTranslation()
  const { status, loading, error, reload } = useConnectivityStatus()

  const gatewayOk = status?.gateway.ok === true
  const model = status?.probe?.model
  const channels = status?.probe?.channels ?? {}
  const channelNames = Object.keys(channels).sort()

  return (
    <div className="flex h-full flex-col">
      <PageHeader title={t("navigation.status")}>
        <Button
          variant="outline"
          size="sm"
          disabled={loading}
          onClick={() => void reload(true)}
        >
          <IconRefresh className={cn("size-4", loading && "animate-spin")} />
          {t("status.test")}
        </Button>
      </PageHeader>

      <div className="flex-1 overflow-auto px-6 py-4">
        {error && <p className="text-destructive mb-4 text-sm">{error}</p>}

        <Card className="mb-4">
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <StatusDot ok={gatewayOk} />
              {t("status.gateway")}
            </CardTitle>
            <CardDescription>
              {status?.gateway.uptime
                ? `${t("status.uptime")}: ${status.gateway.uptime}`
                : (status?.gateway.error ?? "")}
            </CardDescription>
          </CardHeader>
          <CardContent className="text-muted-foreground flex flex-wrap gap-4 text-sm">
            <span>
              {t("status.latency")}: {status?.gateway.latency_ms ?? "—"} ms
            </span>
            {status?.gateway.pid ? (
              <span>PID: {status.gateway.pid}</span>
            ) : null}
            <span>
              {t("status.checked_at")}: {formatTimestamp(status?.checked_at)}
            </span>
          </CardContent>
        </Card>

        <Card className="mb-4">
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <StatusDot ok={model?.ok === true} />
              {t("status.model")}
            </CardTitle>
            <CardDescription>
              {model?.model ?? t("status.no_model")}
            </CardDescription>
          </CardHeader>
          <CardContent className="text-muted-foreground flex flex-wrap gap-4 text-sm">
            <span>
              {t("status.latency")}: {model?.latency_ms ?? "—"} ms
            </span>
            {model?.error ? (
              <span className="text-destructive">{model.error}</span>
            ) : null}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>{t("status.channels")}</CardTitle>
            <CardDescription>{t("status.channels_hint")}</CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            {channelNames.length === 0 && (
              <p className="text-muted-foreground text-sm">
                {t("status.no_channels")}
              </p>
            )}
            {channelNames.map((name) => {
              const health = channels[name]
              const ok = health.running && !health.probe_error
              return (
                <div
                  key={name}
                  className="border-border/50 flex flex-col gap-1 border-b pb-3 last:border-b-0 last:pb-0"
                >
                  <div className="flex items-center gap-2">
                    <StatusDot ok={ok} />
                    <span className="font-medium">{name}</span>
                    {!health.running && (
                      <Badge variant="secondary">{t("status.stopped")}</Badge>
                    )}
                    {health.probe_latency_ms !== undefined && (
                      <span className="text-muted-foreground text-xs">
                        {health.probe_latency_ms} ms
                      </span>
                    )}
                  </div>
                  {channelDetail(health) && (
                    <p className="text-destructive pl-4 text-xs">
                      {channelDetail(health)}
                    </p>
                  )}
                  <p className="text-muted-foreground pl-4 text-xs">
                    {t("status.last_sent")}:{" "}
                    {formatTimestamp(health.last_sent_at)}
                  </p>
                </div>
              )
            })}
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
