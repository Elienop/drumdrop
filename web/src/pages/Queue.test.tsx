import { expect, it, onTestFinished } from "vitest"
import { act, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { http, HttpResponse } from "msw"
import { ORIGIN, renderWithProviders, sendEvent, server } from "@/test/msw"
import { clearToken, setToken } from "@/lib/auth"
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
    deleting: false,
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
    deleting: false,
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

  // Lessons' words: a download, named by its lesson.
  const t = await toastWith("Download queued again")
  expect(within(t).getByText("Single Stroke Roll", { selector: "[data-description]" }))
    .toBeInTheDocument()
})

it("cancels a running job and says so in Lessons' words, naming the lesson", async () => {
  server.use(
    ...jobsAndLessons(),
    http.post(`${ORIGIN}/api/jobs/22/cancel`, () =>
      HttpResponse.json({ ...runningJob, status: "canceled" }),
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

  const t = await toastWith("Download canceled")
  expect(within(t).getByText("Double Stroke Roll", { selector: "[data-description]" }))
    .toBeInTheDocument()
})

// The server's sentences, verbatim from internal/server/messages.go.
const NOT_ACTIVE = "This download has already ended, so there's nothing to cancel."
const CANCEL_GONE =
  "This download is no longer in DrumDrop: it was removed meanwhile, elsewhere. There's nothing left to cancel."
const RETRY_GONE =
  "This download is no longer in DrumDrop: it was removed meanwhile, elsewhere. There's nothing left to retry."
const BEING_DELETED =
  "This lesson's files are being deleted right now. Try again once that's finished."
const SERVER_ERROR =
  "This may not have finished: something went wrong on the server. Check the server log, fix the problem, then try again."

// toastWith is the toast whose title is `title`.
const toastWith = async (title: string) =>
  (await screen.findByText(title)).closest<HTMLElement>("[data-sonner-toast]")!

// A neutral note: not red, and no close button, since it goes away by itself
// (failureToast's toasts have one, and stay until it is pressed).
function expectNeutral(t: HTMLElement) {
  expect(t).not.toHaveAttribute("data-type", "error")
  expect(within(t).queryByRole("button", { name: "Close toast" })).not.toBeInTheDocument()
}

// A race with something done elsewhere (the job ended, or was removed) is
// not a failure: a neutral note naming the lesson, and the list refreshes so
// the stale row goes.
it.each([
  ["409: the job had already ended", 409, NOT_ACTIVE, "Already ended"],
  ["404: the job was removed meanwhile", 404, CANCEL_GONE, "Already removed"],
])("a cancel answered %s reads as a neutral note, and refreshes the list", async (_, status, sentence, title) => {
  let listFetches = 0
  server.use(
    http.get(`${ORIGIN}/api/jobs`, () => {
      listFetches++
      return HttpResponse.json([failedJob, runningJob])
    }),
    http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json(lessons)),
    http.post(`${ORIGIN}/api/jobs/22/cancel`, () =>
      HttpResponse.json({ error: sentence }, { status }),
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
  const fetchesBefore = listFetches
  const runningRow = screen.getByText("Double Stroke Roll").closest("tr")!
  await user.click(within(runningRow).getByRole("button", { name: /cancel/i }))

  const t = await toastWith(title)
  expect(within(t).getByText("Double Stroke Roll")).toBeInTheDocument()
  expectNeutral(t)
  expect(screen.queryByText(/^Couldn't cancel/)).not.toBeInTheDocument()
  await waitFor(() => expect(listFetches).toBeGreaterThan(fetchesBefore))
})

it("a retry answered 404 (the job was removed meanwhile) reads as a neutral note, and refreshes the list", async () => {
  let listFetches = 0
  server.use(
    http.get(`${ORIGIN}/api/jobs`, () => {
      listFetches++
      return HttpResponse.json([failedJob, runningJob])
    }),
    http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json(lessons)),
    http.post(`${ORIGIN}/api/jobs/11/retry`, () =>
      HttpResponse.json({ error: RETRY_GONE }, { status: 404 }),
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
  const fetchesBefore = listFetches
  const failedRow = screen.getByText("Single Stroke Roll").closest("tr")!
  await user.click(within(failedRow).getByRole("button", { name: /retry/i }))

  const t = await toastWith("Removed elsewhere")
  expect(within(t).getByText("Single Stroke Roll")).toBeInTheDocument()
  expectNeutral(t)
  expect(screen.queryByText(/^Couldn't retry/)).not.toBeInTheDocument()
  await waitFor(() => expect(listFetches).toBeGreaterThan(fetchesBefore))
})

it("a cancel that really failed stays red with the server's sentence until closed, and refreshes the list", async () => {
  let listFetches = 0
  server.use(
    http.get(`${ORIGIN}/api/jobs`, () => {
      listFetches++
      return HttpResponse.json([failedJob, runningJob])
    }),
    http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json(lessons)),
    http.post(`${ORIGIN}/api/jobs/22/cancel`, () =>
      HttpResponse.json({ error: SERVER_ERROR }, { status: 500 }),
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
  const fetchesBefore = listFetches
  const runningRow = screen.getByText("Double Stroke Roll").closest("tr")!
  await user.click(within(runningRow).getByRole("button", { name: /cancel/i }))

  const t = await toastWith("Couldn't cancel the download of “Double Stroke Roll”")
  expect(t).toHaveAttribute("data-type", "error")
  expect(within(t).getByText(SERVER_ERROR, { selector: "[data-description]" })).toBeInTheDocument()
  expect(within(t).getByRole("button", { name: "Close toast" })).toBeInTheDocument()
  await waitFor(() => expect(listFetches).toBeGreaterThan(fetchesBefore))
})

it("a 409 on retry shows the server's reason (files being deleted), not a guess of our own", async () => {
  server.use(
    ...jobsAndLessons(),
    http.post(`${ORIGIN}/api/jobs/11/retry`, () =>
      HttpResponse.json({ error: BEING_DELETED }, { status: 409 }),
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

  expect(await screen.findByText("Couldn't retry the download of “Single Stroke Roll”")).toBeInTheDocument()
  expect(screen.getByText(BEING_DELETED, { selector: "[data-description]" })).toBeInTheDocument()
  expect(screen.queryByText(/not retryable/i)).not.toBeInTheDocument()
})

it("a cancel answered by a proxy's HTML page toasts a sentence, never a JSON parse error", async () => {
  server.use(
    ...jobsAndLessons(),
    http.post(
      `${ORIGIN}/api/jobs/22/cancel`,
      () =>
        new HttpResponse("<html><body>502 Bad Gateway</body></html>", {
          status: 502,
          headers: { "Content-Type": "text/html" },
        }),
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

  expect(await screen.findByText("Couldn't cancel the download of “Double Stroke Roll”")).toBeInTheDocument()
  expect(
    screen.getByText("Couldn't reach the server, or it answered unexpectedly. Try again."),
  ).toBeInTheDocument()
  expect(screen.queryByText(/JSON|Unexpected token|HTTP 502/)).not.toBeInTheDocument()
})

// A job reads running as soon as its download starts, not when it ends (UI
// review round 5d, Medium 1): the worker emits download_started after it has
// claimed the job and saved its lesson as 'downloading', and the page
// refreshes on it.
it("a queued job reads running once its download starts", async () => {
  onTestFinished(() => act(() => clearToken({ silent: true })))
  const queued: JobDTO = { ...runningJob, id: 21, status: "queued", attempts: 0, started_at: null }
  let started = false
  server.use(
    http.get(`${ORIGIN}/api/jobs`, () =>
      HttpResponse.json([started ? { ...queued, status: "running", attempts: 1 } : queued]),
    ),
    http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json(lessons)),
  )
  setToken("test-token") // the event stream opens only with a stored token
  renderWithProviders(<Queue />)

  const row = () => screen.getByText("Double Stroke Roll").closest("tr")!
  await screen.findByText("Double Stroke Roll")
  expect(within(row()).getByText("queued")).toBeInTheDocument()

  started = true
  act(() =>
    sendEvent({
      kind: "download_started",
      job_id: 21,
      railcontent_id: 200,
      title: "Double Stroke Roll",
      attempt: 1,
      max_attempts: 3,
    }),
  )

  await waitFor(() => expect(within(row()).getByText("running")).toBeInTheDocument())
  expect(within(row()).queryByText("queued")).not.toBeInTheDocument()
  expect(within(row()).getByRole("button", { name: /cancel/i })).toBeEnabled()
})
