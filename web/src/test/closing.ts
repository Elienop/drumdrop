import { fireEvent } from "@testing-library/react"
import { vi } from "vitest"

// With tw-animate-css loaded, Radix keeps a closing dialog mounted until its
// exit animation ends. jsdom loads no stylesheet, so Presence sees
// animationName "none" and unmounts at once: a test would pass whatever the
// dialog shows while closing. holdClosingOverlays reproduces Radix's
// condition by stubbing exactly that one property from data-state. Undo it
// with vi.unstubAllGlobals() in an afterEach.
export function holdClosingOverlays() {
  const real = globalThis.getComputedStyle.bind(globalThis)
  vi.stubGlobal("getComputedStyle", (el: Element, pseudo?: string | null) => {
    const style = real(el, pseudo ?? undefined)
    const state = el.getAttribute?.("data-state")
    if (state !== "open" && state !== "closed") return style
    // A live Proxy: Presence keeps the declaration from mount and reads it
    // again at close.
    return new Proxy(style, {
      get(target, prop) {
        if (prop === "animationName") {
          return el.getAttribute("data-state") === "closed" ? "x-out" : "x-in"
        }
        const value: unknown = Reflect.get(target, prop)
        return typeof value === "function" ? (value as () => unknown).bind(target) : value
      },
    })
  })
  // Presence matches the animationend's name with CSS.escape; jsdom has no CSS.
  vi.stubGlobal("CSS", { escape: (s: string) => s })
}

// finishClosing ends the exit animation of every closing overlay.
export function finishClosing() {
  for (const el of document.querySelectorAll('[data-state="closed"]')) {
    fireEvent(el, Object.assign(new Event("animationend"), { animationName: "x-out" }))
  }
}
