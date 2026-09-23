/// <reference types="node" />
import { readdirSync, readFileSync } from "node:fs"
import { resolve } from "node:path"
import { describe, expect, it } from "vitest"
import { buttonVariants } from "@/components/ui/button"

// Read from disk: the test config (css: false) blanks every CSS import,
// `?raw` included. (__dirname, not import.meta.url: under jsdom that is an
// http:// URL.)
const css = readFileSync(resolve(__dirname, "index.css"), "utf8")

// The owner's rulings on the theme (2026-09-23), checked as contrast
// INVARIANTS computed from the tokens in index.css, not only as strings: a
// later token change that breaks a ratio fails here even if it looks fine.

type RGB = [number, number, number]

// oklch → gamma-encoded sRGB (clipped), as a browser paints it.
function oklchToSrgb(L: number, C: number, H: number): RGB {
  const h = (H * Math.PI) / 180
  const a = C * Math.cos(h)
  const b = C * Math.sin(h)
  const l = (L + 0.3963377774 * a + 0.2158037573 * b) ** 3
  const m = (L - 0.1055613458 * a - 0.0638541728 * b) ** 3
  const s = (L - 0.0894841775 * a - 1.291485548 * b) ** 3
  const lin = [
    4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s,
    -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s,
    -0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s,
  ]
  return lin.map((x) => {
    const c = Math.min(1, Math.max(0, x))
    return c <= 0.0031308 ? 12.92 * c : 1.055 * c ** (1 / 2.4) - 0.055
  }) as RGB
}

const luminance = (c: RGB) => {
  const [r, g, b] = c.map((x) => (x <= 0.04045 ? x / 12.92 : ((x + 0.055) / 1.055) ** 2.4))
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}

// WCAG 2 contrast ratio.
const contrast = (x: RGB, y: RGB) => {
  const [hi, lo] = [luminance(x), luminance(y)].sort((p, q) => q - p)
  return (hi + 0.05) / (lo + 0.05)
}

// `fg` at `alpha` painted over `bg` (Tailwind's /60, /50).
const over = (fg: RGB, bg: RGB, alpha: number): RGB =>
  fg.map((v, i) => v * alpha + bg[i] * (1 - alpha)) as RGB

// token reads `--name: oklch(L C H)` from :root in index.css.
function token(name: string): RGB {
  const m = css.match(new RegExp(`--${name}:\\s*oklch\\(([\\d.]+) ([\\d.]+) ([\\d.]+)\\)`))
  if (!m) throw new Error(`--${name} is not an oklch() token in index.css`)
  return oklchToSrgb(Number(m[1]), Number(m[2]), Number(m[3]))
}

const WHITE: RGB = [1, 1, 1]

// The alphas the classes use, read from the classes themselves, so a class
// change that a ratio below does not survive fails it.
const destructiveClasses = buttonVariants({ variant: "destructive" }).split(/\s+/)
const alphaOf = (classes: string[], pattern: RegExp): number => {
  const hit = classes.map((c) => c.match(pattern)).find((m) => m !== null)
  if (!hit) throw new Error(`no class matches ${pattern}`)
  return Number(hit[1]) / 100
}
const RED_FILL = alphaOf(destructiveClasses, /^dark:bg-destructive\/(\d+)$/)
const RED_HOVER = alphaOf(destructiveClasses, /^dark:hover:bg-destructive\/(\d+)$/)
const RING_ALPHA = alphaOf(destructiveClasses, /^focus-visible:ring-ring\/(\d+)$/)

describe("theme tokens", () => {
  it("--destructive is shadcn's dark-theme red", () => {
    expect(css).toMatch(/--destructive:\s*oklch\(0\.704 0\.191 22\.216\)/)
  })

  it("red text (errors, a destructive menu item) reads at 4.5:1 on the background and on popovers", () => {
    expect(contrast(token("destructive"), token("background"))).toBeGreaterThanOrEqual(4.5)
    expect(contrast(token("destructive"), token("popover"))).toBeGreaterThanOrEqual(4.5)
  })

  it("white text on a destructive button (dark:bg-destructive/60) reads at 4.5:1, and on its hover fill too", () => {
    for (const alpha of [RED_FILL, RED_HOVER]) {
      const fill = over(token("destructive"), token("background"), alpha)
      expect(contrast(WHITE, fill)).toBeGreaterThanOrEqual(4.5)
    }
  })

  // The ring is painted around a control, over whatever surface the control
  // sits on: the page, a card, a popover (menus, selects) or the muted tab
  // list. It must show at 3:1 against each of them.
  it.each(["background", "card", "popover", "muted"])(
    "the focus ring (ring-ring/60) shows at 3:1 against %s",
    (surface) => {
      const ring = over(token("ring"), token(surface), RING_ALPHA)
      expect(contrast(ring, token(surface))).toBeGreaterThanOrEqual(3)
    },
  )
})

describe("destructive buttons", () => {
  it("use the same amber focus ring as every other control, not a red one", () => {
    expect(destructiveClasses).toContain("focus-visible:ring-ring/60")
    expect(
      destructiveClasses.filter((c) => /ring-destructive/.test(c) && /focus-visible/.test(c)),
    ).toEqual([])
  })

  it("set the ring 2px off the fill, over the background, so it reads as a ring and not a fatter button", () => {
    expect(destructiveClasses).toContain("focus-visible:ring-offset-2")
    expect(destructiveClasses).toContain("focus-visible:ring-offset-background")
  })

  it("change on hover in dark mode: the dark hover fill differs from the dark fill", () => {
    // hover:bg-destructive/90 alone loses to dark:bg-destructive/60.
    expect(RED_HOVER).not.toBe(RED_FILL)
  })
})

// Every focus ring in the app uses the one amber at 60%: a primitive
// re-added from upstream brings back ring-ring/50, which is under 3:1 on
// cards and tab lists.
describe("focus ring everywhere", () => {
  const sources = (readdirSync(__dirname, { recursive: true }) as string[])
    .filter((f) => /\.tsx?$/.test(f) && !/\.test\.tsx?$/.test(f))
    .map((f) => ({ file: f, text: readFileSync(resolve(__dirname, f), "utf8") }))

  it("is ring-ring/60 in every source file that draws one", () => {
    const users = sources.filter((s) => /ring-ring\//.test(s.text))
    // Positive control: the scan reaches the primitives and the pages.
    expect(users.map((s) => s.file)).toEqual(
      expect.arrayContaining([
        expect.stringMatching(/button\.tsx$/),
        expect.stringMatching(/tabs\.tsx$/),
        expect.stringMatching(/Follows\.tsx$/),
      ]),
    )
    const other = sources.flatMap((s) =>
      [...s.text.matchAll(/ring-ring\/(\d+)/g)]
        .filter((m) => m[1] !== "60")
        .map((m) => `${s.file}: ${m[0]}`),
    )
    expect(other).toEqual([])
  })
})

describe("motion", () => {
  it("loads tw-animate-css, the source of the overlays' animate-in/out classes", () => {
    expect(css).toMatch(/@import\s+"tw-animate-css";/)
  })

  it("under reduced motion, overlays keep their fade but lose zoom and slide", () => {
    const block = css.match(/@media \(prefers-reduced-motion: reduce\)\s*\{([\s\S]*?)\n\}/)
    expect(block).not.toBeNull()
    const body = block![1]
    for (const v of [
      "--tw-enter-scale: 1 !important",
      "--tw-exit-scale: 1 !important",
      "--tw-enter-translate-x: 0 !important",
      "--tw-enter-translate-y: 0 !important",
      "--tw-exit-translate-x: 0 !important",
      "--tw-exit-translate-y: 0 !important",
    ]) {
      expect(body).toContain(v)
    }
    // The fade stays: opacity is not neutralised.
    expect(body).not.toMatch(/opacity/)
  })
})
