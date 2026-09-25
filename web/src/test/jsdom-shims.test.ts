// The stubs every test relies on (jsdom-shims.ts, loaded by setup.ts), held
// to the contract of the browser API each stands in for.
import { MemoryStorage } from "./jsdom-shims"

it("localStorage is the shim's own Storage, not the one the environment put there", () => {
  // vitest's jsdom environment puts jsdom's localStorage on the global as a
  // getter (populateGlobal); Node 26 has its own. The shim's is a plain value.
  const own = Object.getOwnPropertyDescriptor(globalThis, "localStorage")
  expect(own?.get).toBeUndefined()
  expect(own?.value).toBeInstanceOf(MemoryStorage)
  expect(localStorage).toBe(own?.value)
})

it("installs every stand-in over whatever is already there, never only if missing", async () => {
  // What setup.ts installed, put back afterwards.
  const saved = {
    localStorage: Object.getOwnPropertyDescriptor(globalThis, "localStorage")!,
    ResizeObserver: globalThis.ResizeObserver,
    hasPointerCapture: Element.prototype.hasPointerCapture,
    setPointerCapture: Element.prototype.setPointerCapture,
    releasePointerCapture: Element.prototype.releasePointerCapture,
    scrollIntoView: Element.prototype.scrollIntoView,
  }
  // Something already in each place, as a Node or jsdom that has the API
  // would leave there.
  const present = {
    localStorage: {} as Storage,
    ResizeObserver: class {} as unknown as typeof ResizeObserver,
    hasPointerCapture: () => true,
    setPointerCapture: () => {},
    releasePointerCapture: () => {},
    scrollIntoView: () => {},
  }
  try {
    Object.defineProperty(globalThis, "localStorage", {
      value: present.localStorage,
      configurable: true,
      writable: true,
    })
    globalThis.ResizeObserver = present.ResizeObserver
    Element.prototype.hasPointerCapture = present.hasPointerCapture
    Element.prototype.setPointerCapture = present.setPointerCapture
    Element.prototype.releasePointerCapture = present.releasePointerCapture
    Element.prototype.scrollIntoView = present.scrollIntoView

    // Run the module again, as setup.ts does before every test file.
    vi.resetModules()
    const fresh = await import("./jsdom-shims")

    expect(Object.getOwnPropertyDescriptor(globalThis, "localStorage")?.value).toBeInstanceOf(
      fresh.MemoryStorage,
    )
    expect(globalThis.ResizeObserver).not.toBe(present.ResizeObserver)
    expect(Element.prototype.hasPointerCapture).not.toBe(present.hasPointerCapture)
    expect(Element.prototype.setPointerCapture).not.toBe(present.setPointerCapture)
    expect(Element.prototype.releasePointerCapture).not.toBe(present.releasePointerCapture)
    expect(Element.prototype.scrollIntoView).not.toBe(present.scrollIntoView)
  } finally {
    Object.defineProperty(globalThis, "localStorage", saved.localStorage)
    globalThis.ResizeObserver = saved.ResizeObserver
    Element.prototype.hasPointerCapture = saved.hasPointerCapture
    Element.prototype.setPointerCapture = saved.setPointerCapture
    Element.prototype.releasePointerCapture = saved.releasePointerCapture
    Element.prototype.scrollIntoView = saved.scrollIntoView
  }
})

it("localStorage keeps what is set in it, as a Storage does", () => {
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
