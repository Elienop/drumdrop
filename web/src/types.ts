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
  bytes: number | null
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
export interface SkipLessonRequest {
  reason?: string
}
export interface LoginRequest {
  email: string
  password: string
}
export interface SyncRequest {
  dry_run?: boolean
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

export interface ApiError {
  error: string
}
