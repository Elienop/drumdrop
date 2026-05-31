import { ApiHttpError } from "@/lib/api"
import { Button } from "@/components/ui/button"

// QueryStatus is the loading / error fallback shared by every page's primary
// query. Loading renders a muted "Loading…" line; error renders the clean
// server message (ApiHttpError.message) plus a Retry button wired to refetch.
// Pages render this BEFORE the data/empty branch so the empty message never
// shows while a query is loading or has errored.
export function QueryStatus({
  loading,
  error,
  onRetry,
  fallbackMessage = "Failed to load",
}: {
  loading: boolean
  error: unknown
  onRetry: () => void
  fallbackMessage?: string
}) {
  if (loading) {
    return <p className="text-sm text-muted-foreground">Loading…</p>
  }
  const message = error instanceof ApiHttpError ? error.message : fallbackMessage
  return (
    <div className="flex items-center justify-between gap-4">
      <p className="text-sm text-destructive">{message}</p>
      <Button variant="outline" size="sm" onClick={onRetry}>
        Retry
      </Button>
    </div>
  )
}
