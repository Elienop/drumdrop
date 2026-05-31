/* eslint-disable react-refresh/only-export-components */
import { createElement, type ReactElement } from "react"
import { render } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { MemoryRouter } from "react-router-dom"
import { setupServer } from "msw/node"
import { http, HttpResponse } from "msw"
import { SSEProvider } from "@/lib/sse"
import type { JobDTO, SummaryDTO } from "@/types"

// Default summary used by the dashboard cards. Maps are zero-filled across the
// full enum vocabulary, mirroring the Go DTO contract.
const defaultSummary: SummaryDTO = {
  follows: 0,
  lessons: { downloaded: 0, pending: 0, downloading: 0, failed: 0, skipped: 0 },
  jobs: { queued: 0, running: 0, done: 0, failed: 0, canceled: 0 },
  paused: false,
}

const defaultJobs: JobDTO[] = []

// Absolute origin so handlers match the api client's relative fetch (jsdom
// resolves "/api/x" against location.origin; MSW relative handler paths do not
// match that absolute request, so anchor every handler to the live origin).
export const ORIGIN = window.location.origin

// The shared MSW server. Tests override individual routes with server.use(...).
export const server = setupServer(
  http.get(`${ORIGIN}/api/summary`, () => HttpResponse.json(defaultSummary)),
  http.get(`${ORIGIN}/api/jobs`, () => HttpResponse.json(defaultJobs)),
  http.get(`${ORIGIN}/api/session`, () => HttpResponse.json({ connected: false })),
)

// MockEventSource stands in for the browser EventSource that the SSEProvider
// opens — jsdom has none. It never emits, so the live-downloads view starts
// empty; tests that exercise SSE drive the reducer directly elsewhere.
class MockEventSource {
  url: string
  onmessage: ((e: MessageEvent) => void) | null = null
  onopen: (() => void) | null = null
  onerror: (() => void) | null = null
  constructor(url: string) {
    this.url = url
  }
  addEventListener() {}
  close() {}
}

if (typeof globalThis.EventSource === "undefined") {
  globalThis.EventSource = MockEventSource as unknown as typeof EventSource
}

// renderWithProviders wraps the UI in the providers the pages rely on:
// QueryClientProvider (fresh, retry-off so error states settle deterministically),
// SSEProvider (useSSE), and a MemoryRouter.
export function renderWithProviders(ui: ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    createElement(
      QueryClientProvider,
      { client: qc },
      createElement(SSEProvider, null, createElement(MemoryRouter, null, ui)),
    ),
  )
}
