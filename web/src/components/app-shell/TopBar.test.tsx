import { act, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { http, HttpResponse } from "msw"
import { ORIGIN, renderWithProviders, server } from "@/test/msw"
import { qk } from "@/lib/queryKeys"
import { Toaster } from "@/components/ui/sonner"
import { TopBar } from "./TopBar"

const summary = (paused: boolean) => ({
  follows: 0,
  lessons: { downloaded: 0, pending: 0, downloading: 0, failed: 0, skipped: 0 },
  jobs: { queued: 0, running: 0, done: 0, failed: 0, canceled: 0 },
  paused,
})

it("shows Pause and calls POST /api/pause when running", async () => {
  let pausedHit = 0
  server.use(
    http.get(`${ORIGIN}/api/summary`, () => HttpResponse.json(summary(false))),
    http.post(`${ORIGIN}/api/pause`, () => {
      pausedHit++
      return HttpResponse.json({ paused: true })
    }),
  )
  renderWithProviders(<TopBar />)

  const btn = await screen.findByRole("button", { name: /pause/i })
  await userEvent.click(btn)

  await waitFor(() => expect(pausedHit).toBe(1))
})

// msgNoDaemon and msgServerError, verbatim from internal/server/messages.go.
const NO_DAEMON =
  "This server runs without the download daemon, so there's nothing to pause, resume or sync."
const SERVER_ERROR =
  "This may not have finished: something went wrong on the server. Check the server log, fix the problem, then try again."

it.each([
  ["Pause", false, "pause", 503, NO_DAEMON, "Couldn't pause syncing"],
  ["Resume", true, "resume", 500, SERVER_ERROR, "Couldn't resume syncing"],
])("a failed %s says so with the server's sentence, and stays until closed", async (button, paused, path, status, sentence, title) => {
  server.use(
    http.get(`${ORIGIN}/api/summary`, () => HttpResponse.json(summary(paused))),
    http.post(`${ORIGIN}/api/${path}`, () => HttpResponse.json({ error: sentence }, { status })),
  )
  renderWithProviders(
    <>
      <TopBar />
      <Toaster />
    </>,
  )

  await userEvent.click(await screen.findByRole("button", { name: button }))
  const t = (await screen.findByText(title)).closest<HTMLElement>("[data-sonner-toast]")!
  expect(within(t).getByText(sentence, { selector: "[data-description]" })).toBeInTheDocument()
  expect(within(t).getByRole("button", { name: "Close toast" })).toBeInTheDocument()
})

it("shows Resume and the paused indicator when paused", async () => {
  let resumeHit = 0
  server.use(
    http.get(`${ORIGIN}/api/summary`, () => HttpResponse.json(summary(true))),
    http.post(`${ORIGIN}/api/resume`, () => {
      resumeHit++
      return HttpResponse.json({ paused: false })
    }),
  )
  renderWithProviders(<TopBar />)

  const btn = await screen.findByRole("button", { name: /resume/i })
  expect(screen.getByText(/paused/i)).toBeInTheDocument()
  await userEvent.click(btn)

  await waitFor(() => expect(resumeHit).toBe(1))
})

// The press keeps keyboard focus while its request runs: a disabled button
// would drop it to <body>, and the next Tab would start from the page's top.
// jsdom does NOT drop focus from a button that becomes disabled (a browser
// does), so toHaveFocus alone would pass with `disabled`; toBeEnabled is the
// assertion that catches it (checked by adding disabled={toggle.isPending}).
it("Pause keeps keyboard focus on the button while its request runs", async () => {
  let answer!: () => void
  server.use(
    http.get(`${ORIGIN}/api/summary`, () => HttpResponse.json(summary(false))),
    http.post(
      `${ORIGIN}/api/pause`,
      () =>
        new Promise<Response>((resolve) => {
          answer = () => resolve(HttpResponse.json({ paused: true }))
        }),
    ),
  )
  const user = userEvent.setup()
  renderWithProviders(<TopBar />)

  const btn = await screen.findByRole("button", { name: "Pause" })
  btn.focus()
  await user.keyboard("{Enter}")

  await waitFor(() => expect(btn).toHaveAttribute("aria-disabled", "true"))
  expect(btn).toHaveFocus()
  expect(btn).toBeEnabled()
  answer()
  await waitFor(() => expect(btn).not.toHaveAttribute("aria-disabled"))
  expect(btn).toHaveFocus()
})

// Owner's ruling 2026-09-24, (l): Pause stays as compact as Sync beside it.
// jsdom cannot measure a width, so this pins what the width is made of: the
// spinner TAKES the icon's place (one icon, a direct child of the button, so
// Button's own icon padding and its small size's gap apply), the label stays
// the pressed verb, and no hidden longer label is laid out to reserve room.
it("while Pause runs, the spinner replaces the icon and the label stays 'Pause', with nothing extra laid out", async () => {
  let paused = false
  let answer!: () => void
  server.use(
    http.get(`${ORIGIN}/api/summary`, () => HttpResponse.json(summary(paused))),
    http.post(
      `${ORIGIN}/api/pause`,
      () =>
        new Promise<Response>((resolve) => {
          answer = () => {
            paused = true
            resolve(HttpResponse.json({ paused: true }))
          }
        }),
    ),
  )
  const user = userEvent.setup()
  renderWithProviders(<TopBar />)

  const btn = await screen.findByRole("button", { name: "Pause" })
  const layout = () => ({
    icons: btn.querySelectorAll("svg").length,
    ownIcons: btn.querySelectorAll(":scope > svg").length,
    spinning: btn.querySelectorAll(":scope > svg.animate-spin").length,
    stacked: btn.querySelectorAll("[data-label]").length,
    text: btn.textContent,
  })
  expect(layout()).toEqual({ icons: 1, ownIcons: 1, spinning: 0, stacked: 0, text: "Pause" })

  await user.click(btn)
  await waitFor(() => expect(btn).toHaveAttribute("aria-disabled", "true"))
  expect(layout()).toEqual({ icons: 1, ownIcons: 1, spinning: 1, stacked: 0, text: "Pause" })
  expect(btn).toHaveAccessibleName("Pause")

  answer()
  await waitFor(() => expect(btn).toHaveAccessibleName("Resume"))
  expect(layout()).toEqual({ icons: 1, ownIcons: 1, spinning: 0, stacked: 0, text: "Resume" })
})

// onSettled RETURNS the refetch, so the press stays pending until the new
// flag is in. If it did not, the button would be live again for one round
// trip, still offering "Pause" on a daemon that is already paused.
it("after the pause is answered the button stays pending until the flag is re-read, then offers Resume", async () => {
  let paused = false
  let holdSummary = false
  let releaseSummary!: () => void
  server.use(
    http.get(`${ORIGIN}/api/summary`, () =>
      holdSummary
        ? new Promise<Response>((resolve) => {
            releaseSummary = () => resolve(HttpResponse.json(summary(paused)))
          })
        : HttpResponse.json(summary(paused)),
    ),
    http.post(`${ORIGIN}/api/pause`, () => {
      paused = true
      holdSummary = true
      return HttpResponse.json({ paused: true })
    }),
  )
  const user = userEvent.setup()
  renderWithProviders(<TopBar />)

  const btn = await screen.findByRole("button", { name: "Pause" })
  await user.click(btn)
  // The pause is answered and the re-read is on its way, held here.
  await waitFor(() => expect(releaseSummary).toBeTypeOf("function"))
  expect(btn).toHaveAttribute("aria-disabled", "true")
  expect(btn).toHaveAccessibleName("Pause")

  releaseSummary()
  await waitFor(() => expect(btn).toHaveAccessibleName("Resume"))
  expect(btn).not.toHaveAttribute("aria-disabled")
})

// After a failure the flag is re-read too, not only after a success: a
// failed press may still have changed it on the server, and the button must
// not keep offering a state the server no longer has.
it.each([
  ["Pause", false, "pause"],
  ["Resume", true, "resume"],
])("a failed %s re-reads the pause flag", async (button, paused, path) => {
  let summaryReads = 0
  server.use(
    http.get(`${ORIGIN}/api/summary`, () => {
      summaryReads++
      return HttpResponse.json(summary(paused))
    }),
    http.post(`${ORIGIN}/api/${path}`, () =>
      HttpResponse.json({ error: SERVER_ERROR }, { status: 500 }),
    ),
  )
  renderWithProviders(
    <>
      <TopBar />
      <Toaster />
    </>,
  )

  const btn = await screen.findByRole("button", { name: button })
  const readsBefore = summaryReads
  await userEvent.click(btn)
  await screen.findByText(SERVER_ERROR)
  await waitFor(() => expect(summaryReads).toBeGreaterThan(readsBefore))
})

// Code review round 5c, I5: while a press runs, the button keeps the verb that
// was pressed even when the flag is re-read meanwhile (another tab or client
// changed it, or something else refreshed the summary) and says the opposite.
// Only the answer to the press, and the re-read after it, decide what the
// button offers next.
it.each([
  ["Pause", false, "pause", "Resume"],
  ["Resume", true, "resume", "Pause"],
] as const)(
  "while %s runs, a re-read of a flag that already changed leaves its label alone",
  async (verb, startPaused, path, next) => {
    let paused: boolean = startPaused
    let answer!: () => void
    server.use(
      http.get(`${ORIGIN}/api/summary`, () => HttpResponse.json(summary(paused))),
      http.post(
        `${ORIGIN}/api/${path}`,
        () =>
          new Promise<Response>((resolve) => {
            answer = () => resolve(HttpResponse.json({ paused }))
          }),
      ),
    )
    const user = userEvent.setup()
    const { qc } = renderWithProviders(<TopBar />)

    const btn = await screen.findByRole("button", { name: verb })
    await user.click(btn)
    await waitFor(() => expect(answer).toBeTypeOf("function"))

    // The flag flips on the server and is re-read while the press still runs.
    paused = !startPaused
    await act(() => qc.refetchQueries({ queryKey: qk.summary }))
    expect(qc.getQueryData<{ paused: boolean }>(qk.summary)?.paused).toBe(!startPaused)
    expect(btn).toHaveAttribute("aria-disabled", "true")
    expect(btn).toHaveAccessibleName(verb)

    answer()
    await waitFor(() => expect(btn).not.toHaveAttribute("aria-disabled"))
    expect(btn).toHaveAccessibleName(next)
  },
)
