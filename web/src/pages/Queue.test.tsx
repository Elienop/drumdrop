import { expect, it } from "vitest"
import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { http, HttpResponse } from "msw"
import { ORIGIN, renderWithProviders, server } from "@/test/msw"
import { Toaster } from "@/components/ui/sonner"
import type { JobDTO, LessonDTO } from "@/types"
import { Queue } from "./Queue"

const failedJob: JobDTO = {
  id: 11,
  follow_id: null,
  railcontent_id: 100,
  status: "failed",
  attempts: 3,
  error: "ffmpeg exited 1",
  created_at: "2026-05-29T00:00:00Z",
  started_at: "2026-05-29T00:01:00Z",
  finished_at: "2026-05-29T00:02:00Z",
}

const runningJob: JobDTO = {
  id: 22,
  follow_id: null,
  railcontent_id: 200,
  status: "running",
  attempts: 1,
  error: null,
  created_at: "2026-05-30T00:00:00Z",
  started_at: "2026-05-30T00:01:00Z",
  finished_at: null,
}

const lessons: LessonDTO[] = [
  {
    railcontent_id: 100,
    title: "Single Stroke Roll",
    parent_railcontent_id: null,
    brand: "drumeo",
    status: "failed",
    quality: "1080p",
    output_dir: null,
    has_files: false,
    video_path: null,
    bytes: null,
    error: null,
    follow_id: null,
    first_seen_at: "2026-05-01T00:00:00Z",
    downloaded_at: null,
    updated_at: "2026-05-29T00:00:00Z",
  },
  {
    railcontent_id: 200,
    title: "Double Stroke Roll",
    parent_railcontent_id: null,
    brand: "drumeo",
    status: "downloading",
    quality: "1080p",
    output_dir: null,
    has_files: false,
    video_path: null,
    bytes: null,
    error: null,
    follow_id: null,
    first_seen_at: "2026-05-02T00:00:00Z",
    downloaded_at: null,
    updated_at: "2026-05-30T00:00:00Z",
  },
]

function jobsAndLessons() {
  return [
    http.get(`${ORIGIN}/api/jobs`, () => HttpResponse.json([failedJob, runningJob])),
    http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json(lessons)),
  ]
}

it("gates cancel/retry by job status and resolves lesson titles", async () => {
  server.use(...jobsAndLessons())
  renderWithProviders(<Queue />)

  // Titles resolve from the lessons cache.
  expect(await screen.findByText("Single Stroke Roll")).toBeInTheDocument()
  expect(screen.getByText("Double Stroke Roll")).toBeInTheDocument()

  const failedRow = screen.getByText("Single Stroke Roll").closest("tr")!
  const runningRow = screen.getByText("Double Stroke Roll").closest("tr")!

  // Failed job: Retry enabled, Cancel disabled.
  expect(within(failedRow).getByRole("button", { name: /retry/i })).toBeEnabled()
  expect(within(failedRow).getByRole("button", { name: /cancel/i })).toBeDisabled()

  // Running job: Cancel enabled, Retry disabled.
  expect(within(runningRow).getByRole("button", { name: /cancel/i })).toBeEnabled()
  expect(within(runningRow).getByRole("button", { name: /retry/i })).toBeDisabled()
})

it("retries a failed job (202) and shows a success toast", async () => {
  server.use(
    ...jobsAndLessons(),
    http.post(`${ORIGIN}/api/jobs/11/retry`, () =>
      HttpResponse.json({ ...failedJob, status: "queued" }, { status: 202 }),
    ),
  )
  const user = userEvent.setup()
  renderWithProviders(
    <>
      <Queue />
      <Toaster />
    </>,
  )

  await screen.findByText("Single Stroke Roll")
  const failedRow = screen.getByText("Single Stroke Roll").closest("tr")!
  await user.click(within(failedRow).getByRole("button", { name: /retry/i }))

  expect(await screen.findByText(/retr(y|ied|ying)/i, { selector: "[data-sonner-toast] *" }))
    .toBeInTheDocument()
})

it("shows an 'already finished' toast when cancel returns 409", async () => {
  server.use(
    ...jobsAndLessons(),
    http.post(`${ORIGIN}/api/jobs/22/cancel`, () =>
      HttpResponse.json({ error: "job is not active" }, { status: 409 }),
    ),
  )
  const user = userEvent.setup()
  renderWithProviders(
    <>
      <Queue />
      <Toaster />
    </>,
  )

  await screen.findByText("Double Stroke Roll")
  const runningRow = screen.getByText("Double Stroke Roll").closest("tr")!
  await user.click(within(runningRow).getByRole("button", { name: /cancel/i }))

  expect(await screen.findByText(/already finished/i)).toBeInTheDocument()
})
