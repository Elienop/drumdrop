import { screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { http, HttpResponse } from "msw"
import { ORIGIN, renderWithProviders, server } from "@/test/msw"
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
