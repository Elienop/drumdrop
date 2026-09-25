import { render, screen, act } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { SSEProvider, useSSE } from "./sse"
import { setToken, clearToken } from "./auth"

class MockEventSource {
  static last: MockEventSource | null = null
  url: string
  listeners: Record<string, ((e: MessageEvent) => void)[]> = {}
  onmessage: ((e: MessageEvent) => void) | null = null
  closed = false
  constructor(url: string) { this.url = url; MockEventSource.last = this }
  addEventListener(t: string, cb: (e: MessageEvent) => void) {
    (this.listeners[t] ??= []).push(cb)
  }
  emit(type: string, data: unknown) {
    const e = { data: JSON.stringify(data) } as MessageEvent
    if (type === "message") this.onmessage?.(e)
    else this.listeners[type]?.forEach((cb) => cb(e))
  }
  close() { this.closed = true }
}

// Each test stubs EventSource; each leaves the global as it found it
// (vite.config.ts does not set unstubGlobals).
let foundEventSource: typeof EventSource
beforeEach(() => {
  foundEventSource = globalThis.EventSource
})
afterEach(() => {
  vi.unstubAllGlobals()
  expect(globalThis.EventSource).toBe(foundEventSource)
  clearToken({ silent: true })
  MockEventSource.last = null
})

function Probe() {
  const { state } = useSSE()
  return <div>active:{Object.keys(state.active).length}</div>
}

it("seeds from ready and applies live events", () => {
  vi.stubGlobal("EventSource", MockEventSource as unknown as typeof EventSource)
  setToken("test-token") // the stream only opens once a credential is stored
  const qc = new QueryClient()
  render(
    <QueryClientProvider client={qc}>
      <SSEProvider>
        <Probe />
      </SSEProvider>
    </QueryClientProvider>,
  )
  const es = MockEventSource.last!
  act(() => es.emit("ready", [{ kind: "download_progress", job_id: 1, pct: 10 }]))
  expect(screen.getByText("active:1")).toBeInTheDocument()
  act(() => es.emit("message", { kind: "download_ok", job_id: 1 }))
  expect(screen.getByText("active:0")).toBeInTheDocument()
})

it("opens with the stored token and reconnects when the token changes", () => {
  vi.stubGlobal("EventSource", MockEventSource as unknown as typeof EventSource)
  setToken("tok-1")
  const qc = new QueryClient()
  render(
    <QueryClientProvider client={qc}>
      <SSEProvider>
        <Probe />
      </SSEProvider>
    </QueryClientProvider>,
  )
  // The initial connection carries the stored token in the query param.
  const first = MockEventSource.last!
  expect(first.url).toContain("access_token=tok-1")

  // Setting a new token (the Settings/gate path) must tear down the old stream and
  // open a fresh one with the new credential — no page reload required.
  act(() => setToken("tok-2"))
  expect(first.closed).toBe(true)
  expect(MockEventSource.last!.url).toContain("access_token=tok-2")
  expect(MockEventSource.last).not.toBe(first)
})

it("does not open a connection without a token", () => {
  vi.stubGlobal("EventSource", MockEventSource as unknown as typeof EventSource)
  // No token stored (afterEach cleared it): SSEProvider must not open a doomed,
  // 401-looping stream.
  const qc = new QueryClient()
  render(
    <QueryClientProvider client={qc}>
      <SSEProvider>
        <Probe />
      </SSEProvider>
    </QueryClientProvider>,
  )
  expect(MockEventSource.last).toBeNull()
})
