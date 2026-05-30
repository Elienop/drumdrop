import * as React from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "react-router-dom"
import { Plus, Trash2 } from "lucide-react"
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
import { ConfirmDialog } from "@/components/ConfirmDialog"
import { QueryStatus } from "@/components/QueryState"
import { AddFollowDialog } from "@/pages/follows/AddFollowDialog"

export function Follows() {
  const qc = useQueryClient()
  const navigate = useNavigate()
  const follows = useQuery({ queryKey: qk.follows, queryFn: api.listFollows })
  const [addOpen, setAddOpen] = React.useState(false)
  const [removing, setRemoving] = React.useState<FollowDTO | null>(null)

  const remove = useMutation({
    mutationFn: (id: number) => api.deleteFollow(id),
    onSuccess: () => {
      toast.success("Follow removed")
      qc.invalidateQueries({ queryKey: qk.follows })
      qc.invalidateQueries({ queryKey: qk.summary })
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
                    <TableCell className="text-right">
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

      <ConfirmDialog
        open={removing !== null}
        onOpenChange={(open) => {
          if (!open) setRemoving(null)
        }}
        title={removing ? `Remove ${removing.title}?` : "Remove follow?"}
        description="This stops tracking it. Existing lessons stay on disk."
        confirmLabel="Remove"
        onConfirm={() => {
          if (removing) remove.mutate(removing.id)
        }}
      />
    </div>
  )
}
