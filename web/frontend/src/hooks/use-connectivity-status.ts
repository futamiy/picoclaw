import { useCallback, useEffect, useState } from "react"

import { type ConnectivityStatus, getConnectivityStatus } from "@/api/status"

// One probe per page open: the active model/channel tests cost a real network
// round-trip (and one model token), so they never run on a timer.
export function useConnectivityStatus() {
  const [status, setStatus] = useState<ConnectivityStatus | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async (probe: boolean) => {
    setLoading(true)
    setError(null)
    try {
      setStatus(await getConnectivityStatus(probe))
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load(true)
  }, [load])

  return { status, loading, error, reload: load }
}
