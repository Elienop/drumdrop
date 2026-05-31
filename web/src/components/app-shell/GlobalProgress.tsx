import { useSSE } from "@/lib/sse"
import { cn } from "@/lib/utils"

// GlobalProgress renders the live activity strip in the center of the TopBar:
// a slim amber bar (average pct across active downloads) while downloads are in
// flight, a "syncing…" chip during a sync cycle, and a transient "reconnecting…"
// chip when the SSE stream is down. The `connected` flag is the EventSource
// stream status (reactive React state) — NOT the Musora account session.
export function GlobalProgress() {
  const { state, connected } = useSSE()
  const active = Object.values(state.active)
  const avgPct =
    active.length > 0 ? active.reduce((sum, d) => sum + d.pct, 0) / active.length : 0

  return (
    <div
      role="status"
      aria-live="polite"
      className="flex flex-1 items-center justify-center gap-3 text-xs"
    >
      {active.length > 0 && (
        <div className="flex w-64 items-center gap-2">
          <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-secondary">
            <div
              className="h-full rounded-full bg-primary transition-all"
              style={{ width: `${avgPct}%` }}
            />
          </div>
          <span className="whitespace-nowrap text-muted-foreground">
            downloading {active.length}
          </span>
        </div>
      )}
      {state.syncing && (
        <span className="rounded-full bg-primary/15 px-2 py-0.5 font-medium text-primary">
          syncing…
        </span>
      )}
      {!connected && (
        <span
          className={cn(
            "rounded-full bg-muted px-2 py-0.5 font-medium text-muted-foreground",
          )}
        >
          reconnecting…
        </span>
      )}
    </div>
  )
}
