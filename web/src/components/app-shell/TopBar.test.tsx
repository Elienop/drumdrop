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
