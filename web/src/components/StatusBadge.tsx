import { Badge } from "@/components/ui/badge"
import { cn } from "@/lib/utils"
import type { JobStatus, LessonStatus } from "@/types"

// tone maps each lesson + job status to a colored Badge treatment. Lesson and
// job vocabularies overlap conceptually: downloaded/done → green, downloading/
// running → amber, pending/queued → muted, failed → destructive, skipped/
// canceled → neutral outline.
const tone: Record<string, string> = {
  downloaded: "bg-emerald-600/20 text-emerald-400 border-emerald-600/30",
  done: "bg-emerald-600/20 text-emerald-400 border-emerald-600/30",
  downloading: "bg-amber-500/20 text-amber-400 border-amber-500/30",
  running: "bg-amber-500/20 text-amber-400 border-amber-500/30",
  pending: "bg-zinc-500/15 text-zinc-300 border-zinc-500/25",
  queued: "bg-zinc-500/15 text-zinc-300 border-zinc-500/25",
  failed: "bg-red-600/20 text-red-400 border-red-600/30",
  skipped: "bg-transparent text-muted-foreground border-border",
  canceled: "bg-transparent text-muted-foreground border-border",
}

export function StatusBadge({ status }: { status: LessonStatus | JobStatus }) {
  return (
    <Badge variant="outline" className={cn("font-medium", tone[status])}>
      {status}
    </Badge>
  )
}
