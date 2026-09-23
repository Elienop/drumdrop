import * as React from "react"
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { InlineError } from "@/components/InlineError"
import { PendingButton } from "@/components/PendingButton"
import { useDialogRequest } from "@/lib/dialog-request"
import type { FocusTarget } from "@/lib/focus"
import { cn } from "@/lib/utils"

// RELEASE_AFTER_MS bounds how long a request may lock the dialog. A delete is
// the server killing a download and unlinking files: well under a second for
// one lesson, a few seconds for a follow with many lessons on a slow disk.
// Nothing on either side times the request out (no fetch timeout, no server
// write timeout), so a hung one would otherwise trap the user in the dialog
// for good. Past this point the dismiss button comes back as "Close" and the
// answer, when it lands, arrives as a toast.
export const RELEASE_AFTER_MS = 10_000

export interface ConfirmDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: React.ReactNode
  description: React.ReactNode
  // The confirm button's label, and its label while the request runs ("…").
  confirmLabel: string
  pendingLabel: string
  confirmVariant?: "default" | "destructive"
  // onConfirm sends the request. Resolve for success, reject for a failure
  // (its ApiHttpError message is shown verbatim). Include any refresh the
  // views need in the promise, on both outcomes: the dialog stays pending
  // until it settles, so the answer and the refreshed lists land together.
  onConfirm: () => Promise<unknown>
  // announce reports a success (a toast); it runs after the dialog closed.
  announce: () => void
  // subject names the item, for a result that lands after the dialog closed.
  subject?: string
  // returnFocus lists where focus goes on close, most specific first.
  returnFocus: () => FocusTarget[]
  // Extra controls between the description and the error (e.g. a checkbox).
  // They are rendered with the pending state so they can disable themselves;
  // their state lives with the caller and survives a failed attempt.
  children?: (state: { pending: boolean }) => React.ReactNode
  releaseAfterMs?: number
}

// ConfirmDialog asks before an action and then OWNS its request:
//
// - Pending: the confirm button reads `pendingLabel` and keeps focus (focus is
//   moved to it first, so a click in Safari, which does not focus buttons,
//   cannot leave focus on a control about to be disabled). Cancel and Escape
//   are locked until the answer lands or RELEASE_AFTER_MS passes; the slot's
//   controls stay disabled for the whole request (changing them then would
//   not change what was sent).
// - Failure: the dialog stays open with the server's message and the confirm
//   button retries. The dismiss button then reads "Close": by then the server
//   may already have acted, and "Cancel" would suggest an undo.
// - Success: it closes, focus returns (see returnFocus), then `announce` runs.
// - Stable footer: on confirm the dialog is re-anchored by its bottom edge at
//   the position it already has, so a message appearing, changing or growing
//   pushes the header up instead of moving the buttons under the pointer.
export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel,
  pendingLabel,
  confirmVariant = "destructive",
  onConfirm,
  announce,
  subject,
  returnFocus,
  children,
  releaseAfterMs = RELEASE_AFTER_MS,
}: ConfirmDialogProps) {
  const { pending, error, run, onCloseAutoFocus } = useDialogRequest({ open, returnFocus })
  const [released, setReleased] = React.useState(false)
  const [anchorBottom, setAnchorBottom] = React.useState<number | null>(null)
  const contentRef = React.useRef<HTMLDivElement>(null)
  const confirmRef = React.useRef<HTMLButtonElement>(null)
  const errorId = React.useId()

  const locked = pending && !released

  // The lock is released after releaseAfterMs of one request.
  React.useEffect(() => {
    if (!pending) {
      setReleased(false)
      return
    }
    const timer = window.setTimeout(() => setReleased(true), releaseAfterMs)
    return () => window.clearTimeout(timer)
  }, [pending, releaseAfterMs])

  // A closed dialog opens centred again.
  React.useEffect(() => {
    if (!open) setAnchorBottom(null)
  }, [open])

  // A resize makes the frozen position stale: centre again.
  const anchored = anchorBottom !== null
  React.useEffect(() => {
    if (!anchored) return
    const recentre = () => setAnchorBottom(null)
    window.addEventListener("resize", recentre)
    return () => window.removeEventListener("resize", recentre)
  }, [anchored])

  const confirm = () => {
    confirmRef.current?.focus()
    const content = contentRef.current
    if (content && anchorBottom === null) {
      setAnchorBottom(window.innerHeight - content.getBoundingClientRect().bottom)
    }
    void run(onConfirm, {
      done: () => onOpenChange(false),
      announce,
      subject,
    })
  }

  return (
    <AlertDialog
      open={open}
      onOpenChange={(next) => {
        if (!next && locked) return // Escape and Cancel wait for the answer
        onOpenChange(next)
      }}
    >
      <AlertDialogContent
        ref={contentRef}
        onCloseAutoFocus={onCloseAutoFocus}
        data-anchored={anchored ? "bottom" : undefined}
        className={cn(anchored && "top-auto translate-y-0")}
        style={anchored ? { bottom: anchorBottom } : undefined}
      >
        <AlertDialogHeader>
          <AlertDialogTitle className="text-pretty wrap-break-word">{title}</AlertDialogTitle>
          <AlertDialogDescription className="text-pretty">{description}</AlertDialogDescription>
        </AlertDialogHeader>

        {children?.({ pending })}

        <InlineError id={errorId} error={error} />
        <p role="status" className="text-center text-sm text-pretty text-muted-foreground empty:-mt-4 sm:text-left">
          {pending && released
            ? "This is taking longer than usual. You can close this dialog; the result will appear as a notification."
            : null}
        </p>

        <AlertDialogFooter>
          <AlertDialogCancel disabled={locked}>
            {error !== null || released ? "Close" : "Cancel"}
          </AlertDialogCancel>
          <PendingButton
            ref={confirmRef}
            variant={confirmVariant}
            pending={pending}
            pendingLabel={pendingLabel}
            aria-describedby={error !== null ? errorId : undefined}
            onClick={confirm}
          >
            {confirmLabel}
          </PendingButton>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
