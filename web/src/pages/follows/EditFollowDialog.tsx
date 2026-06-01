import * as React from "react"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { api, ApiHttpError } from "@/lib/api"
import { qk } from "@/lib/queryKeys"
import type { FollowDTO } from "@/types"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { QUALITY_OPTIONS } from "@/pages/follows/AddFollowDialog"

// EditFollowDialog edits a follow's quality in place. Identity fields (kind,
// railcontent_id, slug, brand) are immutable; only the quality preset changes.
// The new quality is forward-only — it governs lessons enqueued from now on and
// does NOT re-download anything already on disk. Mounted controlled by the
// `follow` prop: non-null opens it, prefilled to that follow's current quality.
export function EditFollowDialog({
  follow,
  onOpenChange,
}: {
  follow: FollowDTO | null
  onOpenChange: (open: boolean) => void
}) {
  const qc = useQueryClient()
  const [quality, setQuality] = React.useState("best")

  // Reseed the Select to the follow's current quality each time it opens.
  React.useEffect(() => {
    if (follow) setQuality(follow.quality)
  }, [follow])

  const update = useMutation({
    mutationFn: (id: number) => api.updateFollow(id, { quality }),
    onSuccess: () => {
      toast.success("Quality updated")
      qc.invalidateQueries({ queryKey: qk.follows })
      qc.invalidateQueries({ queryKey: qk.summary })
      onOpenChange(false)
    },
    onError: (err) => {
      toast.error(err instanceof ApiHttpError ? err.message : "Update failed")
    },
  })

  return (
    <Dialog open={follow !== null} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{follow ? `Edit ${follow.title}` : "Edit follow"}</DialogTitle>
          <DialogDescription>
            Change the download quality. Applies to lessons enqueued from now on.
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-2">
          <Label htmlFor="edit-follow-quality">Quality</Label>
          <Select value={quality} onValueChange={setQuality}>
            <SelectTrigger id="edit-follow-quality" aria-label="Quality">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectGroup>
                {QUALITY_OPTIONS.map((q) => (
                  <SelectItem key={q} value={q}>
                    {q}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            disabled={update.isPending}
            onClick={() => {
              if (follow) update.mutate(follow.id)
            }}
          >
            Save
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
