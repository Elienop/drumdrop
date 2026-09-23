// Wire types mirroring the Go DTOs (snake_case). Nullable columns are `T | null`.

export type LessonStatus = "pending" | "downloading" | "downloaded" | "failed" | "skipped"
export type JobStatus = "queued" | "running" | "done" | "failed" | "canceled"

export interface FollowDTO {
  id: number
  kind: "node" | "instructor"
  railcontent_id: number | null
  slug: string | null
  title: string
  brand: string
  quality: string
  added_at: string
  last_synced_at: string | null
}

export interface LessonDTO {
  railcontent_id: number
  title: string
  parent_railcontent_id: number | null
  brand: string
  status: LessonStatus
  quality: string | null
  output_dir: string | null
  video_path: string | null
  // has_files is true exactly when deleting the lesson would have files to act
  // on (its downloads folder or library entries are on record), whatever its
  // status. The server owns that predicate; output_dir alone does not say it.
  has_files: boolean
  // deleting is true while a delete of this lesson is in progress (always
  // present). The row then shows that and offers no action that would race
  // the delete.
  deleting: boolean
  bytes: number | null
  // error is the failure of a failed lesson, or the reason given for a skipped
  // one ("deleted" for a lesson whose files were deleted).
  error: string | null
  follow_id: number | null
  first_seen_at: string | null
  downloaded_at: string | null
  updated_at: string | null
}

export interface JobDTO {
  id: number
  follow_id: number | null
  railcontent_id: number
  status: JobStatus
  attempts: number
  error: string | null
  created_at: string | null
  started_at: string | null
  finished_at: string | null
}

export interface SummaryDTO {
  follows: number
  lessons: Record<string, number> // every LessonStatus, zero-filled
  jobs: Record<string, number> // every JobStatus, zero-filled
  paused: boolean // the daemon's pause flag (deps.IsPaused)
}

export interface PreviewResponse {
  root_id?: number // node only (omitted for instructor)
  title: string
  lesson_count: number
  kind: "node" | "instructor"
}

export interface SessionResponse {
  connected: boolean
}

// Request bodies
export interface CreateFollowRequest {
  kind: "node" | "instructor"
  id?: string
  url?: string
  slug?: string
  brand?: string
  quality?: string
}
export interface UpdateFollowRequest {
  quality: string
}
export interface SkipLessonRequest {
  reason?: string
}
export interface LoginRequest {
  email: string
  password: string
}

// SSE
export type EventKind =
  | "job_claimed"
  | "download_started"
  | "download_progress"
  | "download_ok"
  | "attempt_failed"
  | "lesson_skipped"
  | "cycle_started"
  | "cycle_done"

export interface ProgressEvent {
  kind: EventKind
  railcontent_id: number
  job_id: number
  follow_id: number
  title: string
  attempt: number
  max_attempts: number
  pct: number
  bytes: number
  total_bytes: number
  speed: string
  error: string
  planned: number
  processed: number
  time: string
}
