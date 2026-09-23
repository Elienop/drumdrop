import * as React from "react"
import { cn } from "@/lib/utils"

// StackedLabel shows one of a button's labels in a box as wide as the widest,
// so the button keeps its width when the label changes ("Delete" →
// "Deleting…", "Cancel" → "Close") and the buttons beside it in a
// right-aligned footer do not slide under the pointer.
//
// Every label sits in the same grid cell; the inactive ones are invisible and
// aria-hidden, so they size the box but are neither seen nor part of the
// button's accessible name.
export function StackedLabel<K extends string>({
  labels,
  active,
}: {
  labels: Record<K, React.ReactNode>
  active: K
}) {
  return (
    <span className="inline-grid">
      {(Object.keys(labels) as K[]).map((key) => (
        <span
          key={key}
          aria-hidden={key !== active || undefined}
          data-label={key}
          className={cn(
            "col-start-1 row-start-1 inline-flex items-center justify-center gap-2",
            key !== active && "invisible",
          )}
        >
          {labels[key]}
        </span>
      ))}
    </span>
  )
}
