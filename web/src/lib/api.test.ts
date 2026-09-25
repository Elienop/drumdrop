import { api, ApiHttpError } from "./api"
import { setToken, clearToken } from "./auth"

const fetchMock = vi.fn()
// Each test stubs fetch; each leaves the global as it found it
// (vite.config.ts does not set unstubGlobals). Found at the start of the
// test, not at load: MSW (test/msw.ts) swaps in its own fetch in a beforeAll.
let foundFetch: typeof fetch
beforeEach(() => {
  clearToken()
  fetchMock.mockReset()
  foundFetch = globalThis.fetch
  vi.stubGlobal("fetch", fetchMock)
})
afterEach(() => {
  vi.unstubAllGlobals()
  expect(globalThis.fetch).toBe(foundFetch)
})

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  })
}

it("prefixes /api and parses JSON", async () => {
  fetchMock.mockResolvedValue(jsonResponse({ follows: 3, lessons: {}, jobs: {} }))
  const out = await api.summary()
  expect(fetchMock).toHaveBeenCalledWith("/api/summary", expect.any(Object))
  expect(out.follows).toBe(3)
})

it("attaches the bearer token when set", async () => {
  setToken("secret")
  fetchMock.mockResolvedValue(jsonResponse([]))
  await api.listFollows()
  const init = fetchMock.mock.calls[0][1]
  expect(init.headers.Authorization).toBe("Bearer secret")
})

it("throws ApiHttpError with the {error} message and status", async () => {
  fetchMock.mockResolvedValue(jsonResponse({ error: "follow not found" }, 404))
  await expect(api.getFollow(1)).rejects.toMatchObject({
    status: 404,
    message: "follow not found",
  })
})

it("marks an error body from the server, and one without it, so the UI can fall back to its own copy", async () => {
  fetchMock.mockResolvedValueOnce(jsonResponse({ error: "follow not found" }, 404))
  await expect(api.getFollow(1)).rejects.toMatchObject({ fromServer: true })

  // A proxy's empty 502: the message is only "HTTP 502", which names no next step.
  fetchMock.mockResolvedValueOnce(new Response(null, { status: 502 }))
  await expect(api.deleteLesson(1)).rejects.toMatchObject({
    status: 502,
    message: "HTTP 502",
    fromServer: false,
  })
  // The same for the requestWithStatus path.
  fetchMock.mockResolvedValueOnce(jsonResponse({ nope: 1 }, 500))
  await expect(api.sync(false)).rejects.toMatchObject({ status: 500, fromServer: false })
})

it("a body that is not JSON (a proxy's HTML page) is an ApiHttpError without a server message, never a SyntaxError", async () => {
  const html = () =>
    new Response("<html><body>502 Bad Gateway</body></html>", {
      status: 502,
      headers: { "Content-Type": "text/html" },
    })
  fetchMock.mockResolvedValueOnce(html())
  await expect(api.cancelJob(1)).rejects.toMatchObject({
    name: "ApiHttpError",
    status: 502,
    fromServer: false,
  })
  // requestWithStatus reads its body the same way.
  fetchMock.mockResolvedValueOnce(html())
  await expect(api.sync(false)).rejects.toMatchObject({ name: "ApiHttpError", fromServer: false })
  // Even on a 2xx: a success we cannot read is not a success.
  fetchMock.mockResolvedValueOnce(new Response("<html></html>", { status: 200 }))
  await expect(api.summary()).rejects.toMatchObject({ name: "ApiHttpError", fromServer: false })
})

it("clears the token on 401", async () => {
  setToken("bad")
  fetchMock.mockResolvedValue(jsonResponse({ error: "unauthorized" }, 401))
  await expect(api.summary()).rejects.toBeInstanceOf(ApiHttpError)
  expect(localStorage.getItem("drumdrop_api_token")).toBeNull()
})
