import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Play, Search, type LucideIcon } from "lucide-react"
import { toast } from "sonner"
import { api, ApiHttpError } from "@/lib/api"
import { errorMessage, failureToast } from "@/lib/errors"
import { qk } from "@/lib/queryKeys"
import { useSSE } from "@/lib/sse"
import { countOf, formatRelativeTime } from "@/lib/format"
import type { JobStatus, LessonStatus } from "@/types"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { PendingButton } from "@/components/PendingButton"
import { ProgressRow } from "@/components/ProgressRow"
import { StatusBadge } from "@/components/StatusBadge"
import { QueryStatus } from "@/components/QueryState"

// Stable enum orders so the stat cards render the statuses in a fixed sequence
// regardless of map iteration order in the JSON.
const LESSON_ORDER: LessonStatus[] = ["pending", "downloading", "downloaded", "failed", "skipped"]
const JOB_ORDER: JobStatus[] = ["queued", "running", "done", "failed", "canceled"]

// is503 reports whether a mutation's last error was an HTTP 503 — the signal
// that the daemon (Run) or planner (Dry-run) is not attached.
function is503(error: unknown): boolean {
  return error instanceof ApiHttpError && error.status === 503
}

export function Dashboard() {
  const qc = useQueryClient()
  const summary = useQuery({ queryKey: qk.summary, queryFn: api.summary })
  const jobs = useQuery({ queryKey: qk.jobs({ limit: 10 }), queryFn: () => api.listJobs({ limit: 10 }) })
  const { state } = useSSE()
  const active = Object.values(state.active)

  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ["jobs"] })
    qc.invalidateQueries({ queryKey: ["summary"] })
  }

  // Run: triggers a real sync cycle. api.sync resolves to { status, data }.
  const run = useMutation({
    mutationFn: () => api.sync(false),
    onSuccess: () => {
      toast.success("Sync triggered")
      invalidate()
    },
    onError: (err) => {
      if (is503(err)) toast.error("No daemon attached")
      else failureToast("Couldn't start a sync", errorMessage(err))
    },
  })

  // Dry-run: asks the planner how many lessons a sync would queue.
  const dryRun = useMutation({
    mutationFn: () => api.sync(true),
    onSuccess: (result) => {
      toast.message(`A sync would queue ${countOf(result.data.would_enqueue ?? 0, "lesson", "lessons")}`)
    },
    onError: (err) => {
      if (is503(err)) toast.error("No planner attached")
      else failureToast("Couldn't run the dry run", errorMessage(err))
    },
  })

  const runBlocked = is503(run.error)
  const dryRunBlocked = is503(dryRun.error)

  return (
    <TooltipProvider>
      <div className="flex flex-col gap-6">
        <div className="flex items-center justify-between gap-4">
          <h1 className="text-2xl font-bold">Dashboard</h1>
          <div className="flex items-center gap-2">
            <SyncButton
              label="Run sync"
              icon={Play}
              onClick={() => run.mutate()}
              pending={run.isPending}
              blocked={runBlocked}
              tooltip="no daemon attached"
            />
            <SyncButton
              label="Dry-run"
              icon={Search}
              variant="outline"
              onClick={() => dryRun.mutate()}
              pending={dryRun.isPending}
              blocked={dryRunBlocked}
              tooltip="no planner attached"
            />
          </div>
        </div>

        {summary.isPending || summary.isError ? (
          <Card>
            <CardContent className="pt-6">
              <QueryStatus
                loading={summary.isPending}
                error={summary.error}
                onRetry={() => summary.refetch()}
                fallbackMessage="Couldn't load the summary. Check that DrumDrop is running, then Retry."
              />
            </CardContent>
          </Card>
        ) : (
          <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
            <StatCard title="Follows" description="Followed nodes and instructors">
              <div className="text-3xl font-bold tabular-nums">{summary.data.follows}</div>
            </StatCard>
            <StatCard title="Lessons" description="By status">
              <StatBreakdown order={LESSON_ORDER} counts={summary.data.lessons} />
            </StatCard>
            <StatCard title="Jobs" description="By state">
              <StatBreakdown order={JOB_ORDER} counts={summary.data.jobs} />
            </StatCard>
          </div>
        )}

        <Card>
          <CardHeader>
            <CardTitle>Now downloading</CardTitle>
            <CardDescription>Live progress from the worker</CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            {active.length === 0 ? (
              <p className="text-sm text-muted-foreground">No active downloads</p>
            ) : (
              active.map((d) => (
                <ProgressRow
                  key={d.jobId}
                  title={d.title}
                  pct={d.pct}
                  speed={d.speed}
                  bytes={d.bytes}
                  totalBytes={d.totalBytes}
                />
              ))
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Recent jobs</CardTitle>
            <CardDescription>The 10 most recent jobs</CardDescription>
          </CardHeader>
          <CardContent>
            {jobs.isPending || jobs.isError ? (
              <QueryStatus
                loading={jobs.isPending}
                error={jobs.error}
                onRetry={() => jobs.refetch()}
                fallbackMessage="Couldn't load the jobs. Check that DrumDrop is running, then Retry."
              />
            ) : jobs.data.length > 0 ? (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Job</TableHead>
                    <TableHead>Lesson</TableHead>
                    <TableHead>Status</TableHead>
                    <TableHead>Finished</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {jobs.data.map((job) => (
                    <TableRow key={job.id}>
                      <TableCell className="tabular-nums text-muted-foreground">#{job.id}</TableCell>
                      <TableCell className="tabular-nums">{job.railcontent_id}</TableCell>
                      <TableCell>
                        <StatusBadge status={job.status} />
                      </TableCell>
                      <TableCell className="text-muted-foreground">
                        {formatRelativeTime(job.finished_at)}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            ) : (
              <p className="text-sm text-muted-foreground">No jobs yet</p>
            )}
          </CardContent>
        </Card>
      </div>
    </TooltipProvider>
  )
}

function StatCard({
  title,
  description,
  children,
}: {
  title: string
  description: string
  children: React.ReactNode
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        <CardDescription>{description}</CardDescription>
      </CardHeader>
      <CardContent>{children}</CardContent>
    </Card>
  )
}

function StatBreakdown({
  order,
  counts,
}: {
  order: readonly string[]
  counts: Record<string, number> | undefined
}) {
  return (
    <dl className="flex flex-col gap-1.5 text-sm">
      {order.map((key) => (
        <div key={key} className="flex items-center justify-between gap-2">
          <dt className="text-muted-foreground capitalize">{key}</dt>
          <dd className="font-medium tabular-nums">{counts?.[key] ?? 0}</dd>
        </div>
      ))}
    </dl>
  )
}

// SyncButton runs a sync or a dry run. While its request runs it is a
// PendingButton, not `disabled`, so keyboard focus stays on it (a disabled
// button drops it to <body>); the spinner takes its icon's place, as on the
// TopBar's Pause, so its width does not change. Only a 503 (nothing
// attached to run it) really disables it.
function SyncButton({
  label,
  icon: Icon,
  onClick,
  pending,
  blocked,
  tooltip,
  variant = "default",
}: {
  label: string
  icon: LucideIcon
  onClick: () => void
  pending: boolean
  blocked: boolean
  tooltip: string
  variant?: "default" | "outline"
}) {
  if (!blocked) {
    return (
      <PendingButton size="sm" variant={variant} icon={Icon} pending={pending} onClick={onClick}>
        {label}
      </PendingButton>
    )
  }
  // A disabled button swallows pointer events, so wrap it in a focusable span
  // that owns the tooltip trigger.
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span tabIndex={0}>
          <Button size="sm" variant={variant} disabled>
            <Icon data-icon="inline-start" />
            {label}
          </Button>
        </span>
      </TooltipTrigger>
      <TooltipContent>{tooltip}</TooltipContent>
    </Tooltip>
  )
}
