import * as React from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useSearchParams } from "react-router-dom"
import { RotateCcw, X } from "lucide-react"
import { toast } from "sonner"
import { api } from "@/lib/api"
import { cancelOutcome, errorMessage, failureToast, itemOutcome } from "@/lib/errors"
import { qk } from "@/lib/queryKeys"
import { formatRelativeTime } from "@/lib/format"
import type { JobDTO, JobStatus } from "@/types"
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
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs"
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { StatusBadge } from "@/components/StatusBadge"
import { QueryStatus } from "@/components/QueryState"

const STATE_TABS: JobStatus[] = ["queued", "running", "done", "failed", "canceled"]
const RECENT_LIMIT = 50

const CANCELABLE: JobStatus[] = ["queued", "running"]
const RETRYABLE: JobStatus[] = ["failed", "canceled"]

// A job a row action is about: its id, and the title its toasts name.
interface JobRef {
  id: number
  title: string
}

// jobTitle names a job by its lesson's title, or by the lesson's id while
// the title is not loaded.
const jobTitle = (job: JobDTO, titleById: ReadonlyMap<number, string>) =>
  titleById.get(job.railcontent_id) ?? `#${job.railcontent_id}`

export function Queue() {
  const qc = useQueryClient()
  const [params, setParams] = useSearchParams()
  const state = params.get("state") as JobStatus | null

  // "recent" (no ?state) lists the latest jobs with ?limit; the state tabs
  // filter by ?state with no limit.
  const jobs = useQuery({
    queryKey: qk.jobs({ state }),
    queryFn: () =>
      state ? api.listJobs({ state }) : api.listJobs({ limit: RECENT_LIMIT }),
  })

  // Resolve job titles from the lessons cache (keyed under ["lessons"] so the
  // SSE invalidation reaches it). A wide page is enough for the recent view.
  const lessons = useQuery({
    queryKey: qk.lessons({ limit: 500 }),
    queryFn: () => api.listLessons({ limit: 500 }),
  })

  const titleById = React.useMemo(() => {
    const m = new Map<number, string>()
    for (const l of lessons.data ?? []) m.set(l.railcontent_id, l.title)
    return m
  }, [lessons.data])

  const invalidate = () => {
    qc.invalidateQueries({ queryKey: qk.jobs() })
    qc.invalidateQueries({ queryKey: ["lessons"] })
    qc.invalidateQueries({ queryKey: qk.summary })
  }

  // A race with something done elsewhere is not a failure: a job removed
  // meanwhile (404), or, for Cancel, one that had already ended (409). Each
  // gets a neutral note that goes away by itself, like Skip's and Delete's
  // "Already removed". A real failure shows the server's own sentence (a
  // retry's 409 says why: the job isn't failed or canceled, or its lesson's
  // files are being deleted) and stays until closed. Either way the list
  // refreshes: the row was out of date.
  const cancel = useMutation({
    mutationFn: ({ id }: JobRef) => cancelOutcome(api.cancelJob(id)),
    onSuccess: (outcome, { title }) => {
      if (outcome === "already-gone") toast.message("Already removed", { description: title })
      else if (outcome === "already-ended") toast.message("Already ended", { description: title })
      else toast.success("Job canceled", { description: title })
    },
    onError: (err) => failureToast("Couldn't cancel the job", errorMessage(err)),
    onSettled: invalidate,
  })

  const retry = useMutation({
    mutationFn: ({ id }: JobRef) => itemOutcome(api.retryJob(id)),
    onSuccess: (outcome, { title }) => {
      if (outcome === "already-gone") toast.message("Already removed", { description: title })
      else toast.success("Retrying job", { description: title })
    },
    onError: (err) => failureToast("Couldn't retry the job", errorMessage(err)),
    onSettled: invalidate,
  })

  const onTabChange = (value: string) => {
    const next = new URLSearchParams(params)
    if (value === "recent") next.delete("state")
    else next.set("state", value)
    setParams(next)
  }

  const rows = jobs.data ?? []

  return (
    <div className="flex flex-col gap-6">
      <h1 className="text-2xl font-bold">Queue</h1>

      <Tabs value={state ?? "recent"} onValueChange={onTabChange}>
        <TabsList>
          <TabsTrigger value="recent">Recent</TabsTrigger>
          {STATE_TABS.map((s) => (
            <TabsTrigger key={s} value={s} className="capitalize">
              {s}
            </TabsTrigger>
          ))}
        </TabsList>
      </Tabs>

      <Card>
        <CardHeader>
          <CardTitle>Jobs</CardTitle>
          <CardDescription>
            {state ? `Jobs with status "${state}"` : "Most recent download jobs"}
          </CardDescription>
        </CardHeader>
        <CardContent>
          {jobs.isPending || jobs.isError ? (
            <QueryStatus
              loading={jobs.isPending}
              error={jobs.error}
              onRetry={() => jobs.refetch()}
              fallbackMessage="Couldn't load the jobs. Check that DrumDrop is running, then Retry."
            />
          ) : rows.length > 0 ? (
            <TooltipProvider>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-0">#</TableHead>
                    <TableHead>Lesson</TableHead>
                    <TableHead>Status</TableHead>
                    <TableHead>Attempts</TableHead>
                    <TableHead>Updated</TableHead>
                    <TableHead>Error</TableHead>
                    <TableHead className="w-0" />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {rows.map((job) => {
                    const ref: JobRef = { id: job.id, title: jobTitle(job, titleById) }
                    return (
                      <JobRow
                        key={job.id}
                        job={job}
                        title={ref.title}
                        cancelPending={cancel.isPending && cancel.variables.id === job.id}
                        retryPending={retry.isPending && retry.variables.id === job.id}
                        onCancel={() => cancel.mutate(ref)}
                        onRetry={() => retry.mutate(ref)}
                      />
                    )
                  })}
                </TableBody>
              </Table>
            </TooltipProvider>
          ) : (
            <p className="text-sm text-muted-foreground">No jobs</p>
          )}
        </CardContent>
      </Card>
    </div>
  )
}

function JobRow({
  job,
  title,
  cancelPending,
  retryPending,
  onCancel,
  onRetry,
}: {
  job: JobDTO
  title: string
  cancelPending: boolean
  retryPending: boolean
  onCancel: () => void
  onRetry: () => void
}) {
  const canCancel = CANCELABLE.includes(job.status)
  const canRetry = RETRYABLE.includes(job.status)
  const when = formatRelativeTime(job.finished_at ?? job.started_at ?? job.created_at)

  return (
    <TableRow>
      <TableCell className="text-muted-foreground tabular-nums">{job.id}</TableCell>
      <TableCell className="font-medium">{title}</TableCell>
      <TableCell>
        <StatusBadge status={job.status} />
      </TableCell>
      <TableCell className="text-muted-foreground tabular-nums">{job.attempts}</TableCell>
      <TableCell className="text-muted-foreground">{when}</TableCell>
      <TableCell className="max-w-xs">
        <JobError error={job.error} />
      </TableCell>
      <TableCell className="text-right">
        <div className="flex justify-end gap-2">
          <Button
            variant="outline"
            size="sm"
            disabled={!canRetry || retryPending}
            onClick={onRetry}
          >
            <RotateCcw />
            Retry
          </Button>
          <Button
            variant="ghost"
            size="sm"
            disabled={!canCancel || cancelPending}
            onClick={onCancel}
          >
            <X />
            Cancel
          </Button>
        </div>
      </TableCell>
    </TableRow>
  )
}

function JobError({ error }: { error: JobDTO["error"] }) {
  if (!error) return <span className="text-muted-foreground">—</span>
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span className="block truncate text-sm text-destructive">{error}</span>
      </TooltipTrigger>
      <TooltipContent className="max-w-md break-words">{error}</TooltipContent>
    </Tooltip>
  )
}
