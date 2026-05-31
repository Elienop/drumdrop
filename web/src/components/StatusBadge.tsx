import { Badge } from "@/components/ui/badge"
import { cn } from "@/lib/utils"
import type { JobStatus, LessonStatus } from "@/types"

// tone maps each lesson + job status to a colored Badge treatment. Lesson and
// job vocabularies overlap conceptually: downloaded/done → green, downloading/
// running → amber, pending/queued → muted, failed → destructive, skipped/
// canceled → neutral outline.
// statusTone is the single source of truth for the four semantic tones the app
// reuses. "success" is the emerald-tinted-outline treatment that downloaded/
// done lessons use AND that the Musora connection / Settings "Connected" pill
// route through, so connected-green is defined here exactly once.
export const statusTone = {
  success: "bg-emerald-600/20 text-emerald-400 border-emerald-600/30",
  active: "bg-amber-500/20 text-amber-400 border-amber-500/30",
  muted: "bg-zinc-500/15 text-zinc-300 border-zinc-500/25",
  error: "bg-red-600/20 text-red-400 border-red-600/30",
  neutral: "bg-transparent text-muted-foreground border-border",
} as const

const tone: Record<string, string> = {
  downloaded: statusTone.success,
  done: statusTone.success,
  downloading: statusTone.active,
  running: statusTone.active,
  pending: statusTone.muted,
  queued: statusTone.muted,
  failed: statusTone.error,
  skipped: statusTone.neutral,
  canceled: statusTone.neutral,
}

export function StatusBadge({ status }: { status: LessonStatus | JobStatus }) {
  return (
    <Badge variant="outline" className={cn("font-medium", tone[status])}>
      {status}
    </Badge>
  )
}

// ConnectionPill renders a Connected/Disconnected indicator using the shared
// success tone — the same emerald-tinted-outline treatment as a downloaded
// lesson — so the concept of "connected" looks identical everywhere.
export function ConnectionPill({
  connected,
  className,
}: {
  connected: boolean
  className?: string
}) {
  return (
    <Badge
      variant="outline"
      className={cn(
        "font-medium",
        connected ? statusTone.success : statusTone.neutral,
        className,
      )}
    >
      {connected ? "Connected" : "Disconnected"}
    </Badge>
  )
}
