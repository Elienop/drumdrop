import * as React from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { api } from "@/lib/api"
import { qk } from "@/lib/queryKeys"
import { useDialogRequest } from "@/lib/dialog-request"
import type { FocusTarget } from "@/lib/focus"
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
import { InlineError } from "@/components/InlineError"
import { PendingButton } from "@/components/PendingButton"
import { StackedLabel } from "@/components/StackedLabel"
import { useHeldWhileClosed, useOpenedNow } from "@/lib/use-held"
import { QUALITY_OPTIONS } from "@/pages/follows/AddFollowDialog"

// EditFollowDialog edits a follow's quality in place. Identity fields (kind,
// railcontent_id, slug, brand) are immutable; only the quality preset changes.
// The new quality is forward-only — it governs lessons queued from now on and
// does NOT re-download anything already on disk. Mounted controlled by the
// `follow` prop: non-null opens it, prefilled to that follow's current quality.
// A failed save shows inside the dialog, which stays open for a retry.
export function EditFollowDialog({
  follow,
  onOpenChange,
  returnFocus,
}: {
  follow: FollowDTO | null
  onOpenChange: (open: boolean) => void
  returnFocus: () => FocusTarget[]
}) {
  const qc = useQueryClient()
  const [quality, setQuality] = React.useState(follow?.quality ?? "best")
  const open = follow !== null
  const { pending, error, run, onCloseAutoFocus } = useDialogRequest({ open, returnFocus })
  const errorId = React.useId()
  // The page clears `follow` as the dialog closes; the title keeps naming it
  // while the dialog fades out.
  const shownFollow = useHeldWhileClosed(open, follow)

  // Reseed the Select to the follow's current quality each time it opens, in
  // the opening render (an effect would show the last choice for a frame). On
  // OPEN, not on a new follow object: reopening the same row passes the same
  // object, and the abandoned choice must not survive.
  if (useOpenedNow(open) && follow) setQuality(follow.quality)

  const save = () => {
    if (!follow) return
    void run(
      async () => {
        await api.updateFollow(follow.id, { quality })
        await Promise.all([
          qc.invalidateQueries({ queryKey: qk.follows }),
          qc.invalidateQueries({ queryKey: qk.summary }),
        ])
      },
      {
        done: () => onOpenChange(false),
        announce: () => toast.success("Quality updated", { description: follow.title }),
        failure: `Couldn't change the quality of “${follow.title}”`,
      },
    )
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      {/* flex, not the primitive's grid: see InlineError. */}
      <DialogContent
        onCloseAutoFocus={onCloseAutoFocus}
        className="flex max-h-[calc(100dvh-2rem)] flex-col overflow-y-auto"
      >
        <DialogHeader>
          {/* leading-snug: the primitive's leading-none makes a wrapped title's
              lines touch; pr-6 keeps a long one clear of the close button. */}
          <DialogTitle className="pr-6 leading-snug text-pretty wrap-break-word">
            {shownFollow ? `Edit “${shownFollow.title}”` : "Edit follow"}
          </DialogTitle>
          <DialogDescription>
            Change the download quality. Applies to lessons queued from now on.
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

        <InlineError id={errorId} error={error} stale={pending} />

        <DialogFooter>
          {/* "Close" while saving: closing does not cancel the save, its
              result then arrives as a notification. */}
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            <StackedLabel labels={{ cancel: "Cancel", close: "Close" }} active={pending ? "close" : "cancel"} />
          </Button>
          <PendingButton
            pending={pending}
            pendingLabel="Saving…"
            aria-describedby={error !== null && !pending ? errorId : undefined}
            onClick={save}
          >
            Save
          </PendingButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
