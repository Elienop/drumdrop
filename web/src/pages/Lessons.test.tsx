import { beforeAll, describe, expect, it } from "vitest"
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { delay, http, HttpResponse } from "msw"
import { newTestQueryClient, ORIGIN, renderWithProviders, server } from "@/test/msw"
import { qk } from "@/lib/queryKeys"
import { Toaster } from "@/components/ui/sonner"
import type { JobDTO, LessonDTO } from "@/types"
import { Lessons } from "./Lessons"

// Radix DropdownMenu (used for the row actions) reads pointer-capture APIs and
// scrollIntoView that jsdom does not implement; stub them so the menu opens.
beforeAll(() => {
  if (!Element.prototype.hasPointerCapture)
    Element.prototype.hasPointerCapture = () => false
  if (!Element.prototype.scrollIntoView)
    Element.prototype.scrollIntoView = () => {}
})

const lessons: LessonDTO[] = [
  {
    railcontent_id: 100,
    title: "Single Stroke Roll",
    parent_railcontent_id: null,
    brand: "drumeo",
    status: "downloaded",
    quality: "1080p",
    output_dir: "/media/drumeo/100",
    video_path: "/media/drumeo/100/video.mp4",
    bytes: 524288000,
    error: null,
    follow_id: null,
    first_seen_at: "2026-05-01T00:00:00Z",
    downloaded_at: "2026-05-28T00:00:00Z",
    updated_at: "2026-05-28T00:00:00Z",
  },
  {
    railcontent_id: 200,
    title: "Double Stroke Roll",
    parent_railcontent_id: null,
    brand: "drumeo",
    status: "pending",
    quality: "1080p",
    output_dir: null,
    video_path: null,
    bytes: null,
    error: null,
    follow_id: null,
    first_seen_at: "2026-05-02T00:00:00Z",
    downloaded_at: null,
    updated_at: "2026-05-29T00:00:00Z",
  },
  {
    railcontent_id: 300,
    title: "Paradiddle",
    parent_railcontent_id: null,
    brand: "drumeo",
    status: "downloading",
    quality: "1080p",
    output_dir: "/media/drumeo/300",
    video_path: null,
    bytes: null,
    error: null,
    follow_id: null,
    first_seen_at: "2026-05-03T00:00:00Z",
    downloaded_at: null,
    updated_at: "2026-05-30T00:00:00Z",
  },
]

const job: JobDTO = {
  id: 42,
  follow_id: null,
  railcontent_id: 200,
  status: "queued",
  attempts: 0,
  error: null,
  created_at: "2026-05-30T00:00:00Z",
  started_at: null,
  finished_at: null,
}

// A running job for the downloading lesson (railcontent 300), used to resolve
// the Cancel action's job id.
const runningJob: JobDTO = {
  id: 77,
  follow_id: null,
  railcontent_id: 300,
  status: "running",
  attempts: 1,
  error: null,
  created_at: "2026-05-30T00:00:00Z",
  started_at: "2026-05-30T00:01:00Z",
  finished_at: null,
}

const skippedLesson: LessonDTO = {
  railcontent_id: 400,
  title: "Flam Tap",
  parent_railcontent_id: null,
  brand: "drumeo",
  status: "skipped",
  quality: "1080p",
  output_dir: null,
  video_path: null,
  bytes: null,
  error: null,
  follow_id: null,
  first_seen_at: "2026-05-04T00:00:00Z",
  downloaded_at: null,
  updated_at: "2026-05-30T00:00:00Z",
}

it("renders lessons with status badges and human sizes", async () => {
  server.use(http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json(lessons)))
  renderWithProviders(<Lessons />)

  expect(await screen.findByText("Single Stroke Roll")).toBeInTheDocument()
  expect(screen.getByText("Double Stroke Roll")).toBeInTheDocument()
  expect(screen.getByText("Paradiddle")).toBeInTheDocument()
  // StatusBadge renders the status text. Scope to the table body — the status
  // tabs share the same words ("Downloaded"/"Pending"), so an unscoped query
  // matches multiple elements.
  const table = screen.getByRole("table")
  expect(within(table).getByText("downloaded")).toBeInTheDocument()
  expect(within(table).getByText("pending")).toBeInTheDocument()
  // formatBytes(524288000) === "500.0 MB"
  expect(within(table).getByText("500.0 MB")).toBeInTheDocument()
})

it("queues a download (202) and shows a 'Queued' toast", async () => {
  server.use(
    http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json(lessons)),
    http.post(`${ORIGIN}/api/lessons/200/download`, () =>
      HttpResponse.json(job, { status: 202 }),
    ),
  )
  const user = userEvent.setup()
  renderWithProviders(
    <>
      <Lessons />
      <Toaster />
    </>,
  )

  await screen.findByText("Double Stroke Roll")
  await user.click(screen.getByRole("button", { name: /actions for double stroke roll/i }))
  await user.click(await screen.findByRole("menuitem", { name: /download/i }))

  // Anchor so this cannot match the 200 case's "Already queued" — blanking the
  // 202 branch must fail this test.
  expect(await screen.findByText(/^queued/i)).toBeInTheDocument()
  expect(screen.queryByText(/already queued/i)).not.toBeInTheDocument()
})

it("offers Cancel (not Download) for a downloading lesson and hits /jobs/{id}/cancel", async () => {
  let canceledId: string | null = null
  server.use(
    http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json(lessons)),
    http.get(`${ORIGIN}/api/jobs`, () => HttpResponse.json([runningJob])),
    http.post(`${ORIGIN}/api/jobs/:id/cancel`, ({ params }) => {
      canceledId = params.id as string
      return HttpResponse.json({ ...runningJob, status: "canceled" })
    }),
  )
  const user = userEvent.setup()
  renderWithProviders(
    <>
      <Lessons />
      <Toaster />
    </>,
  )

  await screen.findByText("Paradiddle")
  await user.click(screen.getByRole("button", { name: /actions for paradiddle/i }))
  // Cancel is offered; Download is not.
  expect(await screen.findByRole("menuitem", { name: /cancel/i })).toBeInTheDocument()
  expect(screen.queryByRole("menuitem", { name: /download/i })).not.toBeInTheDocument()

  await user.click(screen.getByRole("menuitem", { name: /cancel/i }))
  await waitFor(() => expect(canceledId).toBe("77"))
})

it("offers Un-skip for a skipped lesson and hits /lessons/{id}/unskip", async () => {
  let unskippedId: string | null = null
  server.use(
    http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json([skippedLesson])),
    http.post(`${ORIGIN}/api/lessons/:id/unskip`, ({ params }) => {
      unskippedId = params.id as string
      return HttpResponse.json({ ...skippedLesson, status: "pending" })
    }),
  )
  const user = userEvent.setup()
  renderWithProviders(
    <>
      <Lessons />
      <Toaster />
    </>,
  )

  await screen.findByText("Flam Tap")
  await user.click(screen.getByRole("button", { name: /actions for flam tap/i }))
  await user.click(await screen.findByRole("menuitem", { name: /un-?skip/i }))
  await waitFor(() => expect(unskippedId).toBe("400"))
})

it("shows 'Already queued' when download returns 200", async () => {
  server.use(
    http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json(lessons)),
    http.post(`${ORIGIN}/api/lessons/200/download`, () =>
      HttpResponse.json(job, { status: 200 }),
    ),
  )
  const user = userEvent.setup()
  renderWithProviders(
    <>
      <Lessons />
      <Toaster />
    </>,
  )

  await screen.findByText("Double Stroke Roll")
  await user.click(screen.getByRole("button", { name: /actions for double stroke roll/i }))
  await user.click(await screen.findByRole("menuitem", { name: /download/i }))

  await waitFor(() =>
    expect(screen.getByText(/already queued/i)).toBeInTheDocument(),
  )
})

it("deletes a downloaded lesson via DELETE /api/lessons/{id}, confirms, and invalidates the lessons list", async () => {
  let deletedId: string | null = null
  // Count GET /api/lessons: the page mounts one list query, so a successful
  // delete that invalidates ["lessons"] must trigger a SECOND list fetch.
  // Dropping the ["lessons"] invalidate from deleteLesson.onSuccess leaves
  // listFetches at 1 and fails here.
  let listFetches = 0
  server.use(
    http.get(`${ORIGIN}/api/lessons`, () => {
      listFetches++
      return HttpResponse.json(lessons)
    }),
    http.delete(`${ORIGIN}/api/lessons/:id`, ({ params }) => {
      deletedId = params.id as string
      return HttpResponse.json({
        ...lessons[0],
        status: "skipped",
        error: "deleted",
        output_dir: null,
        video_path: null,
        bytes: null,
      })
    }),
  )
  const user = userEvent.setup()
  renderWithProviders(
    <>
      <Lessons />
      <Toaster />
    </>,
  )

  await screen.findByText("Single Stroke Roll")
  await waitFor(() => expect(listFetches).toBe(1))
  // The downloaded lesson (railcontent 100) offers Delete, not Download/Skip.
  await user.click(screen.getByRole("button", { name: /actions for single stroke roll/i }))
  expect(await screen.findByRole("menuitem", { name: /delete/i })).toBeInTheDocument()
  expect(screen.queryByRole("menuitem", { name: /download/i })).not.toBeInTheDocument()
  await user.click(screen.getByRole("menuitem", { name: /delete/i }))

  // Confirm in the dialog before the request fires.
  const dialog = await screen.findByRole("dialog")
  expect(deletedId).toBeNull()
  await user.click(within(dialog).getByRole("button", { name: /^delete$/i }))

  await waitFor(() => expect(deletedId).toBe("100"))
  expect(await screen.findByText(/lesson deleted/i)).toBeInTheDocument()
  // The invalidate refetched the list (dropped ["lessons"] invalidate stays at 1).
  await waitFor(() => expect(listFetches).toBe(2))
})

// --- Delete: failure paths, and which lessons can be deleted ----------------

// The server's fixed 500 when a lesson's files could not all be removed.
const FILES_KEPT =
  "could not delete the lesson's files; the lesson was kept (see the server log)"
// The server's 409 when a download recorded new files during the delete.
const CHANGED = "the lesson was downloaded again while it was being deleted; try again"

// A re-download in flight: status downloading, but its earlier download's
// files are still on record (output_dir set).
const redownloading: LessonDTO = {
  ...lessons[0],
  railcontent_id: 500,
  title: "Moeller Method",
  status: "downloading",
}

function renderLessons() {
  const qc = newTestQueryClient()
  // Seed the summary: the Lessons page does not mount it, but the top bar and
  // the dashboard do, so a delete must mark it stale.
  qc.setQueryData(qk.summary, { follows: 0, lessons: {}, jobs: {}, paused: false })
  renderWithProviders(
    <>
      <Lessons />
      <Toaster />
    </>,
    { client: qc },
  )
  return qc
}

async function openDelete(user: ReturnType<typeof userEvent.setup>, title: string) {
  await user.click(await screen.findByRole("button", { name: `Actions for ${title}` }))
  await user.click(await screen.findByRole("menuitem", { name: /delete/i }))
  return screen.findByRole("dialog")
}

it("a failed delete (500) keeps the dialog open with the server's message and refreshes lessons, jobs and summary", async () => {
  // Before the delete the lesson is re-downloading. The server removes its job
  // first, so after the failed file removal it reads skipped/canceled; the
  // list must show that, not the stale "downloading" row.
  let listFetches = 0
  let jobFetches = 0
  server.use(
    http.get(`${ORIGIN}/api/lessons`, async () => {
      listFetches++
      if (listFetches === 1) return HttpResponse.json([redownloading])
      await delay(20) // the refetch lands after the DELETE answers, as in a browser
      return HttpResponse.json([{ ...redownloading, status: "skipped", error: "canceled" }])
    }),
    http.get(`${ORIGIN}/api/jobs`, () => {
      jobFetches++
      return HttpResponse.json([])
    }),
    http.delete(`${ORIGIN}/api/lessons/:id`, () =>
      HttpResponse.json({ error: FILES_KEPT }, { status: 500 }),
    ),
  )
  const user = userEvent.setup()
  const qc = renderLessons()

  const table = await screen.findByRole("table")
  expect(within(table).getByText("downloading")).toBeInTheDocument()
  await waitFor(() => expect(jobFetches).toBe(1))
  expect(qc.getQueryState(qk.summary)?.isInvalidated).toBe(false)

  const dialog = await openDelete(user, "Moeller Method")
  await user.click(within(dialog).getByRole("button", { name: /^delete$/i }))

  // The server's own words, inside the dialog that is still open, and they
  // arrive together with the refreshed list (getBy, not findBy: no alert
  // beside a row that still reads "downloading").
  expect(await within(dialog).findByRole("alert")).toHaveTextContent(FILES_KEPT)
  expect(within(table).getByText("skipped")).toBeInTheDocument()
  expect(screen.getByRole("dialog")).toBe(dialog)
  expect(
    within(dialog).getByRole("button", { name: /^delete$/i }),
  ).toHaveAccessibleDescription(FILES_KEPT)
  expect(screen.queryByText(/lesson deleted/i)).not.toBeInTheDocument()

  // Every view reflects the server again.
  expect(within(table).queryByText("downloading")).not.toBeInTheDocument()
  expect(listFetches).toBe(2)
  expect(jobFetches).toBe(2)
  expect(qc.getQueryState(qk.summary)?.isInvalidated).toBe(true)
})

it("a 409 shows the server's message and the same dialog retries the delete", async () => {
  let deletes = 0
  server.use(
    http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json([lessons[0]])),
    http.delete(`${ORIGIN}/api/lessons/:id`, () => {
      deletes++
      if (deletes === 1) return HttpResponse.json({ error: CHANGED }, { status: 409 })
      return HttpResponse.json({ ...lessons[0], status: "skipped", output_dir: null })
    }),
  )
  const user = userEvent.setup()
  renderLessons()

  const dialog = await openDelete(user, "Single Stroke Roll")
  await user.click(within(dialog).getByRole("button", { name: /^delete$/i }))
  expect(await within(dialog).findByRole("alert")).toHaveTextContent(CHANGED)

  await user.click(within(dialog).getByRole("button", { name: /^delete$/i }))
  expect(await screen.findByText(/lesson deleted/i)).toBeInTheDocument()
  await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())
  expect(deletes).toBe(2)
})

it("the delete dialog cannot be dismissed while the request is in flight", async () => {
  let answer: (r: Response) => void = () => {}
  server.use(
    http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json([lessons[0]])),
    http.delete(
      `${ORIGIN}/api/lessons/:id`,
      () => new Promise<Response>((resolve) => (answer = resolve)),
    ),
  )
  const user = userEvent.setup()
  renderLessons()

  const dialog = await openDelete(user, "Single Stroke Roll")
  await user.click(within(dialog).getByRole("button", { name: /^delete$/i }))
  expect(within(dialog).getByRole("button", { name: /^cancel$/i })).toBeDisabled()
  await user.keyboard("{Escape}")
  expect(screen.getByRole("dialog")).toBe(dialog)

  // The answer then lands where the user can read it.
  answer(HttpResponse.json({ error: FILES_KEPT }, { status: 500 }))
  expect(await within(dialog).findByRole("alert")).toHaveTextContent(FILES_KEPT)
})

it("closing the dialog after a failure clears the message", async () => {
  server.use(
    http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json([lessons[0]])),
    http.delete(`${ORIGIN}/api/lessons/:id`, () =>
      HttpResponse.json({ error: FILES_KEPT }, { status: 500 }),
    ),
  )
  const user = userEvent.setup()
  renderLessons()

  let dialog = await openDelete(user, "Single Stroke Roll")
  await user.click(within(dialog).getByRole("button", { name: /^delete$/i }))
  await within(dialog).findByRole("alert")
  await user.click(within(dialog).getByRole("button", { name: /^cancel$/i }))
  await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())

  dialog = await openDelete(user, "Single Stroke Roll")
  expect(within(dialog).queryByRole("alert")).not.toBeInTheDocument()
})

describe("Delete is offered exactly when the lesson still records files, whatever its status", () => {
  const withFiles = (status: LessonDTO["status"], error: string | null): LessonDTO => ({
    ...lessons[0],
    railcontent_id: 600,
    title: `Has files (${status}, ${error ?? "no error"})`,
    status,
    error,
  })
  const noFiles = (status: LessonDTO["status"]): LessonDTO => ({
    ...lessons[1],
    railcontent_id: 700,
    title: `No files (${status})`,
    status,
  })

  it.each([
    withFiles("skipped", "canceled"), // a canceled re-download (D63)
    withFiles("skipped", "locked"), // a re-download SkipDownload marked skipped
    withFiles("failed", "yt-dlp exited 1"), // a failed re-download
    withFiles("pending", null), // a retried job reset the lesson to pending
    withFiles("downloading", null), // a re-download under way
    withFiles("downloaded", null),
  ])("offers Delete for $title and sends DELETE for it", async (lesson) => {
    let deletedId: string | null = null
    server.use(
      http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json([lesson])),
      http.delete(`${ORIGIN}/api/lessons/:id`, ({ params }) => {
        deletedId = params.id as string
        return HttpResponse.json({ ...lesson, status: "skipped", output_dir: null })
      }),
    )
    const user = userEvent.setup()
    renderLessons()

    const dialog = await openDelete(user, lesson.title)
    await user.click(within(dialog).getByRole("button", { name: /^delete$/i }))
    await waitFor(() => expect(deletedId).toBe("600"))
  })

  it.each([noFiles("skipped"), noFiles("pending"), noFiles("failed"), noFiles("downloading")])(
    "does not offer Delete for $title",
    async (lesson) => {
      server.use(http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json([lesson])))
      const user = userEvent.setup()
      renderLessons()

      await user.click(
        await screen.findByRole("button", { name: `Actions for ${lesson.title}` }),
      )
      await screen.findByRole("menuitem", { name: /copy path/i })
      expect(screen.queryByRole("menuitem", { name: /delete/i })).not.toBeInTheDocument()
    },
  )
})
