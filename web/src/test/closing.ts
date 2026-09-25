import { fireEvent } from "@testing-library/react"
import { vi } from "vitest"

// With tw-animate-css loaded, Radix keeps a closing dialog mounted until its
// exit animation ends. jsdom loads no stylesheet, so Presence sees
// animationName "none" and unmounts at once: a test would pass whatever the
// dialog shows while closing. holdClosingOverlays reproduces Radix's
// condition by stubbing exactly that one property from data-state. Undo it
// with vi.unstubAllGlobals() in an afterEach.
//
// One class opts a closed element out of its fade:
// `data-[state=closed]:animate-none!`, built as `animation:none!important`
// (RowMenu in pages/Lessons.tsx). The stub reads it live, as the browser
// does, so Presence sees "none" only if the class is on the element when it
// reads the animation, in the commit that closes it
// (@radix-ui/react-presence, usePresence's layout effect). A class that
// arrives a commit later leaves the element closing, and stuck: Presence's
// animationend check then no longer matches.
export function holdClosingOverlays() {
  const real = globalThis.getComputedStyle.bind(globalThis)
  vi.stubGlobal("getComputedStyle", (el: Element, pseudo?: string | null) => {
    const style = real(el, pseudo ?? undefined)
    // An HTML, SVG or MathML element has a live dataset (the DOM's Element
    // type does not declare it); Radix's overlays are HTML elements.
    const { dataset } = el as Element & Partial<HTMLOrSVGElement>
    const state = dataset?.state
    if (state !== "open" && state !== "closed") return style
    // A live Proxy: Presence keeps the declaration from mount and reads it
    // again at close.
    return new Proxy(style, {
      get(target, prop) {
        if (prop === "animationName") {
          if (dataset?.state !== "closed") return "x-in"
          // `animation: none !important` on a closed element, as the built CSS has it
          return el.classList.contains("data-[state=closed]:animate-none!") ? "none" : "x-out"
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
