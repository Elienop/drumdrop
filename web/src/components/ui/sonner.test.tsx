import { afterEach, expect, it } from "vitest"
import { act, render, screen } from "@testing-library/react"
import { toast } from "sonner"
import { failureToast } from "@/lib/errors"
import { Toaster } from "./sonner"

// jsdom loads no CSS (css: false), so these pin the hooks the look is built
// from: the classes sonner puts on the toast and its close button, and the
// variables it positions the close button with. The browser pass checks the
// look itself.

afterEach(() => act(() => toast.dismiss()))

const RING = ["outline-none", "focus-visible:ring-[3px]", "focus-visible:ring-ring/60"]

async function stickyFailure() {
  render(<Toaster richColors />)
  act(() => failureToast("Couldn't cancel the job", "A sentence from the server."))
  const close = await screen.findByRole("button", { name: "Close toast" })
  const toastEl = close.closest<HTMLElement>("[data-sonner-toast]")!
  return { close, toastEl }
}

it("a focused toast and its close button show the one amber ring, on keyboard focus only", async () => {
  const { close, toastEl } = await stickyFailure()
  expect(toastEl.className.split(/\s+/)).toEqual(expect.arrayContaining(RING))
  expect(close.className.split(/\s+/)).toEqual(expect.arrayContaining(RING))
  expect(`${toastEl.className} ${close.className}`).not.toMatch(/(^|\s)focus:/)
})

it("the close × sits inside the top-right corner, level with the title, as a plain button", async () => {
  const { close, toastEl } = await stickyFailure()
  const toaster = toastEl.closest<HTMLElement>("[data-sonner-toaster]")!
  // sonner's own position hooks: from the right edge, not the left, and not
  // pushed half outside by a translate.
  expect(toaster.style.getPropertyValue("--toast-close-button-start")).toBe("auto")
  expect(toaster.style.getPropertyValue("--toast-close-button-end")).toBe("0.75rem")
  expect(toaster.style.getPropertyValue("--toast-close-button-transform")).toBe("none")
  // On the title's line (the toast's 16px padding), without the round badge.
  expect(close.className.split(/\s+/)).toEqual(
    expect.arrayContaining(["top-4", "border-0", "rounded-xs"]),
  )
  // The text keeps clear of it.
  expect(toastEl.className.split(/\s+/)).toContain("has-[[data-close-button]]:pr-10")
})
