// A FocusTarget is an element, or a CSS selector resolved when focus moves
// (so it finds the element a re-render put there, not a stale one).
export type FocusTarget = HTMLElement | string | null | undefined

// focusFirst focuses the first target that is still in the document and
// accepts focus, and returns it; null when none did. Order the targets from
// most to least specific: the control that opened a dialog, then its stable
// neighbours, then a heading that can always take focus.
export function focusFirst(targets: FocusTarget[]): HTMLElement | null {
  for (const target of targets) {
    const el =
      typeof target === "string" ? document.querySelector<HTMLElement>(target) : target
    if (!el || !el.isConnected || el.matches(":disabled")) continue
    el.focus()
    if (document.activeElement === el) return el
  }
  return null
}

// rowFocusTargets is where focus goes when a dialog opened from a table row
// closes: that row's control, else the row after it, else the row before it
// (the row may have left the list, e.g. a deleted lesson in a status tab),
// else `fallback`. `ids` is the row order captured when the dialog opened, and
// `selector` maps a row id to its control.
export function rowFocusTargets<Id>(
  ids: readonly Id[],
  id: Id,
  selector: (id: Id) => string,
  fallback: FocusTarget,
): FocusTarget[] {
  const i = ids.indexOf(id)
  const after = i >= 0 ? ids.slice(i + 1, i + 2) : []
  const before = i > 0 ? ids.slice(i - 1, i) : []
  return [selector(id), ...after.map(selector), ...before.map(selector), fallback]
}
