import * as React from "react"
import { Loader2Icon } from "lucide-react"
import { Button } from "@/components/ui/button"
import { StackedLabel } from "@/components/StackedLabel"
import { cn } from "@/lib/utils"

// PendingButton is a Button that starts a request. While `pending` it reads
// `pendingLabel` (with a spinner) and ignores presses, but it stays FOCUSABLE:
// it is aria-disabled, never `disabled`. A disabled button drops keyboard
// focus to <body> the moment the request starts, and Radix's focus trap does
// not take it back, so Enter on a retry would do nothing.
//
// Both labels are always laid out (StackedLabel), so the button is as wide
// idle as pending and its neighbours never shift when a request starts.
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
      <StackedLabel
        active={pending ? "pending" : "idle"}
        labels={{
          idle: children,
          pending: (
            <>
              {/* Laid out while idle too (it sizes the button); it only
                  spins while it is visible. */}
              <Loader2Icon
                data-icon="inline-start"
                aria-hidden="true"
                className={cn(pending && "animate-spin")}
              />
              {pendingLabel}
            </>
          ),
        }}
      />
    </Button>
  )
}
