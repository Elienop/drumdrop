import { Button } from "@/components/ui/button"
import { errorMessage } from "@/lib/errors"

// QueryStatus is the loading / error fallback shared by every page's primary
// query. Loading renders a muted "Loading…" line; error renders the server's
// message, or `fallbackMessage` when it sent none (never "HTTP 502"), plus a
// Retry button wired to refetch. Pages render this BEFORE the data/empty
// branch so the empty message never shows while a query is loading or has
// errored.
export function QueryStatus({
  loading,
  error,
  onRetry,
  fallbackMessage,
}: {
  loading: boolean
  error: unknown
  onRetry: () => void
  fallbackMessage?: string
}) {
  if (loading) {
    return <p className="text-sm text-muted-foreground">Loading…</p>
  }
  return (
    <div className="flex items-center justify-between gap-4">
      <p className="text-sm text-destructive">{errorMessage(error, fallbackMessage)}</p>
      <Button variant="outline" size="sm" onClick={onRetry}>
        Retry
      </Button>
    </div>
  )
}
