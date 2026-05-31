import { clearToken, getToken } from "./auth"
import type {
  CreateFollowRequest, FollowDTO, JobDTO, LessonDTO, LoginRequest,
  PreviewResponse, SessionResponse, SkipLessonRequest, SummaryDTO,
} from "@/types"

export class ApiHttpError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
    this.name = "ApiHttpError"
  }
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const token = getToken()
  const headers: Record<string, string> = { ...(init.headers as Record<string, string>) }
  if (init.body) headers["Content-Type"] = "application/json"
  if (token) headers.Authorization = `Bearer ${token}`

  const res = await fetch(`/api${path}`, { ...init, headers })
  if (res.status === 401) {
    clearToken() // force the token gate; auth.ts notifies the app
  }
  if (res.status === 204) return undefined as T
  const text = await res.text()
  const data = text ? JSON.parse(text) : undefined
  if (!res.ok) {
    const msg = data && typeof data.error === "string" ? data.error : `HTTP ${res.status}`
    throw new ApiHttpError(res.status, msg)
  }
  return data as T
}

function qs(params: Record<string, string | number | undefined>): string {
  const u = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) if (v !== undefined && v !== "") u.set(k, String(v))
  const s = u.toString()
  return s ? `?${s}` : ""
}

export const api = {
  summary: () => request<SummaryDTO>("/summary"),
  health: () => fetch("/healthz").then((r) => r.json() as Promise<{ status: string; version: string }>),

  listFollows: () => request<FollowDTO[]>("/follows"),
  getFollow: (id: number) => request<FollowDTO>(`/follows/${id}`),
  followLessons: (id: number, status?: string) =>
    request<LessonDTO[]>(`/follows/${id}/lessons${qs({ status })}`),
  createFollow: (body: CreateFollowRequest) =>
    requestWithStatus<FollowDTO>("/follows", { method: "POST", body: JSON.stringify(body) }),
  deleteFollow: (id: number) => request<void>(`/follows/${id}`, { method: "DELETE" }),

  listLessons: (params: { status?: string; limit?: number; offset?: number } = {}) =>
    request<LessonDTO[]>(`/lessons${qs(params)}`),
  getLesson: (id: number) => request<LessonDTO>(`/lessons/${id}`),
  downloadLesson: (id: number) =>
    requestWithStatus<JobDTO>(`/lessons/${id}/download`, { method: "POST" }),
  skipLesson: (id: number, body: SkipLessonRequest = {}) =>
    request<LessonDTO>(`/lessons/${id}/skip`, { method: "POST", body: JSON.stringify(body) }),
  unskipLesson: (id: number) =>
    request<LessonDTO>(`/lessons/${id}/unskip`, { method: "POST" }),

  listJobs: (params: { state?: string; limit?: number } = {}) =>
    request<JobDTO[]>(`/jobs${qs(params)}`),
  getJob: (id: number) => request<JobDTO>(`/jobs/${id}`),
  cancelJob: (id: number) => request<JobDTO>(`/jobs/${id}/cancel`, { method: "POST" }),
  retryJob: (id: number) => request<JobDTO>(`/jobs/${id}/retry`, { method: "POST" }),

  preview: (params: { id?: string; whole?: boolean; slug?: string; brand?: string }) =>
    request<PreviewResponse>(`/preview${qs({ id: params.id, whole: params.whole ? "true" : undefined, slug: params.slug, brand: params.brand })}`),

  getSession: () => request<SessionResponse>("/session"),
  login: (body: LoginRequest) =>
    request<SessionResponse>("/session", { method: "POST", body: JSON.stringify(body) }),

  sync: (dryRun: boolean) =>
    requestWithStatus<{ triggered?: boolean; would_enqueue?: number }>("/sync", {
      method: "POST",
      body: JSON.stringify({ dry_run: dryRun }),
    }),

  pause: () => request<{ paused: boolean }>("/pause", { method: "POST" }),
  resume: () => request<{ paused: boolean }>("/resume", { method: "POST" }),
}

// requestWithStatus returns both the body and the HTTP status so callers can
// distinguish 202 (created/triggered) from 200 (already-active), used by
// download and sync.
export async function requestWithStatus<T>(
  path: string,
  init: RequestInit = {},
): Promise<{ status: number; data: T }> {
  const token = getToken()
  const headers: Record<string, string> = { ...(init.headers as Record<string, string>) }
  if (init.body) headers["Content-Type"] = "application/json"
  if (token) headers.Authorization = `Bearer ${token}`
  const res = await fetch(`/api${path}`, { ...init, headers })
  if (res.status === 401) clearToken()
  const text = await res.text()
  const data = text ? JSON.parse(text) : undefined
  if (!res.ok) {
    const msg = data && typeof data.error === "string" ? data.error : `HTTP ${res.status}`
    throw new ApiHttpError(res.status, msg)
  }
  return { status: res.status, data: data as T }
}
