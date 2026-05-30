import { render, screen, act } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { SSEProvider, useSSE } from "./sse"

class MockEventSource {
  static last: MockEventSource | null = null
  url: string
  listeners: Record<string, ((e: MessageEvent) => void)[]> = {}
  onmessage: ((e: MessageEvent) => void) | null = null
  constructor(url: string) { this.url = url; MockEventSource.last = this }
  addEventListener(t: string, cb: (e: MessageEvent) => void) {
    (this.listeners[t] ??= []).push(cb)
  }
  emit(type: string, data: unknown) {
    const e = { data: JSON.stringify(data) } as MessageEvent
    if (type === "message") this.onmessage?.(e)
    else this.listeners[type]?.forEach((cb) => cb(e))
  }
  close() {}
}

function Probe() {
  const { state } = useSSE()
  return <div>active:{Object.keys(state.active).length}</div>
}

it("seeds from ready and applies live events", () => {
  vi.stubGlobal("EventSource", MockEventSource as unknown as typeof EventSource)
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
