import * as React from "react"
import { toast } from "sonner"
import { errorMessage } from "@/lib/errors"
import { focusFirst, type FocusTarget } from "@/lib/focus"

// ShownError is the failure a dialog shows inline. `seq` counts failures, so a
// repeat of the same message is still a new element for the live region.
export interface ShownError {
  message: string
  seq: number
}

export interface RunOptions<T> {
  // done handles a success: usually it closes the dialog (the caller's
  // onOpenChange(false)); with keepOpen it updates the dialog instead.
  done: (data: T) => void
  // keepOpen marks a step that leaves the dialog open (e.g. a preview): its
  // success clears the last failure and ends the pending state, and a result
  // that lands after the dialog was closed is dropped, since nobody is
  // waiting for it.
  keepOpen?: boolean
  // announce tells the user it worked (a toast). It runs once the dialog has
  // closed and focus has returned: while a modal is open Radix marks the rest
  // of the page aria-hidden, the toaster included, so a toast fired then is
  // shown but not heard.
  announce?: (data: T) => void
  // subject names what the request was about, for a failure that lands after
  // the dialog was closed and so can only be a toast.
  subject?: string
  // fallback replaces a failure that carries no server message.
  fallback?: string
}

// useDialogRequest is the request lifecycle every dialog that sends one
// shares: pending state, the failure shown inline while the dialog is open,
// focus returned to `returnFocus` when it closes, and the success announced
// after that. Closing detaches the request it started: its result then
// arrives as a toast instead of in a dialog that is gone (or that now shows
// something else).
//
// Wire `onCloseAutoFocus` into the dialog's content. `returnFocus` is read
// when the dialog closes, after any refresh a success awaited, so it can name
// controls a re-render may have removed; see focusFirst.
export function useDialogRequest({
  open,
  returnFocus,
}: {
  open: boolean
  returnFocus: () => FocusTarget[]
}) {
  const session = React.useRef(0)
  const inFlight = React.useRef<number | null>(null)
  const [pending, setPending] = React.useState(false)
  const [error, setError] = React.useState<ShownError | null>(null)
  const announcement = React.useRef<(() => void) | null>(null)
  // Kept from the last render while OPEN: by the time focus returns, the
  // caller has usually cleared the state its returnFocus reads (the row it
  // was opened from), so the closing render's returnFocus knows nothing.
  const targets = React.useRef(returnFocus)
  React.useLayoutEffect(() => {
    if (open) targets.current = returnFocus
  })

  // A closed dialog starts the next one clean.
  React.useEffect(() => {
    if (open) return
    session.current += 1
    inFlight.current = null
    setPending(false)
    setError(null)
  }, [open])

  const onCloseAutoFocus = React.useCallback((event: Event) => {
    event.preventDefault() // Radix would focus a DialogTrigger; these dialogs have none
    focusFirst(targets.current())
    const announce = announcement.current
    announcement.current = null
    announce?.()
  }, [])

  const run = React.useCallback(
    async <T>(request: () => Promise<T>, opts: RunOptions<T>): Promise<void> => {
      const mine = session.current
      if (inFlight.current === mine) return // a second press of the same request
      inFlight.current = mine
      setPending(true)
      let data: T
      try {
        data = await request()
      } catch (err) {
        const message = errorMessage(err, opts.fallback)
        if (mine !== session.current) {
          if (!opts.keepOpen) toast.error(message, { description: opts.subject })
          return
        }
        inFlight.current = null
        setPending(false)
        // The last message stays until the next failure replaces it or the
        // dialog closes, so a retry does not collapse and regrow the dialog.
        setError((prev) => ({ message, seq: (prev?.seq ?? 0) + 1 }))
        return
      }
      const announce = opts.announce
      if (mine !== session.current) {
        if (!opts.keepOpen) announce?.(data)
        return
      }
      if (opts.keepOpen) {
        inFlight.current = null
        setPending(false)
        setError(null)
        opts.done(data)
        return
      }
      // pending stays true until the dialog is gone: the button keeps its
      // "…ing" label rather than flashing back to the idle one.
      announcement.current = announce ? () => announce(data) : null
      opts.done(data)
    },
    [],
  )

  return { pending, error, run, onCloseAutoFocus }
}
