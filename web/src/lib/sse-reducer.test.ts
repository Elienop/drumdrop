import { initialSSEState, sseReducer, seedFromSnapshot, invalidationKeys } from "./sse-reducer"
import type { ProgressEvent } from "@/types"

function ev(p: Partial<ProgressEvent>): ProgressEvent {
  return {
    kind: "download_progress", railcontent_id: 0, job_id: 0, follow_id: 0, title: "",
    attempt: 0, max_attempts: 0, pct: 0, bytes: 0, total_bytes: 0, speed: "", error: "",
    planned: 0, processed: 0, time: "2026-05-30T12:00:00Z", ...p,
  }
}

it("opens an active download on download_started", () => {
  let s = initialSSEState()
  s = sseReducer(s, ev({ kind: "download_started", job_id: 1, railcontent_id: 50, title: "Lesson A" }))
  expect(s.active[1]).toMatchObject({ jobId: 1, railcontentId: 50, title: "Lesson A", pct: 0 })
})

it("updates pct/speed on download_progress", () => {
  let s = initialSSEState()
  s = sseReducer(s, ev({ kind: "download_started", job_id: 1, railcontent_id: 50 }))
  s = sseReducer(s, ev({ kind: "download_progress", job_id: 1, pct: 64.2, speed: "3.1MiB/s", bytes: 10, total_bytes: 20 }))
  expect(s.active[1]).toMatchObject({ pct: 64.2, speed: "3.1MiB/s", bytes: 10, totalBytes: 20 })
})

it("closes the active download on download_ok and attempt_failed", () => {
  let s = initialSSEState()
  s = sseReducer(s, ev({ kind: "download_started", job_id: 1 }))
  s = sseReducer(s, ev({ kind: "download_started", job_id: 2 }))
  s = sseReducer(s, ev({ kind: "download_ok", job_id: 1 }))
  s = sseReducer(s, ev({ kind: "attempt_failed", job_id: 2, error: "boom" }))
  expect(s.active[1]).toBeUndefined()
  expect(s.active[2]).toBeUndefined()
})

it("tracks the current cycle on cycle_started/cycle_done", () => {
  let s = initialSSEState()
  s = sseReducer(s, ev({ kind: "cycle_started" }))
  expect(s.syncing).toBe(true)
  s = sseReducer(s, ev({ kind: "cycle_done", planned: 8, processed: 3 }))
  expect(s.syncing).toBe(false)
  expect(s.lastCycle).toEqual({ planned: 8, processed: 3 })
})

it("keeps a bounded recent-events log (newest first)", () => {
  let s = initialSSEState()
  for (let i = 0; i < 60; i++) s = sseReducer(s, ev({ kind: "download_ok", job_id: i }))
  expect(s.recent.length).toBeLessThanOrEqual(50)
  expect(s.recent[0].job_id).toBe(59)
})

it("seeds the in-flight download from a single progress snapshot", () => {
  const snap: ProgressEvent[] = [ev({ kind: "download_progress", job_id: 7, pct: 30, time: "2026-05-30T12:00:05Z" })]
  const s = seedFromSnapshot(initialSSEState(), snap)
  expect(s.active[7]?.pct).toBe(30)
})

it("does not seed phantom downloads from a last-event-per-kind snapshot", () => {
  // The hub sends the last event of EACH kind. The most recent lifecycle event
  // here is a terminal download_ok, so (worker is sequential) nothing is active.
  const snap: ProgressEvent[] = [
    ev({ kind: "download_started", job_id: 1, time: "2026-05-30T12:00:00Z" }),
    ev({ kind: "download_progress", job_id: 1, pct: 80, time: "2026-05-30T12:00:01Z" }),
    ev({ kind: "download_ok", job_id: 1, time: "2026-05-30T12:00:02Z" }),
    ev({ kind: "cycle_done", planned: 5, processed: 5, time: "2026-05-30T11:59:00Z" }),
  ]
  const s = seedFromSnapshot(initialSSEState(), snap)
  expect(Object.keys(s.active)).toHaveLength(0)
  expect(s.lastCycle).toEqual({ planned: 5, processed: 5 })
})

it("seeds the active download when the latest lifecycle event is in-flight", () => {
  const snap: ProgressEvent[] = [
    ev({ kind: "download_ok", job_id: 1, time: "2026-05-30T12:00:00Z" }),
    ev({ kind: "download_progress", job_id: 2, pct: 42, time: "2026-05-30T12:00:03Z" }),
  ]
  const s = seedFromSnapshot(initialSSEState(), snap)
  expect(Object.keys(s.active)).toEqual(["2"])
  expect(s.active[2]?.pct).toBe(42)
})

describe("invalidationKeys", () => {
  it("invalidates lessons+jobs+summary on terminal events", () => {
    for (const kind of ["download_ok", "attempt_failed", "lesson_skipped", "cycle_done"] as const) {
      expect(invalidationKeys(ev({ kind }))).toEqual(
        expect.arrayContaining([["lessons"], ["jobs"], ["summary"]]),
      )
    }
  })
  // The worker emits download_started right after it saves the lesson as
  // 'downloading' (internal/scheduler/worker.go, StartDownload then Emit), so
  // the lesson's row and its job's row must refresh then, not at the end.
  it("invalidates lessons+jobs+summary when a download starts", () => {
    expect(invalidationKeys(ev({ kind: "download_started" }))).toEqual(
      expect.arrayContaining([["lessons"], ["jobs"], ["summary"]]),
    )
  })
  it("does not invalidate on pure progress", () => {
    expect(invalidationKeys(ev({ kind: "download_progress" }))).toEqual([])
  })
})
