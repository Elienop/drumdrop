import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useRef } from "react"
import { Play, Search, type LucideIcon } from "lucide-react"
import { toast } from "sonner"
import { api, ApiHttpError } from "@/lib/api"
import { errorMessage, failureToast } from "@/lib/errors"
import { qk } from "@/lib/queryKeys"
import { useSSE } from "@/lib/sse"
import { countOf, formatRelativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
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

// nothingAttached reports whether a mutation's last error was DrumDrop's own
// 503, its answer that the daemon (Run) or planner (Dry-run) is not attached.
// Only the server's answer counts: a reverse proxy's bodyless 503 while the
// container restarts says nothing about what is attached, and blocking on it
// would leave a dead button until the page remounts.
function nothingAttached(error: unknown): boolean {
  return error instanceof ApiHttpError && error.fromServer && error.status === 503
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
    // Blocked or not, the outcome and errorMessage's sentence (the server's
    // own, or ours when it sent none), which stays until closed: on touch or
    // with a screen reader the toast is where a blocked button's reason is read
    // (its tooltip needs a hover or keyboard focus).
    onError: (err) => failureToast("Couldn't start a sync", errorMessage(err)),
  })

  // Dry-run: asks the planner how many lessons a sync would queue.
  const dryRun = useMutation({
    mutationFn: () => api.sync(true),
    onSuccess: (result) => {
      toast.message(`A sync would queue ${countOf(result.data.would_enqueue ?? 0, "lesson", "lessons")}`)
    },
    onError: (err) => failureToast("Couldn't do a dry run", errorMessage(err)),
  })

  const runBlocked = nothingAttached(run.error)
  const dryRunBlocked = nothingAttached(dryRun.error)

  return (
    <TooltipProvider>
      <div className="flex flex-col gap-6">
        <div className="flex items-center justify-between gap-4">
          <h1 className="text-2xl font-bold">Dashboard</h1>
          <div className="flex items-center gap-3">
            <SyncButton
              label="Run sync"
              icon={Play}
              onClick={() => run.mutate()}
              pending={run.isPending}
              blocked={runBlocked}
              tooltip={errorMessage(run.error)}
            />
            <SyncButton
              label="Dry-run"
              icon={Search}
              variant="outline"
              onClick={() => dryRun.mutate()}
              pending={dryRun.isPending}
              blocked={dryRunBlocked}
              tooltip={errorMessage(dryRun.error)}
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
// TopBar's Pause, so its width does not change. Only DrumDrop's own 503
// (nothing attached to run it) really disables it, and `tooltip` is then the
// server's sentence saying so, as TopBar shows it for the same failure.
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
  const trigger = useRef<HTMLButtonElement>(null)
  if (!blocked) {
    return (
      <PendingButton size="sm" variant={variant} icon={Icon} pending={pending} onClick={onClick}>
        {label}
      </PendingButton>
    )
  }
  // Blocked, the button is aria-disabled, not `disabled` (the PendingButton
  // way): it stays in the tab order and takes the pointer, so it is the
  // tooltip's trigger itself and Radix points its aria-describedby at the
  // reason. A `disabled` button gets neither focus nor pointer events, which
  // needed a focusable span around it (BACKLOG D12).
  //
  // It keeps the disabled look, opacity-50 with no hover change, but that
  // opacity would dim its own focus ring to 1.83:1. So the button draws no
  // ring, and the span around it (not focusable, and not the trigger) draws
  // the app's ring while the button has keyboard focus: undimmed, 3.85:1.
  return (
    <span className={cn(BLOCKED_RING, BLOCKED[variant].ringOffset)}>
      <Tooltip>
        <TooltipTrigger asChild>
          <Button
            ref={trigger}
            size="sm"
            variant={variant}
            aria-disabled="true"
            className={cn(
              "opacity-50 focus-visible:ring-0 focus-visible:ring-offset-0",
              BLOCKED[variant].hover,
            )}
            // Pressing it does nothing, and the reason stays showing. Radix
            // skips the trigger's own handler for an event already prevented,
            // and both of these would close the tooltip: the click one on
            // Enter, Space or a click, the pointer-down one on a mouse or pen
            // press (after which hovering would not reopen it until the
            // pointer left).
            onPointerDown={(e) => e.preventDefault()}
            onClick={(e) => e.preventDefault()}
          >
            <Icon data-icon="inline-start" aria-hidden="true" />
            {label}
          </Button>
        </TooltipTrigger>
        {/* Capped like Queue's error tooltip: a sentence would otherwise
            render as one wide line. The open tooltip also closes on any
            pointer-down outside it, and the button is outside it, so a press
            on the button is let through (Radix's own prop for this). */}
        <TooltipContent
          className="max-w-md break-words"
          onPointerDownOutside={(e) => {
            if (trigger.current?.contains(e.target as Node)) e.preventDefault()
          }}
        >
          {tooltip}
        </TooltipContent>
      </Tooltip>
    </span>
  )
}

// The ring every button draws on keyboard focus (ui/button.tsx), drawn by a
// blocked SyncButton's wrapper instead, which is exactly the button's size.
// The ring is a box-shadow, and it fades in as the button's does
// (transition-all there): Tailwind's default duration and easing.
const BLOCKED_RING =
  "inline-flex rounded-md transition-shadow has-focus-visible:ring-[3px] has-focus-visible:ring-ring/60"

// Per variant: its hover classes from ui/button.tsx, replaced by cn()
// (tailwind-merge) with its resting colours, as a disabled button shows;
// and the ring offset a filled button takes (lib/ring.ts), for the wrapper.
const BLOCKED: Record<"default" | "outline", { hover: string; ringOffset: string }> = {
  default: {
    hover: "hover:bg-primary",
    ringOffset: "has-focus-visible:ring-offset-2 has-focus-visible:ring-offset-background",
  },
  outline: {
    hover: "hover:bg-background hover:text-inherit dark:hover:bg-input/30",
    ringOffset: "",
  },
}
