import * as React from "react"

// useHeldWhileClosed returns `value` while `open`, and the value of the last
// open render once closed.
//
// A Radix dialog stays mounted for its close animation (Presence waits for
// the exit animation to end). The page usually clears what the dialog shows
// the moment it closes (`setDeleting(null)`), so without this the title,
// labels and slot contents would visibly fall back to their empty-state
// values while the dialog fades out.
export function useHeldWhileClosed<T>(open: boolean, value: T): T {
  const held = React.useRef(value)
  React.useLayoutEffect(() => {
    if (open) held.current = value
  })
  return open ? value : held.current
}

// useOpenedNow reports the render in which `open` turned true, so a dialog
// can reset its state as it OPENS rather than as it closes. Resetting on
// close would show the reset during the close animation; resetting in an
// effect after the open would show the previous session for one frame. This
// is a render-time state adjustment, so the first open frame is already
// clean.
export function useOpenedNow(open: boolean): boolean {
  const [wasOpen, setWasOpen] = React.useState(open)
  if (open !== wasOpen) {
    setWasOpen(open)
    return open
  }
  return false
}
