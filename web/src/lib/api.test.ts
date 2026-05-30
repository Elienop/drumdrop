import { api, ApiHttpError } from "./api"
import { setToken, clearToken } from "./auth"

const fetchMock = vi.fn()
beforeEach(() => {
  clearToken()
  fetchMock.mockReset()
  vi.stubGlobal("fetch", fetchMock)
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

it("clears the token on 401", async () => {
  setToken("bad")
  fetchMock.mockResolvedValue(jsonResponse({ error: "unauthorized" }, 401))
  await expect(api.summary()).rejects.toBeInstanceOf(ApiHttpError)
  expect(localStorage.getItem("drumdrop_api_token")).toBeNull()
})
