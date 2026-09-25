import { expect, it, vi } from "vitest"
import { act, renderHook } from "@testing-library/react"
import { useDialogRequest } from "./dialog-request"

it("a second run of the same open dialog while the first is in flight sends nothing", async () => {
  const { result } = renderHook(() => useDialogRequest({ open: true, returnFocus: () => [] }))
  let finish: () => void = () => {}
  const request = vi.fn(() => new Promise<void>((resolve) => (finish = resolve)))
  const done = vi.fn()

  // Both presses land before React re-renders with pending=true, so only the
  // hook itself can tell that the first is still running.
  let first: Promise<void> = Promise.resolve()
  let second: Promise<void> = Promise.resolve()
  act(() => {
    first = result.current.run(request, { done, failure: "Couldn't do it" })
    second = result.current.run(request, { done, failure: "Couldn't do it" })
  })
  expect(request).toHaveBeenCalledTimes(1)

  await act(async () => {
    finish()
    await Promise.all([first, second])
  })
  expect(done).toHaveBeenCalledTimes(1)
})

it("a closed dialog sends nothing, even through a handler captured while it was open", async () => {
  const { result, rerender } = renderHook(
    ({ open }) => useDialogRequest({ open, returnFocus: () => [] }),
    { initialProps: { open: true } },
  )
  const captured = result.current.run // e.g. a held slot's form submit
  rerender({ open: false })
  const request = vi.fn(() => Promise.resolve())
  await act(async () => {
    await captured(request, { done: vi.fn(), failure: "Couldn't do it" })
  })
  expect(request).not.toHaveBeenCalled()
  expect(result.current.pending).toBe(false)
})
