import * as React from "react"
import { Loader2Icon } from "lucide-react"
import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"

// PendingButton is a Button that starts a request. While `pending` it reads
// `pendingLabel` (with a spinner) and ignores presses, but it stays FOCUSABLE:
// it is aria-disabled, never `disabled`. A disabled button drops keyboard
// focus to <body> the moment the request starts, and Radix's focus trap does
// not take it back, so Enter on a retry would do nothing.
export function PendingButton({
  pending,
  pendingLabel,
  onClick,
  className,
  children,
  ...props
}: React.ComponentProps<typeof Button> & {
  pending: boolean
  pendingLabel: string
}) {
  return (
    <Button
      {...props}
      aria-disabled={pending || undefined}
      className={cn("aria-disabled:cursor-progress", className)}
      onClick={(e) => {
        if (pending) {
          e.preventDefault()
          return
        }
        onClick?.(e)
      }}
    >
      {pending ? (
        <>
          <Loader2Icon data-icon="inline-start" aria-hidden="true" className="animate-spin" />
          {pendingLabel}
        </>
      ) : (
        children
      )}
    </Button>
  )
}
