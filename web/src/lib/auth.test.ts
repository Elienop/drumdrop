import { getToken, setToken, clearToken, subscribe, TOKEN_KEY } from "./auth"

beforeEach(() => localStorage.clear())

describe("token store", () => {
  it("uses one key and round-trips", () => {
    expect(getToken()).toBeNull()
    setToken("abc")
    expect(localStorage.getItem(TOKEN_KEY)).toBe("abc")
    expect(getToken()).toBe("abc")
  })
  it("clears", () => {
    setToken("abc")
    clearToken()
    expect(getToken()).toBeNull()
  })
  it("notifies subscribers on set and clear", () => {
    const seen: (string | null)[] = []
    const unsub = subscribe((t) => seen.push(t))
    setToken("x")
    clearToken()
    unsub()
    setToken("y")
    expect(seen).toEqual(["x", null])
  })
})
