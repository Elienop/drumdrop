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
})

it("disables the Dry-run button after a 503 probe", async () => {
  server.use(
    http.get(`${ORIGIN}/api/summary`, () =>
      HttpResponse.json({
        follows: 0,
        lessons: { downloaded: 0, pending: 0, downloading: 0, failed: 0, skipped: 0 },
        jobs: { queued: 0, running: 0, done: 0, failed: 0, canceled: 0 },
      }),
    ),
    http.post(`${ORIGIN}/api/sync`, () =>
      HttpResponse.json({ error: "no planner attached" }, { status: 503 }),
    ),
  )
  renderWithProviders(<Dashboard />)

  const dryRun = await screen.findByRole("button", { name: /dry-run/i })
  expect(dryRun).toBeEnabled()
  await userEvent.click(dryRun)

  // Re-query: a blocked button is re-wrapped in a tooltip trigger, so the
  // original node is detached after the 503 settles.
  await waitFor(() =>
    expect(screen.getByRole("button", { name: /dry-run/i })).toBeDisabled(),
  )
})
