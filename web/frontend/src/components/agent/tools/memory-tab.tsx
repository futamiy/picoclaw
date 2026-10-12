import { useTranslation } from "react-i18next"

import { ConfigChangeNotice } from "@/components/config-change-notice"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"

import { HonchoPendingCard } from "./honcho-pending-card"
import { HonchoSettings } from "./honcho-settings"
import { Mem0Settings } from "./mem0-settings"
import { MemoryProviderPicker } from "./memory-provider-picker"
import { MemorySettings } from "./memory-settings"
import { RetainDBSettings } from "./retaindb-settings"
import { useMemoryTab } from "./use-memory-tab"

export function MemoryTab() {
  const { t } = useTranslation()
  const {
    draft,
    isDirty,
    isLoading,
    hasError,
    isSaving,
    isTesting,
    pending,
    isApproving,
    isDismissing,
    updateDraft,
    save,
    testConnection,
    approve,
    dismiss,
  } = useMemoryTab()

  return (
    <div className="animate-in fade-in slide-in-from-bottom-2 space-y-12 pt-2 duration-500">
      {hasError ? (
        <div className="py-20 text-center">
          <p className="text-destructive font-medium">
            {t(
              "pages.agent.tools.memory.load_error",
              "Failed to load memory configuration",
            )}
          </p>
        </div>
      ) : isLoading || !draft ? (
        <LoadingState />
      ) : (
        <>
          <div className="flex flex-col gap-6 sm:flex-row sm:items-start sm:justify-between">
            <div className="max-w-xl space-y-3">
              <h1 className="text-foreground/90 text-2xl font-semibold tracking-tight">
                {t("pages.agent.tools.memory.title", "Memory")}
              </h1>
              <p className="text-muted-foreground/80 text-[14px] leading-relaxed">
                {t(
                  "pages.agent.tools.memory.description",
                  "Local persistent memory with optional Honcho sync for conversation transcripts.",
                )}
              </p>
            </div>

            <Button
              onClick={save}
              disabled={!isDirty || isSaving}
              className="h-10 shrink-0 rounded-xl px-6 shadow-sm transition-all active:scale-95"
            >
              {t("pages.agent.tools.memory.save", "Save Changes")}
            </Button>
          </div>

          {isDirty && (
            <ConfigChangeNotice
              kind="save"
              title={t("common.saveChangesTitle")}
              description={t(
                "pages.agent.tools.memory.unsaved_prompt",
                "This change has not been saved yet.",
              )}
            />
          )}

          <div className="space-y-10">
            <MemoryProviderPicker
              value={draft.provider}
              onChange={(provider) =>
                updateDraft((current) => ({ ...current, provider }))
              }
            />

            {draft.provider === "honcho" && (
              <>
                <HonchoPendingCard
                  items={pending}
                  isBusy={isApproving || isDismissing}
                  onApprove={approve}
                  onDismiss={dismiss}
                />

                <HonchoSettings
                  draft={draft.honcho}
                  onUpdate={(partial) =>
                    updateDraft((current) => ({
                      ...current,
                      honcho: { ...current.honcho, ...partial },
                    }))
                  }
                  onTest={() =>
                    testConnection(
                      "honcho",
                      draft.honcho.base_url,
                      draft.honcho.api_key_set
                        ? undefined
                        : draft.honcho.api_key,
                    )
                  }
                  isTesting={isTesting}
                />
              </>
            )}

            {draft.provider === "mem0" && (
              <Mem0Settings
                draft={draft.mem0}
                onUpdate={(partial) =>
                  updateDraft((current) => ({
                    ...current,
                    mem0: { ...current.mem0, ...partial },
                  }))
                }
                onTest={() =>
                  testConnection(
                    "mem0",
                    draft.mem0.base_url,
                    draft.mem0.api_key_set ? undefined : draft.mem0.api_key,
                  )
                }
                isTesting={isTesting}
              />
            )}

            {draft.provider === "retaindb" && (
              <RetainDBSettings
                draft={draft.retaindb}
                onUpdate={(partial) =>
                  updateDraft((current) => ({
                    ...current,
                    retaindb: { ...current.retaindb, ...partial },
                  }))
                }
                onTest={() =>
                  testConnection(
                    "retaindb",
                    draft.retaindb.base_url,
                    draft.retaindb.api_key_set
                      ? undefined
                      : draft.retaindb.api_key,
                  )
                }
                isTesting={isTesting}
              />
            )}

            <MemorySettings draft={draft} onUpdateDraft={updateDraft} />
          </div>
        </>
      )}
    </div>
  )
}

function LoadingState() {
  return (
    <div className="space-y-8">
      <Skeleton className="h-24 rounded-2xl" />
      <Skeleton className="h-64 rounded-2xl" />
      <Skeleton className="h-64 rounded-2xl" />
    </div>
  )
}
