import { OctagonXIcon } from "lucide-react"
import type { ShownError } from "@/lib/dialog-request"
import { cn } from "@/lib/utils"

// InlineError shows a dialog's failure between its body and its buttons.
//
// The role="alert" region is ALWAYS rendered and only its content changes: a
// live region inserted together with its text is missed by some screen
// readers. The message is keyed by the failure's sequence number, so a repeat
// of the same text is a fresh node and is announced again. While empty the
// region collapses into the dialog's gap-4 (empty:-mt-4), so it costs no
// space. Point the confirm button's aria-describedby at `id` while `error` is
// set.
export function InlineError({
  id,
  error,
  className,
}: {
  id: string
  error: ShownError | null
  className?: string
}) {
  return (
    <div id={id} role="alert" className={cn("empty:-mt-4", className)}>
      {error && (
        <p
          key={error.seq}
          className="flex items-start justify-center gap-2 text-center text-sm text-pretty text-destructive sm:justify-start sm:text-left"
        >
          {/* The toaster's error icon, so an error is not told by colour alone. */}
          <OctagonXIcon aria-hidden="true" className="mt-0.5 size-4 shrink-0" />
          <span>{error.message}</span>
        </p>
      )}
    </div>
  )
}
