import * as React from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "react-router-dom"
import { Pencil, Plus, Trash2 } from "lucide-react"
import { toast } from "sonner"
import { api, ApiHttpError } from "@/lib/api"
import { qk } from "@/lib/queryKeys"
import { formatRelativeTime } from "@/lib/format"
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
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Checkbox } from "@/components/ui/checkbox"
import { Label } from "@/components/ui/label"
import { QueryStatus } from "@/components/QueryState"
import { AddFollowDialog } from "@/pages/follows/AddFollowDialog"
import { EditFollowDialog } from "@/pages/follows/EditFollowDialog"

export function Follows() {
  const qc = useQueryClient()
  const navigate = useNavigate()
  const follows = useQuery({ queryKey: qk.follows, queryFn: api.listFollows })
  const [addOpen, setAddOpen] = React.useState(false)
  const [editing, setEditing] = React.useState<FollowDTO | null>(null)
  const [removing, setRemoving] = React.useState<FollowDTO | null>(null)
  const [deleteFiles, setDeleteFiles] = React.useState(false)

  // Reset the "also delete files" checkbox whenever the unfollow dialog closes
  // so a fresh unfollow starts with the safe default (off).
  React.useEffect(() => {
    if (!removing) setDeleteFiles(false)
  }, [removing])

  const remove = useMutation({
    mutationFn: ({ id, files }: { id: number; files: boolean }) =>
      api.unfollow(id, { deleteFiles: files }),
    onSuccess: () => {
      toast.success("Follow removed")
      qc.invalidateQueries({ queryKey: qk.follows })
      qc.invalidateQueries({ queryKey: qk.summary })
      // A cascade unfollow drops the follow's lesson rows, so refresh any live
      // Lessons view (raw prefix covers every keyed variant).
      qc.invalidateQueries({ queryKey: ["lessons"] })
      setRemoving(null)
    },
    onError: (err) => {
      toast.error(err instanceof ApiHttpError ? err.message : "Remove failed")
    },
  })

  return (
    <div className="flex flex-col gap-6">
      <div className="flex items-center justify-between gap-4">
        <h1 className="text-2xl font-bold">Follows</h1>
        <Button size="sm" onClick={() => setAddOpen(true)}>
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
              fallbackMessage="Failed to load follows"
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
                  <TableRow
                    key={f.id}
                    className="cursor-pointer"
                    onClick={() => navigate(`/lessons?follow=${f.id}`)}
                  >
                    <TableCell className="font-medium">{f.title}</TableCell>
                    <TableCell>
                      <Badge variant="outline" className="capitalize">
                        {f.kind}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-muted-foreground">{f.brand}</TableCell>
                    <TableCell className="text-muted-foreground">{f.quality}</TableCell>
                    <TableCell className="text-muted-foreground">
                      {formatRelativeTime(f.last_synced_at)}
                    </TableCell>
                    <TableCell className="text-right whitespace-nowrap">
                      <Button
                        variant="ghost"
                        size="sm"
                        aria-label={`Edit ${f.title}`}
                        onClick={(e) => {
                          e.stopPropagation()
                          setEditing(f)
                        }}
                      >
                        <Pencil />
                        Edit
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        aria-label={`Remove ${f.title}`}
                        onClick={(e) => {
                          e.stopPropagation()
                          setRemoving(f)
                        }}
                      >
                        <Trash2 />
                        Remove
                      </Button>
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

      <AddFollowDialog open={addOpen} onOpenChange={setAddOpen} />

      <EditFollowDialog
        follow={editing}
        onOpenChange={(open) => {
          if (!open) setEditing(null)
        }}
      />

      <Dialog
        open={removing !== null}
        onOpenChange={(open) => {
          if (!open) setRemoving(null)
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {removing ? `Remove ${removing.title}?` : "Remove follow?"}
            </DialogTitle>
            <DialogDescription>
              This stops tracking it and clears its lesson history.
            </DialogDescription>
          </DialogHeader>

          <Label className="font-normal">
            <Checkbox
              checked={deleteFiles}
              onCheckedChange={(c) => setDeleteFiles(c === true)}
            />
            Also delete downloaded files
          </Label>

          <DialogFooter>
            <Button variant="outline" onClick={() => setRemoving(null)}>
              Cancel
            </Button>
            <Button
              variant="destructive"
              disabled={remove.isPending}
              onClick={() => {
                if (removing) remove.mutate({ id: removing.id, files: deleteFiles })
              }}
            >
              Remove
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
