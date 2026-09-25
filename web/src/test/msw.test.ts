import { sendEvent } from "./msw"

// A test that sends a server event before any stream is open (no token was
// stored, so the SSEProvider opened none) must fail loudly, not send it into
// nothing and pass on a page that never received it.
it("sendEvent throws while no event stream is open", () => {
  expect(() => sendEvent({ kind: "download_progress" })).toThrow(/no open event stream/)
})
