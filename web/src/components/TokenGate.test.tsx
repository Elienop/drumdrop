import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { TokenGate } from "./TokenGate"
import { getToken, clearToken } from "@/lib/auth"

beforeEach(() => clearToken())

it("opens when auth is required (401) and saves the token", async () => {
  render(<TokenGate onSaved={() => {}} />) // no-op onSaved so the test doesn't reload jsdom
  expect(screen.queryByRole("dialog")).toBeNull()
  clearToken() // simulates api.ts's 401 path: clears the token, fires onAuthRequired
  const input = await screen.findByLabelText(/access token/i)
  await userEvent.type(input, "secret")
  await userEvent.click(screen.getByRole("button", { name: /save/i }))
  expect(getToken()).toBe("secret")
})
