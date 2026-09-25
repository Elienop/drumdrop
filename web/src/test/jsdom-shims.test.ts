// The stubs every test relies on (jsdom-shims.ts, loaded by setup.ts), held
// to the contract of the browser API each stands in for.

it("localStorage is one Storage, the same through window, and keeps what is set in it", () => {
  expect(window.localStorage).toBe(globalThis.localStorage)
  localStorage.clear()
  expect(localStorage).toHaveLength(0)

  localStorage.setItem("a", "1")
  localStorage.setItem("b", "2")
  expect(localStorage).toHaveLength(2)
  expect(localStorage.getItem("a")).toBe("1")
  expect(localStorage.getItem("missing")).toBeNull()
  expect([localStorage.key(0), localStorage.key(1)].sort()).toEqual(["a", "b"])
  expect(localStorage.key(2)).toBeNull()

  localStorage.removeItem("a")
  expect(localStorage).toHaveLength(1)
  expect(localStorage.getItem("a")).toBeNull()
  localStorage.clear()
  expect(localStorage).toHaveLength(0)
})

it("an element answers the pointer-capture and scroll calls Radix makes, without layout", () => {
  const el = document.createElement("div")
  expect(el.hasPointerCapture(1)).toBe(false)
  expect(() => {
    el.setPointerCapture(1)
    el.releasePointerCapture(1)
    el.scrollIntoView()
  }).not.toThrow()
})

it("a ResizeObserver can observe, unobserve and disconnect, and never reports a change", () => {
  const onResize = vi.fn()
  const observer = new ResizeObserver(onResize)
  const el = document.createElement("div")
  observer.observe(el)
  observer.unobserve(el)
  observer.disconnect()
  expect(onResize).not.toHaveBeenCalled()
})
