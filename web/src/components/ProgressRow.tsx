import { formatBytes } from "@/lib/format"

export interface ProgressRowProps {
  title: string
  pct: number
  speed?: string
  bytes?: number
  totalBytes?: number
}

// ProgressRow renders a single in-flight download: the lesson title, a slim
// amber bar filled to `pct`, the rounded percentage, optional transfer speed,
// and a bytes/totalBytes readout. Used in the Dashboard / Queue live views.
export function ProgressRow({ title, pct, speed, bytes, totalBytes }: ProgressRowProps) {
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-center justify-between gap-2 text-sm">
        <span className="truncate font-medium">{title}</span>
        <span className="shrink-0 text-xs text-muted-foreground tabular-nums">
          {pct.toFixed(0)}%
        </span>
      </div>
      <div className="h-1.5 overflow-hidden rounded-full bg-secondary">
        <div
          className="h-full rounded-full bg-primary transition-all"
          style={{ width: `${pct}%` }}
        />
      </div>
      <div className="flex items-center justify-between gap-2 text-xs text-muted-foreground tabular-nums">
        <span>
          {formatBytes(bytes)} / {formatBytes(totalBytes)}
        </span>
        {speed && <span>{speed}</span>}
      </div>
    </div>
  )
}
