import { screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { http, HttpResponse } from "msw"
import { ORIGIN, renderWithProviders, server } from "@/test/msw"
import { afterEach, describe, expect, it } from "vitest"
import { Toaster } from "@/components/ui/sonner"
import { TokenGate } from "@/components/TokenGate"
import { clearToken, getToken, setToken } from "@/lib/auth"
import { Settings } from "./Settings"

it("the Musora connection card says where the email and password go, and that they go nowhere else", async () => {
  server.use(
    http.get(`${ORIGIN}/api/session`, () => HttpResponse.json({ connected: false })),
    http.get(`${ORIGIN}/healthz`, () => HttpResponse.json({ status: "ok", version: "v1.2.3" })),
  )
  renderWithProviders(<Settings />)
  expect(
    await screen.findByText(
      "DrumDrop's server signs in to Musora with your email and password, and keeps a copy in its config folder. They aren't sent anywhere else.",
    ),
  ).toBeInTheDocument()
})

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

// --- A rejected Musora login is not DrumDrop's own 401 ------------------------
//
// The api client treats every 401 as DrumDrop's API token failing: it clears
// the stored token and opens the token gate. So the server answers a Musora
// rejection with 422 and this exact sentence; only DrumDrop's auth answers
// 401. The page is mounted as App.tsx mounts it: with the real
// TokenGate and Toaster beside it, and a stored token.
const REJECTED = "Musora didn't accept that email and password. Check them, then Connect again."

function renderSettingsAsTheAppDoes() {
  setToken("stored-api-token")
  const user = userEvent.setup()
  renderWithProviders(
    <>
      <Settings />
      <TokenGate onSaved={() => {}} />
      <Toaster />
    </>,
  )
  return user
}

async function connectWith(user: ReturnType<typeof userEvent.setup>, password: string) {
  await screen.findByText(/disconnected/i)
  await user.type(screen.getByLabelText(/email/i), "me@example.com")
  await user.type(screen.getByLabelText(/password/i), password)
  await user.click(screen.getByRole("button", { name: /connect/i }))
}

describe("connecting to Musora with a wrong password", () => {
  afterEach(() => clearToken({ silent: true }))

  it("toasts the server's sentence and keeps DrumDrop's token and session: no token gate", async () => {
    server.use(
      http.get(`${ORIGIN}/api/session`, () => HttpResponse.json({ connected: false })),
      http.post(`${ORIGIN}/api/session`, () =>
        HttpResponse.json({ error: REJECTED }, { status: 422 }),
      ),
      http.get(`${ORIGIN}/healthz`, () => HttpResponse.json({ status: "ok", version: "v1.2.3" })),
    )
    const user = renderSettingsAsTheAppDoes()
    await connectWith(user, "wrong")

    expect(await screen.findByText("Couldn't connect to Musora")).toBeInTheDocument()
    expect(screen.getByText(REJECTED, { selector: "[data-description]" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Close toast" })).toBeInTheDocument()
    expect(getToken()).toBe("stored-api-token")
    expect(screen.queryByRole("dialog", { name: "API token required" })).not.toBeInTheDocument()
  })

  it("control: a 401 from DrumDrop's own auth still clears the token and opens the gate", async () => {
    server.use(
      http.get(`${ORIGIN}/api/session`, () => HttpResponse.json({ connected: false })),
      http.post(`${ORIGIN}/api/session`, () =>
        HttpResponse.json({ error: "unauthorized" }, { status: 401 }),
      ),
      http.get(`${ORIGIN}/healthz`, () => HttpResponse.json({ status: "ok", version: "v1.2.3" })),
    )
    const user = renderSettingsAsTheAppDoes()
    await connectWith(user, "anything")

    expect(await screen.findByRole("dialog", { name: "API token required" })).toBeInTheDocument()
    expect(getToken()).toBeNull()
  })
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

// Connect keeps keyboard focus while the sign-in runs (a disabled button
// drops it to <body> in a browser; jsdom does not, so toBeEnabled is the
// assertion that catches `disabled`), and Enter meanwhile, on the button or
// in a field, does not start a second sign-in.
it("Connect keeps keyboard focus while it runs, and Enter meanwhile sends nothing more", async () => {
  let logins = 0
  let answer!: () => void
  server.use(
    http.get(`${ORIGIN}/api/session`, () => HttpResponse.json({ connected: false })),
    http.get(`${ORIGIN}/healthz`, () => HttpResponse.json({ status: "ok", version: "v1.2.3" })),
    http.post(`${ORIGIN}/api/session`, () => {
      logins++
      return new Promise<Response>((resolve) => {
        answer = () => resolve(HttpResponse.json({ connected: true }))
      })
    }),
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
  await user.type(screen.getByLabelText(/password/i), "hunter2")
  const btn = screen.getByRole("button", { name: "Connect" })
  btn.focus()
  await user.keyboard("{Enter}")

  await waitFor(() => expect(btn).toHaveAttribute("aria-disabled", "true"))
  expect(btn).toHaveFocus()
  expect(btn).toBeEnabled()
  expect(btn).toHaveAccessibleName("Connecting…")

  await user.keyboard("{Enter}")
  await user.click(screen.getByLabelText(/password/i))
  await user.keyboard("{Enter}")
  expect(logins).toBe(1)

  answer()
  await waitFor(() => expect(btn).not.toHaveAttribute("aria-disabled"))
  expect(logins).toBe(1)
})
