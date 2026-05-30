import { createContext, useContext, useEffect, useReducer, useState, type ReactNode } from "react"
import { useQueryClient } from "@tanstack/react-query"
import type { ProgressEvent } from "@/types"
import { getToken } from "./auth"
import { initialSSEState, invalidationKeys, seedFromSnapshot, sseReducer, type SSEState } from "./sse-reducer"

interface SSEContextValue {
  state: SSEState
  connected: boolean // EventSource stream status (NOT the Musora session)
}
const SSEContext = createContext<SSEContextValue | null>(null)

export function useSSE(): SSEContextValue {
  const ctx = useContext(SSEContext)
  if (!ctx) throw new Error("useSSE must be used within <SSEProvider>")
  return ctx
}

type Action = { type: "ready"; snapshot: ProgressEvent[] } | { type: "event"; event: ProgressEvent }

function reducer(state: SSEState, action: Action): SSEState {
  if (action.type === "ready") return seedFromSnapshot(initialSSEState(), action.snapshot)
  return sseReducer(state, action.event)
}

export function SSEProvider({ children }: { children: ReactNode }) {
  const [state, dispatch] = useReducer(reducer, undefined, initialSSEState)
  const [connected, setConnected] = useState(false) // reactive so the chip re-renders
  const qc = useQueryClient()

  useEffect(() => {
    const token = getToken()
    const url = "/api/events" + (token ? `?access_token=${encodeURIComponent(token)}` : "")
    const es = new EventSource(url)

    es.addEventListener("ready", (e) => {
      try {
        dispatch({ type: "ready", snapshot: JSON.parse((e as MessageEvent).data) })
      } catch { /* ignore malformed ready */ }
      setConnected(true)
    })
    es.onmessage = (e) => {
      try {
        const event = JSON.parse(e.data) as ProgressEvent
        dispatch({ type: "event", event })
        for (const key of invalidationKeys(event)) qc.invalidateQueries({ queryKey: key })
      } catch { /* ignore malformed frame */ }
    }
    es.onopen = () => setConnected(true)
    es.onerror = () => setConnected(false) // EventSource auto-reconnects; chip shows "reconnecting…"

    return () => es.close()
  }, [qc])

  return <SSEContext.Provider value={{ state, connected }}>{children}</SSEContext.Provider>
}
