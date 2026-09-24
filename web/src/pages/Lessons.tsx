import * as React from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useSearchParams } from "react-router-dom"
import { ChevronLeft, ChevronRight, MoreHorizontal, X } from "lucide-react"
import { toast } from "sonner"
import { api } from "@/lib/api"
import { qk } from "@/lib/queryKeys"
import { useSSE } from "@/lib/sse"
import { brandName, formatBytes, formatRelativeTime } from "@/lib/format"
import { rowFocusTargets } from "@/lib/focus"
import { cn } from "@/lib/utils"
import { cancelOutcome, errorMessage, failureToast, itemOutcome, type ItemOutcome } from "@/lib/errors"
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
import { Label } from "@/components/ui/label"
import { StatusBadge } from "@/components/StatusBadge"
import { ProgressRow } from "@/components/ProgressRow"
import { QueryStatus } from "@/components/QueryState"
import { ConfirmDialog } from "@/components/ConfirmDialog"

const STATUS_TABS: LessonStatus[] = [
  "pending",
  "downloading",
  "downloaded",
  "failed",
  "skipped",
]
const PAGE_SIZE = 50

// WIDE_ONLY hides a column below xl (1280px). It is on the Brand and Quality
// columns, header and cells alike, so the cells stay under their headers
// (owner's ruling 2026-09-24, (t)). Below xl a row note also narrows to 22rem
// (see the note), and together they let the table fit a 1024px window with
// short titles (measured in Chromium; BACKLOG D131).
const WIDE_ONLY = "hidden xl:table-cell"

// A dialog opened from a row remembers the row order at that moment, so focus
// can return to a neighbour if the row itself has left the list on close.
interface RowDialog {
  lesson: LessonDTO
  order: number[]
}

const actionsSelector = (id: number) => `[data-row-actions="${id}"]`

// noItem is a row dialog's confirm without a row. It cannot happen (a row
// dialog is open exactly while its row is set, and a closed dialog sends
// nothing), but if it did it must fail rather than report a success.
const noItem = (): Promise<never> => Promise.reject(new Error("the dialog has no lesson"))

// TOMBSTONE is the reason a delete stores on the lesson it skips.
const TOMBSTONE = "deleted"

// rowNote is the muted line under a lesson's title, all stored in `error`:
// why it was skipped, why it failed, or, on a downloaded lesson, why a
// re-download failed while its earlier download was kept (owner's ruling
// 2026-09-24, (h)); a successful download clears it. Nothing for any other
// status. A delete's tombstone reads "Files deleted", the reason under the
// "skipped" badge, not a bare "deleted" that looks like a code or like the
// lesson itself was deleted. The stored value stays as it is.
function rowNote(lesson: LessonDTO): string | null {
  if (lesson.status !== "skipped" && lesson.status !== "failed" && lesson.status !== "downloaded")
    return null
  const note = lesson.error?.trim()
  if (lesson.status === "skipped" && note === TOMBSTONE) return "Files deleted"
  return note ? note : null
}

export function Lessons() {
  const qc = useQueryClient()
  const [params, setParams] = useSearchParams()
  const followId = params.get("follow")
  const follow = followId ? Number(followId) : null

  const [tab, setTab] = React.useState<"all" | LessonStatus>("all")
  const [offset, setOffset] = React.useState(0)
  const [search, setSearch] = React.useState("")
  const [skipping, setSkipping] = React.useState<RowDialog | null>(null)
  const [skipReason, setSkipReason] = React.useState("")
  const skipReasonId = React.useId()
  const [deleting, setDeleting] = React.useState<RowDialog | null>(null)
  const headingRef = React.useRef<HTMLHeadingElement>(null)

  // A fresh Skip starts without the last one's reason.
  React.useEffect(() => {
    if (!skipping) setSkipReason("")
  }, [skipping])

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

  // The follow's name for the filter badge (the id means nothing to a user).
  // The same query the Follows page runs, so usually already cached.
  const followList = useQuery({
    queryKey: qk.follows,
    queryFn: api.listFollows,
    enabled: follow != null,
  })
  const followTitle =
    follow != null ? followList.data?.find((f) => f.id === follow)?.title : undefined
  const filterLabel = followTitle ? `Filtered by “${followTitle}”` : "Filtered by one follow"

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

  // Row actions report a failure as a toast titled with the outcome and the
  // lesson, and the server's sentence (or our own, never "HTTP 502") below.
  //
  // A race with something done elsewhere is not a failure. It gets a
  // neutral note that goes away by itself, like Skip's and Delete's, titled
  // with what happened:
  // - Cancel: a 404 reads "Already removed" and a 409 (the download had
  //   already ended) "Already ended". "Already" fits: the press wanted it
  //   gone or stopped.
  // - Download: a 404 has two causes, the lesson went with its follow
  //   (msgDownloadGone), or a Skip or a delete elsewhere took the new
  //   download off the queue and the row stays, marked skipped
  //   (msgDownloadJobGone). The title is true of both.
  // - Un-skip: a 404 reads "Removed elsewhere"; the press wanted it back.
  // The lists refresh after a failure too, so a row that was out of date goes.
  const refreshRows = () => {
    qc.invalidateQueries({ queryKey: qk.jobs() })
    qc.invalidateQueries({ queryKey: ["lessons"] })
    qc.invalidateQueries({ queryKey: qk.summary })
  }

  const download = useMutation({
    mutationFn: async (lesson: LessonDTO) => {
      let queuedNow = false
      const outcome = await itemOutcome(
        api.downloadLesson(lesson.railcontent_id).then(({ status: s }) => {
          queuedNow = s === 202
        }),
      )
      return outcome === "already-gone" ? outcome : queuedNow ? "queued" : "already-queued"
    },
    onSuccess: (outcome, lesson) => {
      const description = lesson.title
      if (outcome === "already-gone") {
        toast.message("Won't download: skipped or removed elsewhere", { description })
      } else if (outcome === "queued") toast.success("Queued", { description })
      else toast.message("Already queued", { description })
    },
    onError: (err, lesson) => {
      failureToast(`Couldn't queue “${lesson.title}”`, errorMessage(err))
    },
    onSettled: refreshRows,
  })

  const cancel = useMutation({
    mutationFn: ({ jobId }: { jobId: number; lesson: LessonDTO }) =>
      cancelOutcome(api.cancelJob(jobId)),
    onSuccess: (outcome, { lesson }) => {
      const description = lesson.title
      if (outcome === "already-gone") toast.message("Already removed", { description })
      else if (outcome === "already-ended") toast.message("Already ended", { description })
      else toast.success("Download canceled", { description })
    },
    onError: (err, { lesson }) => {
      failureToast(`Couldn't cancel the download of “${lesson.title}”`, errorMessage(err))
    },
    onSettled: refreshRows,
  })

  const unskip = useMutation({
    mutationFn: (lesson: LessonDTO) => itemOutcome(api.unskipLesson(lesson.railcontent_id)),
    onSuccess: (outcome, lesson) => {
      const description = lesson.title
      if (outcome === "already-gone") toast.message("Removed elsewhere", { description })
      else toast.success("Lesson un-skipped", { description })
    },
    onError: (err, lesson) => {
      failureToast(`Couldn't un-skip “${lesson.title}”`, errorMessage(err))
    },
    onSettled: refreshRows,
  })

  // Per-lesson delete: the server first removes the lesson's queued and running
  // jobs (killing a running download), then removes its recorded files and
  // tombstone-skips the row (skipped, paths cleared).
  //
  // The refresh runs on FAILURE too (finally): a 500 or 409 arrives after the
  // jobs were already removed, so the lesson's status and the jobs list changed
  // even though files were kept. The raw ["lessons"] prefix covers every
  // keyed/live variant, summary the per-status counts. The dialog stays
  // pending until the refresh lands, so a failure's message appears together
  // with the refreshed lists, and on success focus returns to a row that is
  // already where the server says it is. A 404 means it was removed elsewhere
  // first: that closes the dialog as done (see itemOutcome).
  const deleteLesson = (id: number): Promise<ItemOutcome> =>
    itemOutcome(api.deleteLesson(id)).finally(() =>
      Promise.all([
        qc.invalidateQueries({ queryKey: qk.jobs() }),
        qc.invalidateQueries({ queryKey: ["lessons"] }),
        qc.invalidateQueries({ queryKey: qk.summary }),
      ]),
    )

  // A 404 means the lesson was removed meanwhile (with its follow): nothing
  // is left to skip, so the dialog closes as done, like a delete's 404, and
  // the refresh drops the row.
  const skipLesson = (id: number, reason: string): Promise<ItemOutcome> =>
    itemOutcome(api.skipLesson(id, { reason: reason || undefined })).then(async (outcome) => {
      await Promise.all([
        qc.invalidateQueries({ queryKey: ["lessons"] }),
        qc.invalidateQueries({ queryKey: qk.summary }),
      ])
      return outcome
    })

  // Where focus goes when a row's dialog closes: the row's Actions button, a
  // neighbour's when the row has left the list, else the page heading.
  const rowReturn = (d: RowDialog | null) => () =>
    d
      ? rowFocusTargets(d.order, d.lesson.railcontent_id, actionsSelector, headingRef.current)
      : [headingRef.current]
  const openRowDialog = (set: (d: RowDialog) => void, lesson: LessonDTO) =>
    set({ lesson, order: rows.map((r) => r.railcontent_id) })

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
        {/* tabIndex -1: the last place focus can return to when a dialog
            closes and neither its row nor a neighbour is left. */}
        {/* -mx-1.5 px-1.5: the ring gets room around the letters without
            moving the heading. */}
        <h1
          ref={headingRef}
          tabIndex={-1}
          className="-mx-1.5 rounded-md px-1.5 text-2xl font-bold outline-none focus-visible:ring-[3px] focus-visible:ring-ring/60"
        >
          Lessons
        </h1>
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
          {/* A long follow name truncates inside the badge (it may shrink,
              min-w-0); the title attribute carries the full label. */}
          <Badge variant="secondary" className="min-w-0 shrink" title={filterLabel}>
            <span className="truncate">{filterLabel}</span>
          </Badge>
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
            {follow == null
              ? "All tracked lessons"
              : followTitle
                ? `Lessons of “${followTitle}”`
                : "Lessons of one follow"}
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          {lessons.isPending || lessons.isError ? (
            <QueryStatus
              loading={lessons.isPending}
              error={lessons.error}
              onRetry={() => lessons.refetch()}
              fallbackMessage="Couldn't load the lessons. Check that DrumDrop is running, then Retry."
            />
          ) : rows.length > 0 ? (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Title</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead className={WIDE_ONLY}>Brand</TableHead>
                  <TableHead className={WIDE_ONLY}>Quality</TableHead>
                  <TableHead>Size</TableHead>
                  <TableHead>Updated</TableHead>
                  <TableHead className="w-0" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((lesson) => {
                  const live = active[lesson.railcontent_id]
                  const note = rowNote(lesson)
                  // While a delete of it runs, no action is offered that would
                  // race it (the server may refuse them anyway).
                  const busy = lesson.deleting
                  // The running job Cancel stops: unknown until the jobs load.
                  const jobId = runningJobByRailcontent.get(lesson.railcontent_id)
                  // Every menu item is keyed by its action. The items are
                  // built from the live row, and React reuses an unkeyed
                  // item in the same place for the next status's item: the
                  // highlighted Download would become Cancel download, still
                  // highlighted, when the download starts (ruling (q)).
                  const copyItem = (
                    <DropdownMenuItem key="copy" onSelect={() => copyPath(lesson)}>
                      Copy path
                    </DropdownMenuItem>
                  )
                  return (
                    <TableRow key={lesson.railcontent_id}>
                      <TableCell className="font-medium">
                        {lesson.title}
                        {note && (
                          // Clamped; the title attribute carries all of it (a
                          // skip's reason is whatever was typed). A minimum
                          // width: titles don't wrap, so the column is as wide
                          // as the longest one on the page, and with short
                          // titles only it left a note 244px at 1024px, cut
                          // after a clause. From xl: 28rem and two lines, where
                          // every sentence the server writes fits (the
                          // longest, 147 characters, needs about 27rem). Below
                          // xl: 22rem (min-w-88) and three lines, so that with
                          // Brand and Quality hidden (WIDE_ONLY) the table fits
                          // a 1024px window without scrolling sideways (owner's
                          // ruling 2026-09-24, (t)). A long title still makes
                          // it scroll.
                          <p
                            title={note}
                            className="mt-0.5 line-clamp-3 max-w-md min-w-88 text-xs font-normal wrap-break-word whitespace-normal text-muted-foreground xl:line-clamp-2 xl:min-w-md"
                          >
                            {note}
                          </p>
                        )}
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
                        <StatusBadge status={busy ? "deleting" : lesson.status} />
                      </TableCell>
                      <TableCell className={cn(WIDE_ONLY, "text-muted-foreground")}>
                        {brandName(lesson.brand)}
                      </TableCell>
                      <TableCell className={cn(WIDE_ONLY, "text-muted-foreground")}>
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
                              data-row-actions={lesson.railcontent_id}
                            >
                              <MoreHorizontal />
                            </Button>
                          </DropdownMenuTrigger>
                          <DropdownMenuContent align="end">
                            <DropdownMenuGroup>
                              {busy ? (
                                copyItem
                              ) : lesson.status === "downloading" ? (
                                // Copy path first. The menu is built from the
                                // live row, so it changes if the download starts
                                // while it is open: Radix then highlights the
                                // first item, where Download was, and Enter must
                                // land on something harmless, never on Cancel
                                // (owner's ruling 2026-09-24, (q)).
                                <>
                                  {copyItem}
                                  <DropdownMenuItem
                                    key="cancel"
                                    disabled={jobId === undefined}
                                    onSelect={() => {
                                      if (jobId !== undefined) cancel.mutate({ jobId, lesson })
                                    }}
                                  >
                                    Cancel download
                                  </DropdownMenuItem>
                                </>
                              ) : (
                                <>
                                  {lesson.status === "skipped" && (
                                    <DropdownMenuItem key="unskip" onSelect={() => unskip.mutate(lesson)}>
                                      Un-skip
                                    </DropdownMenuItem>
                                  )}
                                  {/* A downloaded lesson WITH a note is a failed
                                      re-download that kept the earlier files:
                                      syncs leave it alone, so Download is how
                                      to try again (owner's ruling 2026-09-24,
                                      (h)). */}
                                  {(lesson.status !== "downloaded" || note) && (
                                    <DropdownMenuItem key="download" onSelect={() => download.mutate(lesson)}>
                                      Download
                                    </DropdownMenuItem>
                                  )}
                                  {(lesson.status === "pending" ||
                                    lesson.status === "failed") && (
                                    <DropdownMenuItem
                                      key="skip"
                                      onSelect={() => openRowDialog(setSkipping, lesson)}
                                    >
                                      Skip
                                    </DropdownMenuItem>
                                  )}
                                  {copyItem}
                                </>
                              )}
                            </DropdownMenuGroup>
                            {/* Whatever the status: a canceled or failed
                                re-download, or a delete that stopped the job
                                but kept files, leaves a lesson that still owns
                                files (BACKLOG D63). */}
                            {lesson.has_files && !busy && (
                              <>
                                <DropdownMenuSeparator />
                                <DropdownMenuGroup>
                                  <DropdownMenuItem
                                    variant="destructive"
                                    onSelect={() => openRowDialog(setDeleting, lesson)}
                                  >
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
            <div className="flex items-center justify-end gap-3">
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

      <ConfirmDialog
        open={skipping !== null}
        onOpenChange={(open) => {
          if (!open) setSkipping(null)
        }}
        title={skipping ? `Skip “${skipping.lesson.title}”?` : "Skip lesson?"}
        description="Any queued or running download of it stops, and what that download had written is discarded; its earlier files stay. Syncs leave a skipped lesson alone until you un-skip it."
        confirmLabel="Skip"
        pendingLabel="Skipping…"
        confirmVariant="default"
        onConfirm={() =>
          skipping ? skipLesson(skipping.lesson.railcontent_id, skipReason) : noItem()
        }
        announce={(outcome) => {
          const description = skipping?.lesson.title
          if (outcome === "already-gone") toast.message("Already removed", { description })
          else toast.success("Lesson skipped", { description })
        }}
        failureTitle={`Couldn't skip “${skipping?.lesson.title ?? "the lesson"}”`}
        returnFocus={rowReturn(skipping)}
      >
        {({ pending, confirm }) => (
          // A form, so Enter in the reason skips. data-disabled dims the
          // label with the input (the Label's group-data-[disabled] style).
          <form
            className="group flex flex-col gap-2"
            data-disabled={pending}
            onSubmit={(e) => {
              e.preventDefault()
              confirm()
            }}
          >
            <Label htmlFor={skipReasonId}>Reason (optional)</Label>
            <Input
              id={skipReasonId}
              value={skipReason}
              disabled={pending}
              onChange={(e) => setSkipReason(e.target.value)}
            />
          </form>
        )}
      </ConfirmDialog>

      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => {
          if (!open) setDeleting(null)
        }}
        title={deleting ? `Delete “${deleting.lesson.title}”?` : "Delete lesson?"}
        description="Its files are deleted from both the downloads folder and the library, and any queued or running download of it is stopped. The lesson is then marked skipped so the next sync leaves it alone; un-skip it to download it again."
        confirmLabel="Delete"
        pendingLabel="Deleting…"
        onConfirm={() => (deleting ? deleteLesson(deleting.lesson.railcontent_id) : noItem())}
        announce={(outcome) => {
          const description = deleting?.lesson.title
          if (outcome === "already-gone") toast.message("Already removed", { description })
          else toast.success("Lesson deleted", { description })
        }}
        failureTitle={`Couldn't delete “${deleting?.lesson.title ?? "the lesson"}”`}
        returnFocus={rowReturn(deleting)}
      />
    </div>
  )
}
