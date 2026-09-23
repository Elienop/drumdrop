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
    first = result.current.run(request, { done })
    second = result.current.run(request, { done })
  })
  expect(request).toHaveBeenCalledTimes(1)

  await act(async () => {
    finish()
    await Promise.all([first, second])
  })
  expect(done).toHaveBeenCalledTimes(1)
})
