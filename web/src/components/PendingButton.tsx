import * as React from "react"
import { Loader2Icon, type LucideIcon } from "lucide-react"
import { Button } from "@/components/ui/button"
import { StackedLabel } from "@/components/StackedLabel"
import { cn } from "@/lib/utils"

// PendingButton is a Button that starts a request. While `pending` it ignores
// presses and shows a spinner, but it stays FOCUSABLE: it is aria-disabled,
// never `disabled`. A disabled button drops keyboard focus to <body> the
// moment the request starts, and Radix's focus trap does not take it back, so
// Enter on a retry would do nothing.
//
// Two ways to show the request, chosen by the props:
// - `pendingLabel` (a text button): the label becomes the spinner plus
//   pendingLabel ("Delete" → "Deleting…"). Both labels are always laid out
//   (StackedLabel), so the button is as wide idle as pending and its
//   neighbours never shift when a request starts.
// - `icon` (a button with an icon, owner's ruling 2026-09-24, (l)): the
//   spinner takes the icon's place and the label stays as it is, so nothing
//   extra is reserved. The icon and the label are the Button's own children,
//   as on a plain Button, so its icon padding (`has-[>svg]`) and its size's
//   gap apply unchanged.
type PendingProps =
  | { pendingLabel: string; icon?: never }
  | { icon: LucideIcon; pendingLabel?: never }

export function PendingButton({
  pending,
  pendingLabel,
  icon: Icon,
  onClick,
  className,
  children,
  ...props
}: React.ComponentProps<typeof Button> & { pending: boolean } & PendingProps) {
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
      {Icon ? (
        <>
          {pending ? (
            <Loader2Icon data-icon="inline-start" aria-hidden="true" className="animate-spin" />
          ) : (
            <Icon data-icon="inline-start" aria-hidden="true" />
          )}
          {children}
        </>
      ) : (
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
      )}
    </Button>
  )
}
