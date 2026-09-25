/// <reference types="node" />
import { readdirSync, readFileSync } from "node:fs"
import { resolve } from "node:path"
import { createElement } from "react"
import { describe, expect, it } from "vitest"
import { render, screen } from "@testing-library/react"
import { MemoryRouter } from "react-router-dom"
import { buttonVariants } from "@/components/ui/button"
import { DialogFooter } from "@/components/ui/dialog"
import { AlertDialogFooter } from "@/components/ui/alert-dialog"
import { Sidebar } from "@/components/app-shell/Sidebar"
import { Checkbox } from "@/components/ui/checkbox"
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs"

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
    const users = sources.filter((s) => /ring-ring\b/.test(s.text))
    // Positive control: the scan reaches the primitives and the pages.
    expect(users.map((s) => s.file)).toEqual(
      expect.arrayContaining([
        expect.stringMatching(/button\.tsx$/),
        expect.stringMatching(/tabs\.tsx$/),
        expect.stringMatching(/Follows\.tsx$/),
      ]),
    )
    // A bare ring-ring (full strength, as the shadcn Dialog's close X had)
    // is caught too: its alpha group is then empty.
    const other = sources.flatMap((s) =>
      [...s.text.matchAll(/ring-ring(?:\/(\d+))?/g)]
        .filter((m) => m[1] !== "60")
        .map((m) => `${s.file}: ${m[0]}`),
    )
    expect(other).toEqual([])
  })

  it("shows on keyboard focus only: no ring on plain :focus, which a mouse click also sets", () => {
    const onFocus = sources.flatMap((s) =>
      [...s.text.matchAll(/(?<![\w-])focus:ring[\w/[\]-]*/g)].map((m) => `${s.file}: ${m[0]}`),
    )
    expect(onFocus).toEqual([])
  })

  it("is drawn by every sidebar link, at 3px, instead of the browser's outline", () => {
    render(createElement(MemoryRouter, null, createElement(Sidebar)))
    const links = screen.getAllByRole("link")
    // Positive control: the render reached the nav.
    expect(links.length).toBeGreaterThanOrEqual(5)
    for (const link of links) {
      expect(link.className.split(/\s+/), link.textContent ?? "").toEqual(
        expect.arrayContaining([
          "outline-none",
          "focus-visible:ring-[3px]",
          "focus-visible:ring-ring/60",
        ]),
      )
    }
  })
})

// Owner, 2026-09-25: a focused control's border turns amber, as Inputs and
// Selects do. The app is dark-only, and a dark-scoped border colour class
// (the outline button's dark:border-input, the active tab's
// dark:data-[state=active]:border-input) beats the base
// focus-visible:border-ring: at least as specific, and never earlier in the
// built CSS than a class it ties with. jsdom builds no CSS, so the browser
// pass checks the colour; this keeps the winning class from silently going.
describe("the focus border in the dark theme", () => {
  // A class's variants and its utility: split at the colons outside brackets
  // (`[&_svg:not(…)]:size-4` is one variant).
  const parts = (cls: string): { variants: string[]; utility: string } => {
    const out: string[] = []
    let depth = 0
    let from = 0
    for (let i = 0; i < cls.length; i++) {
      if (cls[i] === "[") depth++
      else if (cls[i] === "]") depth--
      else if (cls[i] === ":" && depth === 0) {
        out.push(cls.slice(from, i))
        from = i + 1
      }
    }
    return { variants: out, utility: cls.slice(from) }
  }
  // border, border-2, border-x, border-t-0, border-dashed…: not a colour.
  const NOT_A_COLOUR = /^border(-[xytrblse])?(-\d+)?$|^border-(solid|dashed|dotted|double|hidden|none)$/

  // The dark-scoped border colour classes that no focus class beats. A focus
  // border-ring class beats one when it carries every variant the other has
  // plus focus-visible (strictly more specific, so its place in the CSS no
  // longer matters), or when it is important (`!`).
  function unbeaten(classes: string[]): string[] {
    const focus = classes
      .map(parts)
      .filter((p) => /^border-ring!?$/.test(p.utility) && p.variants.includes("focus-visible"))
    return classes.filter((cls) => {
      const { variants, utility } = parts(cls)
      if (!variants.includes("dark") || !utility.startsWith("border-")) return false
      if (NOT_A_COLOUR.test(utility) || /^border-ring!?$/.test(utility)) return false
      return !focus.some(
        (f) =>
          (f.utility.endsWith("!") && !utility.endsWith("!")) ||
          (f.variants.length > variants.length && variants.every((v) => f.variants.includes(v))),
      )
    })
  }

  // Every primitive with a dark-scoped border colour class, as rendered.
  const buttonVariantNames = ["default", "destructive", "outline", "secondary", "ghost", "link"] as const
  const rendered: Record<string, () => Record<string, string[]>> = {
    "button.tsx": () =>
      Object.fromEntries(
        buttonVariantNames.map((variant) => [variant, buttonVariants({ variant }).split(/\s+/)]),
      ),
    "tabs.tsx": () => {
      render(
        createElement(
          Tabs,
          { defaultValue: "a" },
          createElement(TabsList, null, createElement(TabsTrigger, { value: "a" }, "A")),
        ),
      )
      return { TabsTrigger: screen.getByRole("tab", { name: "A" }).className.split(/\s+/) }
    },
  }

  it("every primitive with a dark-scoped border colour class is checked below", () => {
    const uiDir = resolve(__dirname, "components/ui")
    const withDarkBorder = readdirSync(uiDir).filter((f) => {
      // Comments stripped: only the classes the component ships count.
      const code = readFileSync(resolve(uiDir, f), "utf8").replace(/\/\*[\s\S]*?\*\/|\/\/.*$/gm, "")
      return code
        .split(/[\s"'`]+/)
        .some((cls) => {
          const { variants, utility } = parts(cls)
          return variants.includes("dark") && utility.startsWith("border-") && !NOT_A_COLOUR.test(utility)
        })
    })
    // Positive control: the scan finds the two known ones.
    expect(withDarkBorder).toEqual(expect.arrayContaining(["button.tsx", "tabs.tsx"]))
    expect(withDarkBorder.sort()).toEqual(Object.keys(rendered).sort())
  })

  it.each(Object.keys(rendered))("%s: turns amber on keyboard focus whatever dark border it has", (file) => {
    const elements = rendered[file]()
    for (const [name, classes] of Object.entries(elements)) {
      expect(unbeaten(classes), name).toEqual([])
    }
  })

  // Positive controls: without its focus class each known collision is caught,
  // so the check above is not passing vacuously.
  it("catches the outline button's and the active tab's collision without their focus class", () => {
    const outline = rendered["button.tsx"]().outline
    expect(unbeaten(outline.filter((c) => c !== "dark:focus-visible:border-ring"))).toEqual([
      "dark:border-input",
    ])
    const tab = rendered["tabs.tsx"]().TabsTrigger
    expect(unbeaten(tab.filter((c) => c !== "dark:focus-visible:border-ring!"))).toEqual([
      "dark:group-data-[variant=line]/tabs-list:data-[state=active]:border-transparent",
      "dark:data-[state=active]:border-input",
    ])
    // The plain class that fixes the button does not fix the tab: it is only
    // as specific as dark:data-[state=active]:border-input, and Tailwind emits
    // it earlier.
    expect(
      unbeaten(tab.map((c) => (c === "dark:focus-visible:border-ring!" ? "dark:focus-visible:border-ring" : c))),
    ).toContain("dark:data-[state=active]:border-input")
  })
})

// A small reader of index.css, for the checks below on where its rules sit.
type CssRule = { selector: string; body: string; within: string[] }

// Every block of a stylesheet (rules and @-blocks alike), each with its OWN
// declarations (not its nested blocks') and the blocks around it. Enough of a
// parser for index.css: comments are stripped, and no string holds a brace.
// Nesting counts: in `.y { border-color: red; &:hover { … } }` the
// declaration before `&:hover` is .y's.
function cssBlocks(source: string): CssRule[] {
  const blocks: CssRule[] = []
  const text = source.replace(/\/\*[\s\S]*?\*\//g, "")
  const open: { selector: string; body: string }[] = []
  let from = 0
  for (let i = 0; i < text.length; i++) {
    if (text[i] === "{") {
      const run = text.slice(from, i)
      const cut = run.lastIndexOf(";") + 1
      if (open.length > 0) open.at(-1)!.body += run.slice(0, cut)
      open.push({ selector: run.slice(cut).trim(), body: "" })
      from = i + 1
    } else if (text[i] === "}") {
      const block = open.pop()
      if (block) {
        block.body += text.slice(from, i)
        blocks.push({ ...block, within: open.map((o) => o.selector) })
      }
      from = i + 1
    }
  }
  return blocks
}

// The default border colour's own rule: `*` in the base layer, where a
// border-* colour class (utilities layer) can replace it.
const baseBorderBodies = (blocks: CssRule[]) =>
  blocks.filter((b) => b.selector === "*" && b.within.includes("@layer base")).map((b) => b.body.trim())

// The blocks outside every layer that set a border property, by declaration
// (property names are case-insensitive) or by @apply of a border-* utility.
// A rule outside a layer outranks every layered utility.
const unlayeredBorderSetters = (blocks: CssRule[]) =>
  blocks
    .filter((b) => ![...b.within, b.selector].some((s) => s.startsWith("@layer")))
    .filter(
      (b) =>
        /(^|[;{\s])border(-[a-z]+)*\s*:/i.test(b.body) ||
        /@apply\b[^;]*(?<=[\s:])!?border(?=[-\s;!]|$)/i.test(b.body),
    )
    .map((b) => b.selector)

// The browser pass on 2026-09-25 found every border-* colour class dead, the
// one above included: index.css set the default border colour outside any
// @layer, and a rule outside a layer outranks all of Tailwind's layered
// utilities whatever their specificity. jsdom has no cascade, so this reads
// where each rule of index.css sits.
describe("the default border colour", () => {
  const blocks = cssBlocks(css)

  it("is set in the base layer, where a border-* colour class can replace it", () => {
    expect(baseBorderBodies(blocks)).toEqual(["border-color: var(--color-border);"])
  })

  it("is not set, nor any other border property, by a rule outside a layer", () => {
    // Positive control: the scan sees the rules outside a layer (:root's tokens).
    expect(blocks.some((b) => b.selector === ":root" && b.within.length === 0)).toBe(true)
    expect(unlayeredBorderSetters(blocks)).toEqual([])
  })

  // The scanner itself, on stylesheets that must fail.
  it.each([
    ["shadcn's template form, @apply outside a layer", "* { @apply border-border; }", ["*"]],
    ["@apply with a variant", ".x { @apply outline-none hover:border-input; }", [".x"]],
    [
      "a declaration before a nested rule",
      ".y { border-color: red; &:hover { color: blue } }",
      [".y"],
    ],
    ["an uppercase property", ".z { BORDER-COLOR: red; }", [".z"]],
    ["a list selector", "*, ::before { border-color: red; }", ["*, ::before"]],
    ["a border set inside @media", "@media (hover: hover) { .m { border: 1px solid; } }", [".m"]],
  ])("flags %s", (_, sheet, flagged) => {
    expect(unlayeredBorderSetters(cssBlocks(sheet))).toEqual(flagged)
  })

  it("passes a border set inside a layer, and a nested rule's own declarations stay its own", () => {
    const sheet = `
      :root { --border: red; }
      @layer base { * { border-color: var(--color-border); } }
      @layer components { .c { border-width: 2px; } }
      .n { color: red; &:hover { color: blue; } }
      .o { @apply outline-border bg-background; }`
    expect(unlayeredBorderSetters(cssBlocks(sheet))).toEqual([])
    expect(baseBorderBodies(cssBlocks(sheet))).toEqual(["border-color: var(--color-border);"])
  })

  it("does not take @apply, or a list selector, as the base rule", () => {
    expect(baseBorderBodies(cssBlocks("@layer base { * { @apply border-border; } }"))).toEqual([
      "@apply border-border;",
    ])
    expect(baseBorderBodies(cssBlocks("@layer base { *, ::before { border-color: red; } }"))).toEqual([])
  })
})

// Owner's ruling (g), 2026-09-24: every control with a solid fill while
// focused sets its ring 2px off the fill, over the page background, so the
// amber ring never touches an amber (or red) fill and reads as a fatter
// shape. Controls without a fill keep the ring flush, as before.
describe("the ring offset on filled controls", () => {
  const OFFSET = ["focus-visible:ring-offset-2", "focus-visible:ring-offset-background"]
  const classesOf = (variant: Parameters<typeof buttonVariants>[0]) =>
    buttonVariants(variant).split(/\s+/)

  it.each(["default", "destructive", "secondary"] as const)(
    "the filled %s button sets its ring 2px off the fill",
    (variant) => {
      expect(classesOf({ variant })).toEqual(expect.arrayContaining(OFFSET))
    },
  )

  it.each(["outline", "ghost", "link"] as const)(
    "the unfilled %s button keeps its ring flush",
    (variant) => {
      expect(classesOf({ variant }).filter((c) => /ring-offset/.test(c))).toEqual([])
    },
  )

  it("the active sidebar link, filled amber, sets its ring off the fill; the idle links keep theirs flush", () => {
    render(
      createElement(MemoryRouter, { initialEntries: ["/lessons"] }, createElement(Sidebar)),
    )
    const active = screen.getByRole("link", { name: "Lessons" })
    // Positive control: this is the filled one.
    expect(active.className.split(/\s+/)).toContain("bg-primary")
    expect(active.className.split(/\s+/)).toEqual(expect.arrayContaining(OFFSET))
    const idle = screen.getAllByRole("link").filter((l) => l !== active)
    expect(idle.length).toBeGreaterThanOrEqual(4)
    for (const link of idle) {
      expect(link.className, link.textContent ?? "").not.toMatch(/ring-offset/)
    }
  })

  it("a checked checkbox, filled amber, sets its ring off the fill; unchecked it keeps it flush", () => {
    render(createElement(Checkbox, { defaultChecked: true, "aria-label": "files" }))
    const classes = screen.getByRole("checkbox", { name: "files" }).className.split(/\s+/)
    expect(classes).toEqual(
      expect.arrayContaining([
        "data-[state=checked]:bg-primary",
        "data-[state=checked]:focus-visible:ring-offset-2",
        "data-[state=checked]:focus-visible:ring-offset-background",
      ]),
    )
    // Only while checked: no offset class without the checked condition.
    expect(classes.filter((c) => /ring-offset/.test(c) && !c.startsWith("data-[state=checked]:"))).toEqual([])
  })
})

// Owner, 2026-09-24: 12px between a dialog footer's buttons, stacked on a
// phone and in a row from sm up; and a red button's offset ring must clear
// the Cancel beside it.
describe("dialog footers", () => {
  const px = (classes: string[], pattern: RegExp, scale: number): number => {
    const hit = classes.map((c) => c.match(pattern)).find((m) => m !== null)
    if (!hit) throw new Error(`no class matches ${pattern}`)
    return Number(hit[1]) * scale
  }
  const RING_PX = px(destructiveClasses, /^focus-visible:ring-\[(\d+)px\]$/, 1)
  const OFFSET_PX = px(destructiveClasses, /^focus-visible:ring-offset-(\d+)$/, 1)

  it.each([
    ["DialogFooter", DialogFooter],
    ["AlertDialogFooter", AlertDialogFooter],
  ])("%s puts 12px between its buttons, stacked, then in a row from sm", (_, Footer) => {
    const { container } = render(createElement(Footer))
    const classes = (container.firstElementChild as HTMLElement).className.split(/\s+/)
    expect(classes.filter((c) => /^gap-/.test(c))).toEqual(["gap-3"])
    const gap = px(classes, /^gap-(\d+)$/, 4) // Tailwind's spacing step is 4px
    expect(gap).toBe(12)
    // The red button's ring and its offset stay clear of the next button.
    expect(gap).toBeGreaterThan(RING_PX + OFFSET_PX)
    expect(classes).toEqual(expect.arrayContaining(["flex-col-reverse", "sm:flex-row"]))
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
