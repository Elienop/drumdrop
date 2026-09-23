import * as React from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useSearchParams } from "react-router-dom"
import { ChevronLeft, ChevronRight, MoreHorizontal, Trash2, X } from "lucide-react"
import { toast } from "sonner"
import { api, ApiHttpError } from "@/lib/api"
import { qk } from "@/lib/queryKeys"
import { useSSE } from "@/lib/sse"
import { formatBytes, formatRelativeTime } from "@/lib/format"
import type { ActiveDownload } from "@/lib/sse-reducer"
import type { LessonDTO, LessonStatus } from "@/types"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Input } from "@/components/ui/input"
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
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { StatusBadge } from "@/components/StatusBadge"
import { ProgressRow } from "@/components/ProgressRow"
import { QueryStatus } from "@/components/QueryState"

const STATUS_TABS: LessonStatus[] = [
  "pending",
  "downloading",
  "downloaded",
  "failed",
  "skipped",
]
const PAGE_SIZE = 50

// hasRecordedFiles reports whether the server still records files for the
// lesson, whatever its status: a canceled or failed re-download, or a delete
// that removed the lesson's job but not its files, leaves a skipped, failed or
// pending lesson that still owns its earlier files (BACKLOG D63).
//
// The server's own predicate is `output_dir IS NOT NULL OR library_entries IS
// NOT NULL`, and the DTO carries only output_dir. That is enough today because
// every writer that sets library_entries also sets output_dir (FinishDownload),
// and every writer that clears output_dir clears library_entries with it
// (TombstoneLesson). A server change that breaks that pairing needs a field
// on LessonDTO, not a wider guess here.
function hasRecordedFiles(lesson: LessonDTO): boolean {
  return lesson.output_dir !== null
}

export function Lessons() {
  const qc = useQueryClient()
  const [params, setParams] = useSearchParams()
  const followId = params.get("follow")
  const follow = followId ? Number(followId) : null

  const [tab, setTab] = React.useState<"all" | LessonStatus>("all")
  const [offset, setOffset] = React.useState(0)
  const [search, setSearch] = React.useState("")
  const [skipping, setSkipping] = React.useState<LessonDTO | null>(null)
  const [deleting, setDeleting] = React.useState<LessonDTO | null>(null)

  const status = tab === "all" ? undefined : tab

  // When filtered by a follow we use api.followLessons but still key under the
  // ["lessons"] prefix (qk.lessons) so the SSE invalidation of ["lessons"]
  // refreshes this live view. The "All" tab pages with ?limit&offset; the
  // status tabs and follow view return the full set (no paging).
  const lessons = useQuery({
    queryKey: qk.lessons({ follow, status, offset: status ? undefined : offset }),
    queryFn: () =>
      follow != null
        ? api.followLessons(follow, status)
        : api.listLessons(
            status ? { status } : { limit: PAGE_SIZE, offset },
          ),
  })

  // Running jobs let us resolve a downloading lesson's job id for Cancel.
  const runningJobs = useQuery({
    queryKey: qk.jobs({ state: "running" }),
    queryFn: () => api.listJobs({ state: "running" }),
  })

  const runningJobByRailcontent = React.useMemo(() => {
    const m = new Map<number, number>()
    for (const j of runningJobs.data ?? []) m.set(j.railcontent_id, j.id)
    return m
  }, [runningJobs.data])

  const { state } = useSSE()
  const active = React.useMemo(() => {
    const byRailcontent: Record<number, ActiveDownload> = {}
    for (const d of Object.values(state.active)) byRailcontent[d.railcontentId] = d
    return byRailcontent
  }, [state.active])

  const download = useMutation({
    mutationFn: (id: number) => api.downloadLesson(id),
    onSuccess: ({ status: s }) => {
      if (s === 202) toast.success("Queued")
      else toast.message("Already queued")
      qc.invalidateQueries({ queryKey: qk.jobs() })
      qc.invalidateQueries({ queryKey: ["lessons"] })
      qc.invalidateQueries({ queryKey: qk.summary })
    },
    onError: (err) => {
      toast.error(err instanceof ApiHttpError ? err.message : "Download failed")
    },
  })

  const skip = useMutation({
    mutationFn: ({ id, reason }: { id: number; reason: string }) =>
      api.skipLesson(id, { reason: reason || undefined }),
    onSuccess: () => {
      toast.success("Lesson skipped")
      setSkipping(null)
      qc.invalidateQueries({ queryKey: ["lessons"] })
      qc.invalidateQueries({ queryKey: qk.summary })
    },
    onError: (err) => {
      toast.error(err instanceof ApiHttpError ? err.message : "Skip failed")
    },
  })

  const cancel = useMutation({
    mutationFn: (jobId: number) => api.cancelJob(jobId),
    onSuccess: () => {
      toast.success("Download canceled")
      qc.invalidateQueries({ queryKey: qk.jobs() })
      qc.invalidateQueries({ queryKey: ["lessons"] })
      qc.invalidateQueries({ queryKey: qk.summary })
    },
    onError: (err) => {
      toast.error(err instanceof ApiHttpError ? err.message : "Cancel failed")
    },
  })

  const unskip = useMutation({
    mutationFn: (id: number) => api.unskipLesson(id),
    onSuccess: () => {
      toast.success("Lesson un-skipped")
      qc.invalidateQueries({ queryKey: qk.jobs() })
      qc.invalidateQueries({ queryKey: ["lessons"] })
      qc.invalidateQueries({ queryKey: qk.summary })
    },
    onError: (err) => {
      toast.error(err instanceof ApiHttpError ? err.message : "Un-skip failed")
    },
  })

  // Per-lesson delete: the server first removes the lesson's queued and running
  // jobs (killing a running download), then removes its recorded files and
  // tombstone-skips the row (skipped, paths cleared).
  //
  // The refresh runs on FAILURE too (onSettled, not onSuccess): a 500 or 409
  // arrives after the jobs were already removed, so the lesson's status and the
  // jobs list changed even though the files were kept. The raw ["lessons"]
  // prefix covers every keyed/live variant, summary the per-status counts.
  // Returned, so isPending (and the disabled Delete) holds until the refreshed
  // lists have landed, and a failure's message appears together with them.
  //
  // A failure is shown inside the dialog, not as a toast: see the dialog below.
  const deleteLesson = useMutation({
    mutationFn: (id: number) => api.deleteLesson(id),
    onSuccess: () => {
      toast.success("Lesson deleted")
      setDeleting(null)
    },
    onSettled: () =>
      Promise.all([
        qc.invalidateQueries({ queryKey: qk.jobs() }),
        qc.invalidateQueries({ queryKey: ["lessons"] }),
        qc.invalidateQueries({ queryKey: qk.summary }),
      ]),
  })

  // The dialog stays open on failure, so the user reads the server's answer
  // next to the lesson it is about and can retry (a 409 asks for exactly that)
  // or cancel. It cannot be dismissed mid-request, or the answer would land in
  // a closed dialog and be lost. Closing clears the answer.
  const deleteErrorId = React.useId()
  const deleteError = deleteLesson.isError
    ? deleteLesson.error instanceof ApiHttpError
      ? deleteLesson.error.message
      : "Delete failed"
    : null
  const closeDelete = () => {
    if (deleteLesson.isPending) return
    setDeleting(null)
    deleteLesson.reset()
  }

  const copyPath = (lesson: LessonDTO) => {
    const path = lesson.video_path ?? lesson.output_dir
    if (!path) {
      toast.message("No path yet")
      return
    }
    void navigator.clipboard.writeText(path)
    toast.message("Path copied")
  }

  const onTabChange = (value: string) => {
    setTab(value as "all" | LessonStatus)
    setOffset(0)
  }

  const clearFollow = () => {
    const next = new URLSearchParams(params)
    next.delete("follow")
    setParams(next)
  }

  // Client-side title filter over the currently loaded rows.
  const rows = React.useMemo(() => {
    const all = lessons.data ?? []
    const q = search.trim().toLowerCase()
    return q ? all.filter((l) => l.title.toLowerCase().includes(q)) : all
  }, [lessons.data, search])

  const loadedCount = lessons.data?.length ?? 0

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-center justify-between gap-4">
        <h1 className="text-2xl font-bold">Lessons</h1>
        <Input
          type="search"
          placeholder="Search titles…"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="w-full max-w-xs"
        />
      </div>

      {follow != null && (
        <div className="flex items-center gap-2">
          <Badge variant="secondary">Filtered by follow #{follow}</Badge>
          <Button variant="ghost" size="sm" onClick={clearFollow}>
            <X />
            Clear
          </Button>
        </div>
      )}

      <Tabs value={tab} onValueChange={onTabChange}>
        <TabsList>
          <TabsTrigger value="all">All</TabsTrigger>
          {STATUS_TABS.map((s) => (
            <TabsTrigger key={s} value={s} className="capitalize">
              {s}
            </TabsTrigger>
          ))}
        </TabsList>
      </Tabs>

      <Card>
        <CardHeader>
          <CardTitle>Lessons</CardTitle>
          <CardDescription>
            {follow != null ? `Lessons for follow #${follow}` : "All tracked lessons"}
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          {lessons.isPending || lessons.isError ? (
            <QueryStatus
              loading={lessons.isPending}
              error={lessons.error}
              onRetry={() => lessons.refetch()}
              fallbackMessage="Failed to load lessons"
            />
          ) : rows.length > 0 ? (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Title</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Brand</TableHead>
                  <TableHead>Quality</TableHead>
                  <TableHead>Size</TableHead>
                  <TableHead>Updated</TableHead>
                  <TableHead className="w-0" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((lesson) => {
                  const live = active[lesson.railcontent_id]
                  return (
                    <TableRow key={lesson.railcontent_id}>
                      <TableCell className="font-medium">
                        {lesson.title}
                        {live && (
                          <div className="mt-2 max-w-md">
                            <ProgressRow
                              title={live.title || lesson.title}
                              pct={live.pct}
                              speed={live.speed}
                              bytes={live.bytes}
                              totalBytes={live.totalBytes}
                            />
                          </div>
                        )}
                      </TableCell>
                      <TableCell>
                        <StatusBadge status={lesson.status} />
                      </TableCell>
                      <TableCell className="text-muted-foreground">{lesson.brand}</TableCell>
                      <TableCell className="text-muted-foreground">
                        {lesson.quality ?? "—"}
                      </TableCell>
                      <TableCell className="text-muted-foreground tabular-nums">
                        {formatBytes(lesson.bytes)}
                      </TableCell>
                      <TableCell className="text-muted-foreground">
                        {formatRelativeTime(lesson.updated_at)}
                      </TableCell>
                      <TableCell className="text-right">
                        <DropdownMenu>
                          <DropdownMenuTrigger asChild>
                            <Button
                              variant="ghost"
                              size="icon"
                              aria-label={`Actions for ${lesson.title}`}
                            >
                              <MoreHorizontal />
                            </Button>
                          </DropdownMenuTrigger>
                          <DropdownMenuContent align="end">
                            <DropdownMenuGroup>
                              {lesson.status === "downloading" ? (
                                (() => {
                                  const jobId = runningJobByRailcontent.get(
                                    lesson.railcontent_id,
                                  )
                                  return (
                                    <DropdownMenuItem
                                      disabled={jobId === undefined}
                                      onSelect={() => {
                                        if (jobId !== undefined) cancel.mutate(jobId)
                                      }}
                                    >
                                      Cancel
                                    </DropdownMenuItem>
                                  )
                                })()
                              ) : lesson.status === "downloaded" ? null : (
                                <>
                                  {lesson.status === "skipped" && (
                                    <DropdownMenuItem
                                      onSelect={() =>
                                        unskip.mutate(lesson.railcontent_id)
                                      }
                                    >
                                      Un-skip
                                    </DropdownMenuItem>
                                  )}
                                  <DropdownMenuItem
                                    onSelect={() =>
                                      download.mutate(lesson.railcontent_id)
                                    }
                                  >
                                    Download
                                  </DropdownMenuItem>
                                  {(lesson.status === "pending" ||
                                    lesson.status === "failed") && (
                                    <DropdownMenuItem
                                      onSelect={() => setSkipping(lesson)}
                                    >
                                      Skip
                                    </DropdownMenuItem>
                                  )}
                                </>
                              )}
                              <DropdownMenuItem onSelect={() => copyPath(lesson)}>
                                Copy path
                              </DropdownMenuItem>
                            </DropdownMenuGroup>
                            {hasRecordedFiles(lesson) && (
                              <>
                                <DropdownMenuSeparator />
                                <DropdownMenuGroup>
                                  <DropdownMenuItem
                                    variant="destructive"
                                    onSelect={() => setDeleting(lesson)}
                                  >
                                    <Trash2 />
                                    Delete
                                  </DropdownMenuItem>
                                </DropdownMenuGroup>
                              </>
                            )}
                          </DropdownMenuContent>
                        </DropdownMenu>
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          ) : (
            <p className="text-sm text-muted-foreground">No lessons</p>
          )}

          {tab === "all" && follow == null && (
            <div className="flex items-center justify-end gap-2">
              <Button
                variant="outline"
                size="sm"
                disabled={offset === 0}
                onClick={() => setOffset((o) => Math.max(0, o - PAGE_SIZE))}
              >
                <ChevronLeft />
                Prev
              </Button>
              <Button
                variant="outline"
                size="sm"
                disabled={loadedCount < PAGE_SIZE}
                onClick={() => setOffset((o) => o + PAGE_SIZE)}
              >
                Next
                <ChevronRight />
              </Button>
            </div>
          )}
        </CardContent>
      </Card>

      <Dialog
        open={skipping !== null}
        onOpenChange={(open) => {
          if (!open) setSkipping(null)
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Skip lesson</DialogTitle>
            <DialogDescription>
              {skipping ? `Skip "${skipping.title}"?` : "Skip this lesson?"}
            </DialogDescription>
          </DialogHeader>
          <SkipForm
            pending={skip.isPending}
            onConfirm={(reason) => {
              if (skipping) skip.mutate({ id: skipping.railcontent_id, reason })
            }}
            onCancel={() => setSkipping(null)}
          />
        </DialogContent>
      </Dialog>

      <Dialog
        open={deleting !== null}
        onOpenChange={(open) => {
          if (!open) closeDelete()
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {deleting ? `Delete "${deleting.title}"?` : "Delete lesson?"}
            </DialogTitle>
            <DialogDescription>
              This removes the downloaded files (downloads + library) and cancels
              any queued or running download of it. The lesson is marked skipped
              so it is not re-downloaded; un-skip to restore it.
            </DialogDescription>
          </DialogHeader>
          {deleteError !== null && (
            <p id={deleteErrorId} role="alert" className="text-sm text-destructive">
              {deleteError}
            </p>
          )}
          <DialogFooter>
            <Button
              variant="outline"
              disabled={deleteLesson.isPending}
              onClick={closeDelete}
            >
              Cancel
            </Button>
            <Button
              variant="destructive"
              disabled={deleteLesson.isPending}
              aria-describedby={deleteError !== null ? deleteErrorId : undefined}
              onClick={() => {
                if (deleting) deleteLesson.mutate(deleting.railcontent_id)
              }}
            >
              Delete
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}

function SkipForm({
  pending,
  onConfirm,
  onCancel,
}: {
  pending: boolean
  onConfirm: (reason: string) => void
  onCancel: () => void
}) {
  const [reason, setReason] = React.useState("")
  return (
    <>
      <Input
        placeholder="Reason (optional)"
        value={reason}
        onChange={(e) => setReason(e.target.value)}
        aria-label="Skip reason"
      />
      <DialogFooter>
        <Button variant="outline" onClick={onCancel}>
          Cancel
        </Button>
        <Button disabled={pending} onClick={() => onConfirm(reason)}>
          Skip
        </Button>
      </DialogFooter>
    </>
  )
}
