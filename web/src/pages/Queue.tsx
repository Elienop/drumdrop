import * as React from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useSearchParams } from "react-router-dom"
import { RotateCcw, X } from "lucide-react"
import { toast } from "sonner"
import { api, ApiHttpError } from "@/lib/api"
import { errorMessage } from "@/lib/errors"
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

  const cancel = useMutation({
    mutationFn: (id: number) => api.cancelJob(id),
    onSuccess: () => {
      toast.success("Job canceled")
      invalidate()
    },
    onError: (err) => {
      if (err instanceof ApiHttpError && err.status === 409)
        toast.message("Job already finished")
      else toast.error("Couldn't cancel the job", { description: errorMessage(err) })
    },
  })

  const retry = useMutation({
    mutationFn: (id: number) => api.retryJob(id),
    onSuccess: () => {
      toast.success("Retrying job")
      invalidate()
    },
    onError: (err) => {
      if (err instanceof ApiHttpError && err.status === 409)
        toast.message("Job is not retryable")
      else toast.error("Couldn't retry the job", { description: errorMessage(err) })
    },
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
              fallbackMessage="Couldn't load the jobs. Check that DrumDrop is running, then retry."
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
                  {rows.map((job) => (
                    <JobRow
                      key={job.id}
                      job={job}
                      title={titleById.get(job.railcontent_id)}
                      cancelPending={cancel.isPending && cancel.variables === job.id}
                      retryPending={retry.isPending && retry.variables === job.id}
                      onCancel={() => cancel.mutate(job.id)}
                      onRetry={() => retry.mutate(job.id)}
                    />
                  ))}
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
  title: string | undefined
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
      <TableCell className="font-medium">
        {title ?? `#${job.railcontent_id}`}
      </TableCell>
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
