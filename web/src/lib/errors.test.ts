import { expect, it } from "vitest"
import { ApiHttpError } from "./api"
import { errorMessage, itemOutcome, UNREACHABLE } from "./errors"

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
