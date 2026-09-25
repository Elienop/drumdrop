/* eslint-disable react-refresh/only-export-components */
import { createElement, type ReactElement } from "react"
import { render } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { MemoryRouter } from "react-router-dom"
import { setupServer } from "msw/node"
import { http, HttpResponse } from "msw"
import { SSEProvider } from "@/lib/sse"
import type { JobDTO, ProgressEvent, SummaryDTO } from "@/types"

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
// opens — jsdom has none. It emits nothing by itself, so the live-downloads
// view starts empty. The SSEProvider opens it only once a token is stored; a
// test that stores one can then push a server event with sendEvent.
// The most recently opened stream: sendEvent's target.
let lastEventSource: MockEventSource | null = null

class MockEventSource {
  url: string
  onmessage: ((e: MessageEvent) => void) | null = null
  onopen: (() => void) | null = null
  onerror: (() => void) | null = null
  closed = false
  constructor(url: string) {
    this.url = url
    lastEventSource = this
  }
  addEventListener() {}
  close() {
    this.closed = true
  }
}

// sendEvent delivers one server event on the page's open stream, as the
// SSEProvider receives a live frame. Wrap it in act(). It throws when no
// stream is open, so a test that forgot to store a token fails loudly instead
// of sending into nothing.
export function sendEvent(event: Partial<ProgressEvent> & Pick<ProgressEvent, "kind">) {
  const es = lastEventSource
  if (!es || es.closed) throw new Error("sendEvent: no open event stream (store a token first)")
  const full: ProgressEvent = {
    railcontent_id: 0,
    job_id: 0,
    follow_id: 0,
    title: "",
    attempt: 0,
    max_attempts: 0,
    pct: 0,
    bytes: 0,
    total_bytes: 0,
    speed: "",
    error: "",
    planned: 0,
    processed: 0,
    time: new Date().toISOString(),
    ...event,
  }
  es.onmessage?.({ data: JSON.stringify(full) } as MessageEvent)
}

if (typeof globalThis.EventSource === "undefined") {
  globalThis.EventSource = MockEventSource as unknown as typeof EventSource
}

// renderWithProviders wraps the UI in the providers the pages rely on:
// QueryClientProvider (fresh, retry-off so error states settle deterministically),
// SSEProvider (useSSE), and a MemoryRouter. Pass `client` to seed or inspect
// the cache from the test (e.g. whether a key the page does not mount was
// invalidated); it is returned as `qc` either way.
export function newTestQueryClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

// `route` is the router's starting location (e.g. "/lessons?follow=1").
export function renderWithProviders(
  ui: ReactElement,
  { client, route = "/" }: { client?: QueryClient; route?: string } = {},
) {
  const qc = client ?? newTestQueryClient()
  const result = render(
    createElement(
      QueryClientProvider,
      { client: qc },
      createElement(
        SSEProvider,
        null,
        createElement(MemoryRouter, { initialEntries: [route] }, ui),
      ),
    ),
  )
  return { ...result, qc }
}
