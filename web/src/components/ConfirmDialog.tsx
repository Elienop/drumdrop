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
import { StackedLabel } from "@/components/StackedLabel"
import { useDialogRequest } from "@/lib/dialog-request"
import type { FocusTarget } from "@/lib/focus"
import { useHeldWhileClosed, useOpenedNow } from "@/lib/use-held"
import { cn } from "@/lib/utils"

// RELEASE_AFTER_MS bounds how long a request may lock the dialog. A delete is
// the server killing a download and unlinking files: well under a second for
// one lesson, a few seconds for a follow with many lessons on a slow disk.
// Nothing on either side times the request out (no fetch timeout, no server
// write timeout), so a hung one would otherwise trap the user in the dialog
// for good. Past this point the dismiss button comes back as "Close" and the
// answer, when it lands, arrives as a toast.
export const RELEASE_AFTER_MS = 10_000

// VIEWPORT_MARGIN_PX keeps a dialog pinned by its bottom edge this far from the
// top of the viewport: past it the dialog scrolls instead of growing.
const VIEWPORT_MARGIN_PX = 16

export interface ConfirmDialogProps<T> {
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
  onConfirm: () => Promise<T>
  // announce reports a success (a toast); it runs after the dialog closed.
  announce: (result: T) => void
  // failureTitle names the item and the outcome, for a failure that lands
  // after the dialog was closed ("Couldn't delete “Six”").
  failureTitle: string
  // returnFocus lists where focus goes on close, most specific first.
  returnFocus: () => FocusTarget[]
  // Extra controls between the description and the error (e.g. a checkbox).
  // They are rendered with the pending state so they can disable themselves,
  // and get `confirm` so a form in them can submit on Enter; their state
  // lives with the caller and survives a failed attempt.
  children?: (state: { pending: boolean; confirm: () => void }) => React.ReactNode
  releaseAfterMs?: number
}

// ConfirmDialog asks before an action and then OWNS its request:
//
// - Pending: the confirm button reads `pendingLabel` and keeps focus (focus is
//   moved to it first, so a click in Safari, which does not focus buttons,
//   cannot leave focus on a control about to be disabled). Cancel and Escape
//   are locked until the answer lands or RELEASE_AFTER_MS passes; the slot's
//   controls stay disabled for the whole request (changing them then would
//   not change what was sent). A failure from the last attempt stays, muted.
// - Failure: the dialog stays open with the server's message and the confirm
//   button retries. The dismiss button then reads "Close": by then the server
//   may already have acted, and "Cancel" would suggest an undo.
// - Success: it closes, focus returns (see returnFocus), then `announce` runs.
// - Stable footer: on confirm the dialog is re-anchored by its bottom edge at
//   the position it already has, so a message appearing, changing or growing
//   pushes the header up instead of moving the buttons under the pointer.
//   Labels are stacked (StackedLabel), so no button changes width either.
// - Short screens: the dialog never runs past the viewport; it scrolls.
// - Closing: the dialog fades out showing exactly what it last showed (title,
//   labels, message, position); everything resets when it next opens.
export function ConfirmDialog<T>({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel,
  pendingLabel,
  confirmVariant = "destructive",
  onConfirm,
  announce,
  failureTitle,
  returnFocus,
  children,
  releaseAfterMs = RELEASE_AFTER_MS,
}: ConfirmDialogProps<T>) {
  const { pending, error, run, onCloseAutoFocus } = useDialogRequest({ open, returnFocus })
  const [released, setReleased] = React.useState(false)
  const [anchorBottom, setAnchorBottom] = React.useState<number | null>(null)
  const contentRef = React.useRef<HTMLDivElement>(null)
  const confirmRef = React.useRef<HTMLButtonElement>(null)
  const isOpen = React.useRef(open)
  const errorId = React.useId()

  React.useLayoutEffect(() => {
    isOpen.current = open
  })

  // A dialog opens centred and unlocked (reset on open, so a closing dialog
  // fades out where it is).
  if (useOpenedNow(open)) {
    setAnchorBottom(null)
    setReleased(false)
  }

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

  // A resize makes the frozen position stale: centre again.
  const anchored = anchorBottom !== null
  React.useEffect(() => {
    if (!anchored) return
    const recentre = () => setAnchorBottom(null)
    window.addEventListener("resize", recentre)
    return () => window.removeEventListener("resize", recentre)
  }, [anchored])

  const confirm = () => {
    // Read through a ref: a slot's form may hold a confirm from an earlier
    // render, and a press during the close animation must send nothing.
    if (!isOpen.current) return
    confirmRef.current?.focus()
    const content = contentRef.current
    if (content && anchorBottom === null) {
      setAnchorBottom(window.innerHeight - content.getBoundingClientRect().bottom)
    }
    void run(onConfirm, {
      done: () => onOpenChange(false),
      announce,
      failure: failureTitle,
    })
  }

  // What the dialog shows is held from its last open render while it closes:
  // the page clears the item it was opened for as soon as it closes.
  const shown = useHeldWhileClosed(open, {
    title,
    description,
    confirmLabel,
    pendingLabel,
    confirmVariant,
    slot: children?.({ pending, confirm }),
  })

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
        // flex, not the primitive's grid: an empty message region's
        // negative margin can cancel a flex gap, never a grid row's.
        className={cn(
          "flex max-h-[calc(100dvh-2rem)] flex-col overflow-y-auto",
          anchored && "top-auto translate-y-0",
        )}
        style={
          anchored
            ? {
                bottom: anchorBottom,
                maxHeight: window.innerHeight - anchorBottom - VIEWPORT_MARGIN_PX,
              }
            : undefined
        }
      >
        <AlertDialogHeader className="gap-2">
          <AlertDialogTitle className="leading-snug text-pretty wrap-break-word">
            {shown.title}
          </AlertDialogTitle>
          <AlertDialogDescription className="text-pretty">{shown.description}</AlertDialogDescription>
        </AlertDialogHeader>

        {shown.slot}

        <InlineError id={errorId} error={error} stale={pending} />
        <p role="status" className="text-center text-sm text-pretty text-muted-foreground empty:-mt-4 sm:text-left">
          {pending && released
            ? "This is taking longer than usual. You can close this dialog; the result will appear as a notification."
            : null}
        </p>

        <AlertDialogFooter>
          <AlertDialogCancel disabled={locked}>
            <StackedLabel
              labels={{ cancel: "Cancel", close: "Close" }}
              active={error !== null || released ? "close" : "cancel"}
            />
          </AlertDialogCancel>
          <PendingButton
            ref={confirmRef}
            variant={shown.confirmVariant}
            pending={pending}
            pendingLabel={shown.pendingLabel}
            // Not while a retry runs: the message is about the last attempt.
            aria-describedby={error !== null && !pending ? errorId : undefined}
            onClick={confirm}
          >
            {shown.confirmLabel}
          </PendingButton>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
