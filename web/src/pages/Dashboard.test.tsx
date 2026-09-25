import { screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { http, HttpResponse } from "msw"
import { ORIGIN, renderWithProviders, server } from "@/test/msw"
import { Toaster } from "@/components/ui/sonner"
import { Dashboard } from "./Dashboard"

it("renders summary cards with the downloaded and follows counts", async () => {
  server.use(
    http.get(`${ORIGIN}/api/summary`, () =>
      HttpResponse.json({
        follows: 12,
        lessons: { downloaded: 128, pending: 4, downloading: 1, failed: 0, skipped: 2 },
        jobs: { queued: 8, running: 1, done: 120, failed: 0, canceled: 0 },
      }),
    ),
  )
  renderWithProviders(<Dashboard />)

  expect(await screen.findByText("128")).toBeInTheDocument()
  expect(screen.getByText("12")).toBeInTheDocument()
})

it("a sync that fails without a server message toasts the outcome and a sentence, never 'HTTP 500'", async () => {
  server.use(http.post(`${ORIGIN}/api/sync`, () => new HttpResponse(null, { status: 500 })))
  renderWithProviders(
    <>
      <Dashboard />
      <Toaster />
    </>,
  )

  await userEvent.click(await screen.findByRole("button", { name: /run sync/i }))
  expect(await screen.findByText("Couldn't start a sync")).toBeInTheDocument()
  expect(
    screen.getByText("Couldn't reach the server, or it answered unexpectedly. Try again."),
  ).toBeInTheDocument()
  expect(screen.queryByText(/HTTP 500/)).not.toBeInTheDocument()
  // A toast with a sentence to read stays until closed (the app's one rule).
  expect(screen.getByRole("button", { name: "Close toast" })).toBeInTheDocument()
})

// The server's own sentences for its 503s on /api/sync (msgNoDaemon and
// msgNoPlanner, internal/server/messages.go).
const NO_DAEMON =
  "This server runs without the download daemon, so there's nothing to pause, resume or sync."
const NO_PLANNER =
  "This server runs without the sync planner, so it can't say what a sync would queue."

// DrumDrop's 503 means nothing is attached to run a sync, so the button that
// got it is blocked: aria-disabled, with the server's reason in a tooltip.
// jsdom applies no CSS, so how it LOOKS is read from its classes here; the
// browser pass checks it.
describe("a sync button blocked by a 503", () => {
  // Every POST to /api/sync answers DrumDrop's 503 for it and is counted, so
  // a test can show that pressing a blocked button sends nothing.
  function blockSync() {
    const posts = { count: 0 }
    server.use(
      http.post(`${ORIGIN}/api/sync`, async ({ request }) => {
        posts.count++
        const { dry_run } = (await request.json()) as { dry_run: boolean }
        return HttpResponse.json({ error: dry_run ? NO_PLANNER : NO_DAEMON }, { status: 503 })
      }),
    )
    return posts
  }

  // Presses the named button once, so it gets its 503, and returns it blocked.
  // Re-queried: the blocked button is a new node, the first one is detached.
  async function block(name: string) {
    renderWithProviders(<Dashboard />)
    const idle = await screen.findByRole("button", { name })
    expect(idle).not.toHaveAttribute("aria-disabled")
    await userEvent.click(idle)
    return waitFor(() => {
      const blocked = screen.getByRole("button", { name })
      expect(blocked).toHaveAttribute("aria-disabled", "true")
      return blocked
    })
  }

  // Run sync comes first in the tab order, Dry-run second.
  it.each([
    ["Run sync", 1, NO_DAEMON],
    ["Dry-run", 2, NO_PLANNER],
  ])(
    "%s stays in the tab order, is announced as unavailable, and gives the server's reason on focus",
    async (name, tabs, reason) => {
      blockSync()
      const button = await block(name)

      // The pressed node was replaced, so focus starts from the page.
      for (let i = 0; i < tabs; i++) await userEvent.tab()
      expect(button).toHaveFocus()
      expect(button).toHaveAttribute("aria-disabled", "true")
      expect(await screen.findByRole("tooltip")).toHaveTextContent(reason)
      expect(button).toHaveAccessibleDescription(reason)
      // The icon is decoration: the name is the label alone.
      expect(button.querySelector(":scope > svg")).toHaveAttribute("aria-hidden", "true")
      // A sentence wraps at a readable width instead of one wide line.
      expect(document.querySelector('[data-slot="tooltip-content"]')).toHaveClass(
        "max-w-md",
        "break-words",
      )
    },
  )

  it("gives the reason on hover", async () => {
    blockSync()
    const button = await block("Run sync")

    await userEvent.hover(button)
    expect(await screen.findByRole("tooltip")).toHaveTextContent(NO_DAEMON)
  })

  it("does nothing when pressed, and Enter or Space leaves the reason showing", async () => {
    const posts = blockSync()
    const button = await block("Run sync")
    expect(posts.count).toBe(1)

    await userEvent.tab()
    expect(button).toHaveFocus()
    await screen.findByRole("tooltip")
    await userEvent.keyboard("{Enter}")
    expect(screen.getByRole("tooltip")).toHaveTextContent(NO_DAEMON)
    await userEvent.keyboard(" ")
    expect(screen.getByRole("tooltip")).toHaveTextContent(NO_DAEMON)
    await userEvent.click(button)

    expect(posts.count).toBe(1)
    expect(button).toHaveAttribute("aria-disabled", "true")
  })

  // Radix's trigger closes its tooltip on pointer-down, and hovering would
  // not reopen it until the pointer left the button.
  it("a mouse press leaves the reason showing", async () => {
    blockSync()
    const button = await block("Run sync")

    await userEvent.hover(button)
    await screen.findByRole("tooltip")
    await userEvent.click(button)
    expect(screen.getByRole("tooltip")).toHaveTextContent(NO_DAEMON)
  })

  // The disabled look: dimmed, the same size (h-8, size sm), and no hover
  // change (each variant's own hover classes give way to its resting colours).
  it.each([
    ["Run sync", ["hover:bg-primary"], ["hover:bg-primary/90"]],
    [
      "Dry-run",
      ["hover:bg-background", "hover:text-inherit", "dark:hover:bg-input/30"],
      ["hover:bg-accent", "hover:text-accent-foreground", "dark:hover:bg-input/50"],
    ],
  ])("%s keeps the disabled look", async (name, kept, dropped) => {
    blockSync()
    const classes = (await block(name)).className.split(/\s+/)

    expect(classes).toEqual(expect.arrayContaining(["opacity-50", "h-8", ...kept]))
    for (const c of dropped) expect(classes).not.toContain(c)
  })

  // opacity-50 would dim the button's own ring to 1.83:1, so the wrapper,
  // at full opacity, draws the app's ring (ring-ring/60, 3px, offset off a
  // filled button only) while the button inside has keyboard focus.
  it.each([
    ["Run sync", true],
    ["Dry-run", false],
  ])("%s has its focus ring drawn undimmed around it", async (name, filled) => {
    blockSync()
    const button = await block(name)
    const own = button.className.split(/\s+/)
    const wrapper = button.parentElement!.className.split(/\s+/)

    expect(own).toEqual(
      expect.arrayContaining(["focus-visible:ring-0", "focus-visible:ring-offset-0"]),
    )
    expect(own).not.toContain("focus-visible:ring-[3px]")
    expect(own).not.toContain("focus-visible:ring-offset-2")
    expect(wrapper).toEqual(
      expect.arrayContaining([
        "inline-flex",
        "rounded-md",
        "has-focus-visible:ring-[3px]",
        "has-focus-visible:ring-ring/60",
        // The ring (a box-shadow) fades in like every button's does.
        "transition-shadow",
      ]),
    )
    const offset = ["has-focus-visible:ring-offset-2", "has-focus-visible:ring-offset-background"]
    if (filled) expect(wrapper).toEqual(expect.arrayContaining(offset))
    else expect(wrapper.filter((c) => /ring-offset/.test(c))).toEqual([])
    // The wrapper is only a frame: not focusable, no role.
    expect(button.parentElement).not.toHaveAttribute("tabindex")
    expect(button.parentElement).not.toHaveAttribute("role")
  })
})

// Only DrumDrop's own 503 blocks a button. A reverse proxy answers 503 with
// no body, or with its own page, while the container restarts: that says
// nothing about what is attached, and the next press may well work.
const proxy503s = {
  "with no body": () => new HttpResponse(null, { status: 503 }),
  "with a proxy's HTML page": () =>
    new HttpResponse("<html><body>503 Service Unavailable</body></html>", {
      status: 503,
      headers: { "Content-Type": "text/html" },
    }),
}
it.each([
  ["Run sync", "with no body", "Couldn't start a sync"],
  ["Run sync", "with a proxy's HTML page", "Couldn't start a sync"],
  ["Dry-run", "with no body", "Couldn't run the dry run"],
  ["Dry-run", "with a proxy's HTML page", "Couldn't run the dry run"],
] as const)("%s: a 503 %s leaves the button live", async (name, kind, outcome) => {
  server.use(http.post(`${ORIGIN}/api/sync`, proxy503s[kind]))
  renderWithProviders(
    <>
      <Dashboard />
      <Toaster />
    </>,
  )

  await userEvent.click(await screen.findByRole("button", { name }))
  // The failure is still said, as any other failure is.
  expect(await screen.findByText(outcome)).toBeInTheDocument()
  // Re-queried: a blocked button would be a new node.
  await waitFor(() =>
    expect(screen.getByRole("button", { name })).not.toHaveAttribute("aria-disabled"),
  )
})

it.each([
  [1, "A sync would queue 1 lesson"],
  [3, "A sync would queue 3 lessons"],
])("a dry run that would queue %i says so in the right number", async (n, sentence) => {
  server.use(http.post(`${ORIGIN}/api/sync`, () => HttpResponse.json({ would_enqueue: n })))
  renderWithProviders(
    <>
      <Dashboard />
      <Toaster />
    </>,
  )

  await userEvent.click(await screen.findByRole("button", { name: /dry-run/i }))
  expect(await screen.findByText(sentence)).toBeInTheDocument()
})

// The press keeps keyboard focus while its request runs: a disabled button
// would drop it to <body> in a browser. jsdom does NOT drop focus from a
// button that becomes disabled, so toHaveFocus alone would pass with
// `disabled`; toBeEnabled is the assertion that catches it.
it.each([
  ["Run sync", { triggered: true }, 202],
  ["Dry-run", { would_enqueue: 2 }, 200],
])("%s keeps keyboard focus while its request runs, its spinner in its icon's place", async (name, body, status) => {
  let answer!: () => void
  server.use(
    http.post(
      `${ORIGIN}/api/sync`,
      () =>
        new Promise<Response>((resolve) => {
          answer = () => resolve(HttpResponse.json(body, { status }))
        }),
    ),
  )
  const user = userEvent.setup()
  renderWithProviders(
    <>
      <Dashboard />
      <Toaster />
    </>,
  )

  const btn = await screen.findByRole("button", { name })
  btn.focus()
  await user.keyboard("{Enter}")

  await waitFor(() => expect(btn).toHaveAttribute("aria-disabled", "true"))
  expect(btn).toHaveFocus()
  expect(btn).toBeEnabled()
  expect(btn).toHaveAccessibleName(name)
  expect(btn.querySelectorAll(":scope > svg")).toHaveLength(1)
  expect(btn.querySelector(":scope > svg")).toHaveClass("animate-spin")
  answer()
  await waitFor(() => expect(btn).not.toHaveAttribute("aria-disabled"))
  expect(btn).toHaveFocus()
})
