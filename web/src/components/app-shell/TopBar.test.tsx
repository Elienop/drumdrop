import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { http, HttpResponse } from "msw"
import { ORIGIN, renderWithProviders, server } from "@/test/msw"
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
  expect(btn).toHaveAccessibleName("Pausing…")
  answer()
  await waitFor(() => expect(btn).not.toHaveAttribute("aria-disabled"))
  expect(btn).toHaveFocus()
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
