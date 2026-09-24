import * as React from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { Link, useNavigate } from "react-router-dom"
import { Pencil, Plus, Trash2 } from "lucide-react"
import { toast } from "sonner"
import { api } from "@/lib/api"
import { qk } from "@/lib/queryKeys"
import { brandName, formatRelativeTime } from "@/lib/format"
import { rowFocusTargets } from "@/lib/focus"
import { itemOutcome, type ItemOutcome } from "@/lib/errors"
import type { FollowDTO } from "@/types"
import { Badge } from "@/components/ui/badge"
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
import { Checkbox } from "@/components/ui/checkbox"
import { Label } from "@/components/ui/label"
import { ConfirmDialog } from "@/components/ConfirmDialog"
import { QueryStatus } from "@/components/QueryState"
import { AddFollowDialog } from "@/pages/follows/AddFollowDialog"
import { EditFollowDialog } from "@/pages/follows/EditFollowDialog"

// A dialog opened from a row remembers the row order at that moment, so focus
// can return to a neighbour if the row itself has left the list on close.
interface RowDialog {
  follow: FollowDTO
  order: number[]
}

const editSelector = (id: number) => `[data-follow-edit="${id}"]`
const removeSelector = (id: number) => `[data-follow-remove="${id}"]`

export function Follows() {
  const qc = useQueryClient()
  const navigate = useNavigate()
  const follows = useQuery({ queryKey: qk.follows, queryFn: api.listFollows })
  const [addOpen, setAddOpen] = React.useState(false)
  const [editing, setEditing] = React.useState<RowDialog | null>(null)
  const [removing, setRemoving] = React.useState<RowDialog | null>(null)
  const [deleteFiles, setDeleteFiles] = React.useState(false)
  const headingRef = React.useRef<HTMLHeadingElement>(null)
  const addButtonRef = React.useRef<HTMLButtonElement>(null)
  const deleteFilesId = React.useId()

  // Reset the "also delete files" checkbox whenever the unfollow dialog closes
  // so a fresh unfollow starts with the safe default (off). A failed attempt
  // keeps the dialog open, and so keeps the choice for the retry.
  React.useEffect(() => {
    if (!removing) setDeleteFiles(false)
  }, [removing])

  // The server first removes the follow's queued and running jobs (killing its
  // running downloads); with ?files=true it then deletes each lesson's files
  // and tombstones every lesson whose files are all gone, and only then drops
  // the follow. A 500 or 409 keeps the follow, but by then the jobs are gone
  // and some lessons may already be tombstoned. So the refresh runs on FAILURE
  // too (finally): follows, summary, jobs, and the raw ["lessons"] prefix for
  // every keyed Lessons view. The dialog stays pending until they land. A 404
  // means it was removed elsewhere first: done, not a failure (itemOutcome).
  const unfollow = (id: number, files: boolean): Promise<ItemOutcome> =>
    itemOutcome(api.unfollow(id, { deleteFiles: files })).finally(() =>
      Promise.all([
        qc.invalidateQueries({ queryKey: qk.follows }),
        qc.invalidateQueries({ queryKey: qk.summary }),
        qc.invalidateQueries({ queryKey: qk.jobs() }),
        qc.invalidateQueries({ queryKey: ["lessons"] }),
      ]),
    )

  const openRowDialog = (set: (d: RowDialog) => void, follow: FollowDTO) =>
    set({ follow, order: (follows.data ?? []).map((f) => f.id) })

  // Where focus goes when a row's dialog closes: the button that opened it, the
  // same button on a neighbouring row when the follow is gone, else the page
  // heading.
  const rowReturn = (d: RowDialog | null, selector: (id: number) => string) => () =>
    d ? rowFocusTargets(d.order, d.follow.id, selector, headingRef.current) : [headingRef.current]

  return (
    <div className="flex flex-col gap-6">
      <div className="flex items-center justify-between gap-4">
        {/* tabIndex -1: the last place focus can return to when a dialog
            closes and neither its row nor a neighbour is left. */}
        {/* -mx-1.5 px-1.5: the ring gets room around the letters without
            moving the heading. */}
        <h1
          ref={headingRef}
          tabIndex={-1}
          className="-mx-1.5 rounded-md px-1.5 text-2xl font-bold outline-none focus-visible:ring-[3px] focus-visible:ring-ring/60"
        >
          Follows
        </h1>
        <Button ref={addButtonRef} size="sm" onClick={() => setAddOpen(true)}>
          <Plus />
          Add follow
        </Button>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Followed</CardTitle>
          <CardDescription>Nodes and instructors you track</CardDescription>
        </CardHeader>
        <CardContent>
          {follows.isPending || follows.isError ? (
            <QueryStatus
              loading={follows.isPending}
              error={follows.error}
              onRetry={() => follows.refetch()}
              fallbackMessage="Couldn't load the follows. Check that DrumDrop is running, then Retry."
            />
          ) : follows.data.length > 0 ? (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Title</TableHead>
                  <TableHead>Kind</TableHead>
                  <TableHead>Brand</TableHead>
                  <TableHead>Quality</TableHead>
                  <TableHead>Last synced</TableHead>
                  <TableHead className="w-0" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {follows.data.map((f) => (
                  // The title is the link to the follow's lessons (keyboard and
                  // screen readers); a click anywhere else on the row does the
                  // same for a pointer.
                  <TableRow
                    key={f.id}
                    className="cursor-pointer"
                    onClick={() => navigate(`/lessons?follow=${f.id}`)}
                  >
                    <TableCell className="font-medium">
                      <Link
                        to={`/lessons?follow=${f.id}`}
                        // The row's own click would navigate a second time.
                        onClick={(e) => e.stopPropagation()}
                        // -mx-1 px-1: the ring gets room around the letters
                        // without moving the title (the h1 treatment).
                        className="-mx-1 rounded-sm px-1 underline-offset-4 outline-none hover:underline focus-visible:ring-[3px] focus-visible:ring-ring/60"
                      >
                        {f.title}
                      </Link>
                    </TableCell>
                    <TableCell>
                      <Badge variant="outline" className="capitalize">
                        {f.kind}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-muted-foreground">{brandName(f.brand)}</TableCell>
                    <TableCell className="text-muted-foreground">{f.quality}</TableCell>
                    <TableCell className="text-muted-foreground">
                      {formatRelativeTime(f.last_synced_at)}
                    </TableCell>
                    <TableCell>
                      <div className="flex justify-end gap-3">
                        <Button
                          variant="ghost"
                          size="sm"
                          aria-label={`Edit ${f.title}`}
                          data-follow-edit={f.id}
                          onClick={(e) => {
                            e.stopPropagation()
                            openRowDialog(setEditing, f)
                          }}
                        >
                          <Pencil />
                          Edit
                        </Button>
                        <Button
                          variant="ghost"
                          size="sm"
                          aria-label={`Remove ${f.title}`}
                          data-follow-remove={f.id}
                          onClick={(e) => {
                            e.stopPropagation()
                            openRowDialog(setRemoving, f)
                          }}
                        >
                          <Trash2 />
                          Remove
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          ) : (
            <p className="text-sm text-muted-foreground">No follows yet</p>
          )}
        </CardContent>
      </Card>

      <AddFollowDialog
        open={addOpen}
        onOpenChange={setAddOpen}
        returnFocus={() => [addButtonRef.current, headingRef.current]}
      />

      <EditFollowDialog
        follow={editing?.follow ?? null}
        onOpenChange={(open) => {
          if (!open) setEditing(null)
        }}
        returnFocus={rowReturn(editing, editSelector)}
      />

      <ConfirmDialog
        open={removing !== null}
        onOpenChange={(open) => {
          if (!open) setRemoving(null)
        }}
        title={removing ? `Remove “${removing.follow.title}”?` : "Remove follow?"}
        description="Removing it stops its queued and running downloads, stops tracking it, and clears its lesson history. Downloaded files stay where they are unless you also delete them."
        // The button says what it will do, files included.
        confirmLabel={deleteFiles ? "Remove and delete files" : "Remove"}
        pendingLabel="Removing…"
        onConfirm={() =>
          removing
            ? unfollow(removing.follow.id, deleteFiles)
            : Promise.reject(new Error("the dialog has no follow"))
        }
        announce={(outcome) => {
          const description = removing?.follow.title
          if (outcome === "already-gone") toast.message("Already removed", { description })
          else toast.success("Follow removed", { description })
        }}
        failureTitle={`Couldn't remove “${removing?.follow.title ?? "the follow"}”`}
        returnFocus={rowReturn(removing, removeSelector)}
      >
        {({ pending }) => (
          // Left-aligned at every width, phones included, like the dialog's
          // failure message (InlineError) below it.
          <div className="flex items-center gap-2">
            <Checkbox
              id={deleteFilesId}
              checked={deleteFiles}
              disabled={pending}
              onCheckedChange={(c) => setDeleteFiles(c === true)}
            />
            <Label htmlFor={deleteFilesId} className="font-normal">
              Also delete downloaded files
            </Label>
          </div>
        )}
      </ConfirmDialog>
    </div>
  )
}
