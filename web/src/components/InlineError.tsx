import { OctagonXIcon } from "lucide-react"
import type { ShownError } from "@/lib/dialog-request"
import { cn } from "@/lib/utils"

// InlineError shows a dialog's failure between its body and its buttons.
//
// The role="alert" region is ALWAYS rendered and only its content changes: a
// live region inserted together with its text is missed by some screen
// readers. The message is keyed by the failure's sequence number, so a repeat
// of the same text is a fresh node and is announced again. Point the confirm
// button's aria-describedby at `id` while `error` is set and no retry runs.
//
// Empty, it costs no space: empty:-mt-4 cancels the gap-4 before it. That only
// works in a FLEX column; a grid row cannot shrink below zero, so the dialog
// content around it must be `flex flex-col` (ConfirmDialog, AddFollowDialog
// and EditFollowDialog pass that). Never display:none it: the region would
// stop announcing.
//
// The message is left-aligned at every width, even below `sm` where the
// dialog header is centred: it can run to several lines, and centred lines of
// uneven length are hard to read. The icon is inline with the text, so it
// stays beside the first word however the message wraps. `stale` mutes a
// message while a retry is running: it describes the last attempt, not the
// one in progress.
export function InlineError({
  id,
  error,
  stale = false,
  className,
}: Readonly<{
  id: string
  error: ShownError | null
  stale?: boolean
  className?: string
}>) {
  return (
    <div id={id} role="alert" className={cn("empty:-mt-4", className)}>
      {error && (
        <p
          key={error.seq}
          data-stale={stale || undefined}
          className={cn(
            "text-left text-sm text-pretty text-destructive transition-colors",
            stale && "text-muted-foreground",
          )}
        >
          {/* The toaster's error icon, so an error is not told by colour alone. */}
          <OctagonXIcon aria-hidden="true" className="mr-1.5 inline-block size-4 align-[-3px]" />
          {error.message}
        </p>
      )}
    </div>
  )
}
