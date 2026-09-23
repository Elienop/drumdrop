import { screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { http, HttpResponse } from "msw"
import { ORIGIN, renderWithProviders, server } from "@/test/msw"
import { Toaster } from "@/components/ui/sonner"
import { Settings } from "./Settings"

it("shows the Disconnected pill, then flips to Connected after a successful login", async () => {
  server.use(
    http.get(`${ORIGIN}/api/session`, () => HttpResponse.json({ connected: false })),
    http.get(`${ORIGIN}/healthz`, () =>
      HttpResponse.json({ status: "ok", version: "v1.2.3" }),
    ),
  )
  const user = userEvent.setup()
  renderWithProviders(
    <>
      <Settings />
      <Toaster />
    </>,
  )

  expect(await screen.findByText(/disconnected/i)).toBeInTheDocument()

  await user.type(screen.getByLabelText(/email/i), "me@example.com")
  await user.type(screen.getByLabelText(/password/i), "hunter2")

  // After Connect, the server reports connected so the invalidated query flips.
  server.use(
    http.post(`${ORIGIN}/api/session`, () => HttpResponse.json({ connected: true })),
    http.get(`${ORIGIN}/api/session`, () => HttpResponse.json({ connected: true })),
  )
  await user.click(screen.getByRole("button", { name: /connect/i }))

  // The pill flips once the invalidated session query refetches connected:true.
  // Both the success toast and the badge read "Connected", so wait for the
  // Disconnected pill to disappear and assert at least one "Connected" remains.
  await waitFor(() => expect(screen.queryByText(/disconnected/i)).not.toBeInTheDocument())
  expect((await screen.findAllByText(/^connected$/i)).length).toBeGreaterThan(0)
})

it("a 401 on connect shows a toast naming the outcome, with the server's reason", async () => {
  server.use(
    http.get(`${ORIGIN}/api/session`, () => HttpResponse.json({ connected: false })),
    http.post(`${ORIGIN}/api/session`, () =>
      HttpResponse.json({ error: "bad credentials" }, { status: 401 }),
    ),
    http.get(`${ORIGIN}/healthz`, () =>
      HttpResponse.json({ status: "ok", version: "v1.2.3" }),
    ),
  )
  const user = userEvent.setup()
  renderWithProviders(
    <>
      <Settings />
      <Toaster />
    </>,
  )

  await screen.findByText(/disconnected/i)
  await user.type(screen.getByLabelText(/email/i), "me@example.com")
  await user.type(screen.getByLabelText(/password/i), "wrong")
  await user.click(screen.getByRole("button", { name: /connect/i }))

  expect(await screen.findByText("Couldn't connect to Musora")).toBeInTheDocument()
  expect(screen.getByText("bad credentials", { selector: "[data-description]" })).toBeInTheDocument()
})

it("renders the version string from the health endpoint", async () => {
  server.use(
    http.get(`${ORIGIN}/api/session`, () => HttpResponse.json({ connected: false })),
    http.get(`${ORIGIN}/healthz`, () =>
      HttpResponse.json({ status: "ok", version: "v1.2.3" }),
    ),
  )
  renderWithProviders(<Settings />)

  expect(await screen.findByText(/v1\.2\.3/)).toBeInTheDocument()
})
