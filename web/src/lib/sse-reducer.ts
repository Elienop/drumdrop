import type { EventKind, ProgressEvent } from "@/types"

export interface ActiveDownload {
  jobId: number
  railcontentId: number
  title: string
  pct: number
  speed: string
  bytes: number
  totalBytes: number
  attempt: number
  maxAttempts: number
}

export interface SSEState {
  active: Record<number, ActiveDownload> // keyed by job_id
  recent: ProgressEvent[] // newest first, bounded
  syncing: boolean
  lastCycle: { planned: number; processed: number } | null
}

const RECENT_MAX = 50

export function initialSSEState(): SSEState {
  return { active: {}, recent: [], syncing: false, lastCycle: null }
}

function upsertActive(state: SSEState, e: ProgressEvent): Record<number, ActiveDownload> {
  const prev = state.active[e.job_id]
  // Identity fields carry forward when an event omits them (a download_progress
  // event carries no title/railcontent_id). Progress fields are taken verbatim
  // from a download_progress event — including a legitimate 0 (start / retry
  // reset) — and carried forward on non-progress kinds (job_claimed /
  // download_started), so a real 0% is never mistaken for "absent" and lost.
  const isProgress = e.kind === "download_progress"
  const next: ActiveDownload = {
    jobId: e.job_id,
    railcontentId: e.railcontent_id || prev?.railcontentId || 0,
    title: e.title || prev?.title || "",
    attempt: e.attempt || prev?.attempt || 0,
    maxAttempts: e.max_attempts || prev?.maxAttempts || 0,
    pct: isProgress ? e.pct : prev?.pct ?? e.pct,
    speed: isProgress ? e.speed : prev?.speed ?? e.speed,
    bytes: isProgress ? e.bytes : prev?.bytes ?? e.bytes,
    totalBytes: isProgress ? e.total_bytes : prev?.totalBytes ?? e.total_bytes,
  }
  return { ...state.active, [e.job_id]: next }
}

function removeActive(state: SSEState, jobId: number): Record<number, ActiveDownload> {
  const { [jobId]: _gone, ...rest } = state.active
  return rest
}

export function sseReducer(state: SSEState, e: ProgressEvent): SSEState {
  const recent = [e, ...state.recent].slice(0, RECENT_MAX)
  switch (e.kind) {
    case "job_claimed":
    case "download_started":
    case "download_progress":
      return { ...state, active: upsertActive(state, e), recent }
    case "download_ok":
    case "attempt_failed":
    case "lesson_skipped":
      return { ...state, active: removeActive(state, e.job_id), recent }
    case "cycle_started":
      return { ...state, syncing: true, recent }
    case "cycle_done":
      return { ...state, syncing: false, lastCycle: { planned: e.planned, processed: e.processed }, recent }
    default:
      return { ...state, recent }
  }
}

// seedFromSnapshot applies the SSE "ready" frame. The frame carries the hub's
// LAST event PER KIND (not the live active set), so reducing it blindly would
// create phantom active downloads (a stale download_started seeds a job that
// already finished). The worker is strictly sequential — at most ONE download
// is active — so reconstruct the current download from the most recent
// download-lifecycle event by timestamp: an in-flight kind seeds it; a terminal
// kind means nothing is active. Cycle status comes from cycle_started/_done.
const INFLIGHT_KINDS = new Set<EventKind>(["job_claimed", "download_started", "download_progress"])
const LIFECYCLE_KINDS = new Set<EventKind>([
  "job_claimed", "download_started", "download_progress",
  "download_ok", "attempt_failed", "lesson_skipped",
])

export function seedFromSnapshot(state: SSEState, snapshot: ProgressEvent[]): SSEState {
  let next: SSEState = { ...state }

  const latestLifecycle = snapshot
    .filter((e) => LIFECYCLE_KINDS.has(e.kind))
    .reduce<ProgressEvent | null>(
      (acc, e) => (!acc || Date.parse(e.time) >= Date.parse(acc.time) ? e : acc),
      null,
    )
  if (latestLifecycle && INFLIGHT_KINDS.has(latestLifecycle.kind)) {
    next = { ...next, active: upsertActive(next, latestLifecycle) }
  }

  const started = snapshot.find((e) => e.kind === "cycle_started")
  const done = snapshot.find((e) => e.kind === "cycle_done")
  if (started && (!done || Date.parse(started.time) > Date.parse(done.time))) next.syncing = true
  if (done) next.lastCycle = { planned: done.planned, processed: done.processed }
  return next
}

// invalidationKeys returns the TanStack Query keys to invalidate for an event.
// Events that follow a write to persisted state (lessons/jobs/summary) refresh
// it; pure progress does not. download_started is emitted right after the
// worker saves the lesson as 'downloading' (and its job is running), so the
// row's badge, note and menu, and the Queue's job row, move on at the start of
// a download, not only when it ends. Refreshing keeps the cached data on
// screen while it refetches, and live progress lives in this reducer, not in
// the query cache, so neither flickers.
export function invalidationKeys(e: ProgressEvent): (readonly string[])[] {
  switch (e.kind) {
    case "download_started":
    case "download_ok":
    case "attempt_failed":
    case "lesson_skipped":
    case "cycle_done":
      return [["lessons"], ["jobs"], ["summary"]]
    default:
      return []
  }
}
