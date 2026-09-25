import { expect, it } from "vitest"
import { ApiHttpError } from "./api"
import { cancelOutcome, errorMessage, itemOutcome, UNREACHABLE } from "./errors"

const JOB_ENDED = "This download has already ended, so there's nothing to cancel."
const CANCEL_GONE =
  "This download is no longer in DrumDrop: it was removed meanwhile, elsewhere. There's nothing left to cancel."

it("a cancel answered 409 (the job had already ended) is 'already-ended', not a failure", async () => {
  await expect(cancelOutcome(Promise.reject(new ApiHttpError(409, JOB_ENDED)))).resolves.toBe(
    "already-ended",
  )
})

it("a cancel answered 404 is 'already-gone', and one that worked is 'done'", async () => {
  await expect(cancelOutcome(Promise.reject(new ApiHttpError(404, CANCEL_GONE)))).resolves.toBe(
    "already-gone",
  )
  await expect(cancelOutcome(Promise.resolve(undefined))).resolves.toBe("done")
})

it("a cancel's 409 without a server message, or any other failure, stays a failure", async () => {
  const proxy = new ApiHttpError(409, "HTTP 409", false)
  await expect(cancelOutcome(Promise.reject(proxy))).rejects.toBe(proxy)
  const broken = new ApiHttpError(500, "This may not have finished.")
  await expect(cancelOutcome(Promise.reject(broken))).rejects.toBe(broken)
})

it("a request on an item that succeeds is 'done'", async () => {
  await expect(itemOutcome(Promise.resolve(undefined))).resolves.toBe("done")
})

it("the API's own 404 means the item is already gone: done, not a failure", async () => {
  const notFound = new ApiHttpError(404, "lesson not found")
  await expect(itemOutcome(Promise.reject(notFound))).resolves.toBe("already-gone")
})

it("a 404 without a server message (a proxy's page) stays a failure: nothing says the item is gone", async () => {
  const proxy = new ApiHttpError(404, "HTTP 404", false)
  await expect(itemOutcome(Promise.reject(proxy))).rejects.toBe(proxy)
})

it("any other failure stays a failure", async () => {
  const refused = new ApiHttpError(409, "a delete of this lesson is already running")
  await expect(itemOutcome(Promise.reject(refused))).rejects.toBe(refused)
})

it("shows the server's sentence, and our own for anything else (never 'HTTP 502' or a parse error)", () => {
  expect(errorMessage(new ApiHttpError(500, "could not delete the files"))).toBe(
    "could not delete the files",
  )
  expect(errorMessage(new ApiHttpError(502, "HTTP 502", false))).toBe(UNREACHABLE)
  expect(errorMessage(new SyntaxError("Unexpected token '<'"))).toBe(UNREACHABLE)
  expect(errorMessage(new TypeError("Failed to fetch"), "Custom.")).toBe("Custom.")
})
