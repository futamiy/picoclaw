import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import {
  type MemoryConfigResponse,
  approveHoncho,
  dismissHoncho,
  getHonchoPending,
  getMemoryConfig,
  testHoncho,
  updateMemoryConfig,
} from "@/api/tools"
import { showSaveSuccessOrRestartToast } from "@/lib/restart-required"
import { refreshGatewayState } from "@/store/gateway"

export function useMemoryTab() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [draftOverride, setDraftOverride] =
    useState<MemoryConfigResponse | null>(null)

  const configQuery = useQuery({
    queryKey: ["tools", "memory-config"],
    queryFn: getMemoryConfig,
  })
  const pendingQuery = useQuery({
    queryKey: ["tools", "memory", "honcho-pending"],
    queryFn: getHonchoPending,
    refetchInterval: 15000,
  })

  const draft = draftOverride ?? configQuery.data ?? null
  const isDirty = useMemo(() => {
    if (!draft || !configQuery.data) {
      return false
    }
    return JSON.stringify(draft) !== JSON.stringify(configQuery.data)
  }, [draft, configQuery.data])

  const updateDraft = (
    updater: (current: MemoryConfigResponse) => MemoryConfigResponse,
  ) => {
    setDraftOverride((current) => {
      const base = current ?? configQuery.data
      return base ? updater(base) : current
    })
  }

  const saveMutation = useMutation({
    mutationFn: updateMemoryConfig,
    onSuccess: async () => {
      setDraftOverride(null)
      const gateway = await refreshGatewayState({ force: true })
      showSaveSuccessOrRestartToast(
        t,
        t("pages.agent.tools.memory.save_success", "Memory settings saved"),
        t("pages.agent.tools.memory.title", "Memory"),
        gateway?.restartRequired === true,
      )
      void queryClient.invalidateQueries({
        queryKey: ["tools", "memory-config"],
      })
      void queryClient.invalidateQueries({ queryKey: ["tools"] })
    },
    onError: (error) => {
      toast.error(
        error instanceof Error
          ? error.message
          : t(
              "pages.agent.tools.memory.save_error",
              "Failed to save memory settings",
            ),
      )
    },
  })

  const approveMutation = useMutation({
    mutationFn: ({
      agentId,
      sessionKey,
    }: {
      agentId: string
      sessionKey: string
    }) => approveHoncho(agentId, sessionKey),
    onSuccess: () => {
      toast.success(
        t("pages.agent.tools.memory.honcho.approved", "Sent to Honcho"),
      )
      void queryClient.invalidateQueries({
        queryKey: ["tools", "memory", "honcho-pending"],
      })
    },
    onError: (error) => {
      toast.error(
        error instanceof Error
          ? error.message
          : t(
              "pages.agent.tools.memory.honcho.approve_error",
              "Failed to send to Honcho",
            ),
      )
    },
  })

  const dismissMutation = useMutation({
    mutationFn: ({
      agentId,
      sessionKey,
    }: {
      agentId: string
      sessionKey: string
    }) => dismissHoncho(agentId, sessionKey),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["tools", "memory", "honcho-pending"],
      })
    },
    onError: (error) => {
      toast.error(
        error instanceof Error
          ? error.message
          : t(
              "pages.agent.tools.memory.honcho.dismiss_error",
              "Failed to skip batch",
            ),
      )
    },
  })

  const testMutation = useMutation({
    mutationFn: ({
      baseUrl,
      apiKey,
    }: {
      baseUrl?: string
      apiKey?: string
    }) => testHoncho(baseUrl, apiKey),
    onSuccess: (result) => {
      if (result.ok) {
        toast.success(
          t(
            "pages.agent.tools.memory.honcho.test_success",
            "Connection successful",
          ),
        )
      } else {
        toast.error(
          result.error ??
            t(
              "pages.agent.tools.memory.honcho.test_error",
              "Connection failed",
            ),
        )
      }
    },
    onError: (error) => {
      toast.error(
        error instanceof Error
          ? error.message
          : t(
              "pages.agent.tools.memory.honcho.test_error",
              "Connection failed",
            ),
      )
    },
  })

  const save = () => {
    if (draft) {
      saveMutation.mutate(draft)
    }
  }

  const pending = pendingQuery.data?.pending ?? []

  return {
    draft,
    isDirty,
    isLoading: configQuery.isLoading,
    hasError: configQuery.error != null,
    isSaving: saveMutation.isPending,
    isTesting: testMutation.isPending,
    pending,
    isPendingLoading: pendingQuery.isLoading,
    isApproving: approveMutation.isPending,
    isDismissing: dismissMutation.isPending,
    updateDraft,
    save,
    testConnection: (baseUrl?: string, apiKey?: string) =>
      testMutation.mutate({ baseUrl, apiKey }),
    approve: (agentId: string, sessionKey: string) =>
      approveMutation.mutate({ agentId, sessionKey }),
    dismiss: (agentId: string, sessionKey: string) =>
      dismissMutation.mutate({ agentId, sessionKey }),
  }
}
