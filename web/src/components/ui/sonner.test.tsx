import { afterEach, expect, it } from "vitest"
import { act, render, screen } from "@testing-library/react"
import { toast } from "sonner"
import { failureToast } from "@/lib/errors"
import { Toaster } from "./sonner"

// WHAT THESE DO NOT CHECK: the look. jsdom loads no CSS (css: false) and has
// no cascade layers, so a class here can be present and still lose to
// sonner's own unlayered stylesheet in a browser (that is how the × kept its
// round badge while these passed, round 5). They pin only the hooks the look
// is built from: the classes sonner puts on the toast and its close button,
// with the "!" that lets them beat sonner's CSS (see sonner.tsx), and the
// variables it positions the close button with. Whether the × really sits
// level with the title, borderless, and whether the ring really shows, is for
// the browser pass: read computed styles, not class names.

afterEach(() => act(() => toast.dismiss()))

// The ring's box-shadow must be !important: sonner's focus box-shadow is
// unlayered and would win over a plain class.
const RING = ["outline-none", "focus-visible:ring-[3px]!", "focus-visible:ring-ring/60"]

async function stickyFailure() {
  render(<Toaster richColors />)
  act(() => failureToast("Couldn't cancel the job", "A sentence from the server."))
  const close = await screen.findByRole("button", { name: "Close toast" })
  const toastEl = close.closest<HTMLElement>("[data-sonner-toast]")!
  return { close, toastEl }
}

it("the toast and its close button carry the one focus ring's classes, !important, on keyboard focus only", async () => {
  const { close, toastEl } = await stickyFailure()
  expect(toastEl.className.split(/\s+/)).toEqual(expect.arrayContaining(RING))
  expect(close.className.split(/\s+/)).toEqual(expect.arrayContaining(RING))
  expect(`${toastEl.className} ${close.className}`).not.toMatch(/(^|\s)focus:/)
})

it("the close button carries the classes and variables that place it top-right without the badge, !important where sonner sets the same property", async () => {
  const { close, toastEl } = await stickyFailure()
  const toaster = toastEl.closest<HTMLElement>("[data-sonner-toaster]")!
  // sonner's own position hooks: from the right edge, not the left, and not
  // pushed half outside by a translate. Custom properties, so no "!" needed.
  expect(toaster.style.getPropertyValue("--toast-close-button-start")).toBe("auto")
  expect(toaster.style.getPropertyValue("--toast-close-button-end")).toBe("0.75rem")
  expect(toaster.style.getPropertyValue("--toast-close-button-transform")).toBe("none")
  // top, border and border-radius are all set by sonner too.
  expect(close.className.split(/\s+/)).toEqual(
    expect.arrayContaining(["top-4!", "border-0!", "rounded-xs!"]),
  )
  // The room kept clear on the right: sonner sets the toast's padding too.
  expect(toastEl.className.split(/\s+/)).toContain("has-[[data-close-button]]:pr-10!")
})

// The error icon sits level with the title's first line, like the ×, not
// centred against a wrapped title or a title and its sentence. sonner sets
// align-items, so the toast's items-start needs the "!"; it sets no
// margin-top, so the icon's needs none. The browser pass checks the result.
it("the icon is set on the title's first line: the toast aligns to the top, the icon centred on that line", async () => {
  const { toastEl } = await stickyFailure()
  expect(toastEl.className.split(/\s+/)).toContain("items-start!")
  const icon = toastEl.querySelector<HTMLElement>("[data-icon]")!
  expect(icon.className.split(/\s+/)).toContain("mt-[calc((1.5em_-_16px)/2)]")
})
