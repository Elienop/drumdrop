import { expect, it } from "vitest"
import { render, screen } from "@testing-library/react"
import { PendingButton } from "./PendingButton"

// A text PendingButton lays out both labels (StackedLabel). The space between
// the spinner and "Saving…" must be the button's own, which its size sets
// (gap-2, gap-1.5 at sm): a fixed gap-2 inside made a small button wider than
// a plain small button. jsdom computes no layout, so this pins the classes;
// the browser pass measures the widths.
it("a text PendingButton's labels take the button's own gap, not a fixed one", () => {
  render(
    <PendingButton size="sm" pending={false} pendingLabel="Saving…">
      Save
    </PendingButton>,
  )
  const btn = screen.getByRole("button", { name: "Save" })
  expect(btn.className.split(/\s+/)).toContain("gap-1.5")
  const boxes = [btn.querySelector(".inline-grid")!, ...btn.querySelectorAll("[data-label]")]
  expect(boxes).toHaveLength(3)
  for (const box of boxes) {
    const gaps = box.className.split(/\s+/).filter((c) => /^gap-/.test(c))
    expect(gaps).toEqual(["gap-[inherit]"])
  }
})
