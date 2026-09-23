import { beforeAll, describe, expect, it, vi } from "vitest"
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { delay, http, HttpResponse } from "msw"
import { toast } from "sonner"
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
    has_files: true,
    deleting: false,
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
    has_files: false,
    deleting: false,
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
    has_files: true,
    deleting: false,
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
  has_files: false,
  deleting: false,
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
  // "Cancel download" is offered (it cancels the download, not a dialog);
  // Download is not.
  expect(
    await screen.findByRole("menuitem", { name: "Cancel download" }),
  ).toBeInTheDocument()
  expect(screen.queryByRole("menuitem", { name: "Download" })).not.toBeInTheDocument()

  await user.click(screen.getByRole("menuitem", { name: "Cancel download" }))
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

// --- Delete ------------------------------------------------------------------

// The server's fixed 500 when a lesson's files could not all be removed.
const FILES_KEPT =
  "could not delete the lesson's files; the lesson was kept (see the server log)"
// The server's 409 when a download recorded new files during the delete.
const CHANGED = "the lesson was downloaded again while it was being deleted; try again"

// A re-download in flight: status downloading, but its earlier download's
// files are still on record.
const redownloading: LessonDTO = {
  ...lessons[0],
  railcontent_id: 500,
  title: "Moeller Method",
  status: "downloading",
}

const tombstoned = (l: LessonDTO): LessonDTO => ({
  ...l,
  status: "skipped",
  error: "deleted",
  output_dir: null,
  video_path: null,
  bytes: null,
  has_files: false,
  deleting: false,
})

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

type User = ReturnType<typeof userEvent.setup>

async function openRowAction(user: User, title: string, action: RegExp) {
  const trigger = await screen.findByRole("button", { name: `Actions for ${title}` })
  await user.click(trigger)
  await user.click(await screen.findByRole("menuitem", { name: action }))
  const dialog = await screen.findByRole("alertdialog")
  return { trigger, dialog }
}

const openDelete = async (user: User, title: string) =>
  (await openRowAction(user, title, /^delete$/i)).dialog

const confirmButton = (dialog: HTMLElement, name: RegExp = /^delete$/i) =>
  within(dialog).getByRole("button", { name })

it("deletes a downloaded lesson via DELETE /api/lessons/{id}, confirms, and invalidates the lessons list", async () => {
  let deletedId: string | null = null
  // Count GET /api/lessons: the page mounts one list query, so a successful
  // delete that invalidates ["lessons"] must trigger a SECOND list fetch.
  let listFetches = 0
  server.use(
    http.get(`${ORIGIN}/api/lessons`, () => {
      listFetches++
      return HttpResponse.json(lessons)
    }),
    http.delete(`${ORIGIN}/api/lessons/:id`, ({ params }) => {
      deletedId = params.id as string
      return HttpResponse.json(tombstoned(lessons[0]))
    }),
  )
  const user = userEvent.setup()
  renderLessons()

  await screen.findByText("Single Stroke Roll")
  await waitFor(() => expect(listFetches).toBe(1))
  // The downloaded lesson (railcontent 100) offers Delete, not Download/Skip.
  await user.click(screen.getByRole("button", { name: /actions for single stroke roll/i }))
  expect(await screen.findByRole("menuitem", { name: /^delete$/i })).toBeInTheDocument()
  expect(screen.queryByRole("menuitem", { name: /^download$/i })).not.toBeInTheDocument()
  await user.click(screen.getByRole("menuitem", { name: /^delete$/i }))

  // Confirm in the alert dialog before the request fires. Its title quotes the
  // lesson with typographic quotes.
  const dialog = await screen.findByRole("alertdialog", {
    name: "Delete “Single Stroke Roll”?",
  })
  expect(deletedId).toBeNull()
  await user.click(confirmButton(dialog))

  await waitFor(() => expect(deletedId).toBe("100"))
  expect(await screen.findByText(/lesson deleted/i)).toBeInTheDocument()
  // The refresh landed before the dialog closed.
  expect(listFetches).toBe(2)
  expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument()
})

it("the Delete menu item has no icon, like every other item in the menu", async () => {
  server.use(http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json([lessons[0]])))
  const user = userEvent.setup()
  renderLessons()

  await user.click(await screen.findByRole("button", { name: "Actions for Single Stroke Roll" }))
  const del = await screen.findByRole("menuitem", { name: /^delete$/i })
  expect(del.querySelector("svg")).toBeNull()
})

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
  await user.click(confirmButton(dialog))

  // The server's own words, inside the dialog that is still open, and they
  // arrive together with the refreshed list (getBy, not findBy: no message
  // beside a row that still reads "downloading").
  await waitFor(() => expect(within(dialog).getByRole("alert")).toHaveTextContent(FILES_KEPT))
  expect(within(table).getByText("skipped")).toBeInTheDocument()
  expect(screen.getByRole("alertdialog")).toBe(dialog)
  expect(confirmButton(dialog)).toHaveAccessibleDescription(FILES_KEPT)
  // The server may already have acted: dismissing is "Close", not "Cancel".
  expect(within(dialog).getByRole("button", { name: "Close" })).toBeEnabled()
  expect(within(dialog).queryByRole("button", { name: "Cancel" })).not.toBeInTheDocument()
  expect(screen.queryByText(/lesson deleted/i)).not.toBeInTheDocument()

  // Every view reflects the server again.
  expect(within(table).queryByText("downloading")).not.toBeInTheDocument()
  expect(listFetches).toBe(2)
  expect(jobFetches).toBe(2)
  expect(qc.getQueryState(qk.summary)?.isInvalidated).toBe(true)
})

it("a failure without a server message (an empty 502) shows copy that names the next step", async () => {
  server.use(
    http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json([lessons[0]])),
    http.delete(`${ORIGIN}/api/lessons/:id`, () => new HttpResponse(null, { status: 502 })),
  )
  const user = userEvent.setup()
  renderLessons()

  const dialog = await openDelete(user, "Single Stroke Roll")
  await user.click(confirmButton(dialog))
  await waitFor(() =>
    expect(within(dialog).getByRole("alert")).toHaveTextContent(
      "Couldn't reach the server, or it answered unexpectedly. Try again.",
    ),
  )
})

it("while the delete runs the dialog says so, keeps focus on the confirm button, and cannot be dismissed", async () => {
  let answer: (r: Response) => void = () => {}
  let deletes = 0
  server.use(
    http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json([lessons[0]])),
    http.delete(`${ORIGIN}/api/lessons/:id`, () => {
      deletes++
      return new Promise<Response>((resolve) => (answer = resolve))
    }),
  )
  const user = userEvent.setup()
  renderLessons()

  const dialog = await openDelete(user, "Single Stroke Roll")
  // Keyboard confirm: focus is on Cancel (the safe default), Tab to Delete.
  expect(within(dialog).getByRole("button", { name: "Cancel" })).toHaveFocus()
  await user.tab()
  expect(confirmButton(dialog)).toHaveFocus()
  await user.keyboard("{Enter}")

  // Pending is visible and focus stays put: aria-disabled, never disabled
  // (a disabled button would drop focus to <body>).
  const pendingButton = await within(dialog).findByRole("button", { name: "Deleting…" })
  expect(pendingButton).toHaveAttribute("aria-disabled", "true")
  expect(pendingButton).toBeEnabled()
  expect(pendingButton).toHaveFocus()
  expect(within(dialog).getByRole("button", { name: "Cancel" })).toBeDisabled()

  // Neither Escape nor a second press does anything while it runs.
  await user.keyboard("{Escape}")
  await user.keyboard("{Enter}")
  expect(screen.getByRole("alertdialog")).toBe(dialog)
  expect(deletes).toBe(1)

  // The answer lands where the user can read it, and focus has not moved, so
  // Enter retries.
  answer(HttpResponse.json({ error: FILES_KEPT }, { status: 500 }))
  await waitFor(() => expect(within(dialog).getByRole("alert")).toHaveTextContent(FILES_KEPT))
  expect(confirmButton(dialog)).toHaveFocus()
  await user.keyboard("{Enter}")
  await waitFor(() => expect(deletes).toBe(2))
})

it("the failure region is always present, keeps its message through a retry, and a repeated failure is a new announcement", async () => {
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
  // The live region exists, empty, before anything failed: a region inserted
  // together with its text is missed by some screen readers.
  const region = within(dialog).getByRole("alert")
  expect(region).toBeEmptyDOMElement()

  await user.click(confirmButton(dialog))
  answer(HttpResponse.json({ error: FILES_KEPT }, { status: 500 }))
  await waitFor(() => expect(region).toHaveTextContent(FILES_KEPT))
  const first = region.firstElementChild

  // Retry: while it runs, the message stays (the dialog does not shrink and
  // regrow under the pointer).
  await user.click(confirmButton(dialog))
  await within(dialog).findByRole("button", { name: "Deleting…" })
  expect(region).toHaveTextContent(FILES_KEPT)
  expect(region.firstElementChild).toBe(first)

  // The same failure again: same region, new node, so it is announced again.
  answer(HttpResponse.json({ error: FILES_KEPT }, { status: 500 }))
  await within(dialog).findByRole("button", { name: /^delete$/i })
  expect(within(dialog).getByRole("alert")).toBe(region)
  expect(region).toHaveTextContent(FILES_KEPT)
  expect(region.firstElementChild).not.toBe(first)
})

it("a 409 shows the server's message and the same dialog retries the delete", async () => {
  let deletes = 0
  server.use(
    http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json([lessons[0]])),
    http.delete(`${ORIGIN}/api/lessons/:id`, () => {
      deletes++
      if (deletes === 1) return HttpResponse.json({ error: CHANGED }, { status: 409 })
      return HttpResponse.json(tombstoned(lessons[0]))
    }),
  )
  const user = userEvent.setup()
  renderLessons()

  const dialog = await openDelete(user, "Single Stroke Roll")
  await user.click(confirmButton(dialog))
  await waitFor(() => expect(within(dialog).getByRole("alert")).toHaveTextContent(CHANGED))

  await user.click(confirmButton(dialog))
  expect(await screen.findByText(/lesson deleted/i)).toBeInTheDocument()
  await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument())
  expect(deletes).toBe(2)
})

it("closing the dialog after a failure clears the message and returns focus to the row's Actions button", async () => {
  server.use(
    http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json([lessons[0]])),
    http.delete(`${ORIGIN}/api/lessons/:id`, () =>
      HttpResponse.json({ error: FILES_KEPT }, { status: 500 }),
    ),
  )
  const user = userEvent.setup()
  renderLessons()

  let { trigger, dialog } = await openRowAction(user, "Single Stroke Roll", /^delete$/i)
  await user.click(confirmButton(dialog))
  await waitFor(() => expect(within(dialog).getByRole("alert")).toHaveTextContent(FILES_KEPT))
  await user.click(within(dialog).getByRole("button", { name: "Close" }))
  await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument())
  await waitFor(() => expect(trigger).toHaveFocus())
  // Nothing left the page unclickable (Radix's modal layers restore it).
  expect(document.body.style.pointerEvents).not.toBe("none")

  ;({ trigger, dialog } = await openRowAction(user, "Single Stroke Roll", /^delete$/i))
  expect(within(dialog).getByRole("alert")).toBeEmptyDOMElement()
  expect(within(dialog).getByRole("button", { name: "Cancel" })).toBeInTheDocument()
})

it("Escape on the delete dialog returns focus to the row's Actions button", async () => {
  server.use(http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json(lessons)))
  const user = userEvent.setup()
  renderLessons()

  const { trigger } = await openRowAction(user, "Paradiddle", /^delete$/i)
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument())
  await waitFor(() => expect(trigger).toHaveFocus())
})

it("after a delete removes the row, focus goes to the next row's Actions button, and to the heading when no row is left", async () => {
  const second: LessonDTO = { ...lessons[0], railcontent_id: 101, title: "Flam Accent" }
  let listed: LessonDTO[] = [lessons[0], second]
  server.use(
    http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json(listed)),
    http.delete(`${ORIGIN}/api/lessons/:id`, ({ params }) => {
      // As in a status tab: the deleted lesson leaves the list.
      listed = listed.filter((l) => String(l.railcontent_id) !== params.id)
      return HttpResponse.json(tombstoned(lessons[0]))
    }),
  )
  const user = userEvent.setup()
  renderLessons()

  let { dialog } = await openRowAction(user, "Single Stroke Roll", /^delete$/i)
  await user.click(confirmButton(dialog))
  await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument())
  const next = screen.getByRole("button", { name: "Actions for Flam Accent" })
  await waitFor(() => expect(next).toHaveFocus())

  ;({ dialog } = await openRowAction(user, "Flam Accent", /^delete$/i))
  await user.click(confirmButton(dialog))
  await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument())
  await waitFor(() =>
    expect(screen.getByRole("heading", { level: 1, name: "Lessons" })).toHaveFocus(),
  )
})

it("announces a successful delete only once the dialog is gone and focus has returned", async () => {
  const seen: { dialogOpen: boolean; focus: Element | null; hidden: boolean }[] = []
  const spy = vi.spyOn(toast, "success").mockImplementation(() => {
    seen.push({
      dialogOpen: document.querySelector('[role="alertdialog"]') !== null,
      focus: document.activeElement,
      // While a modal is open Radix marks the rest of the page aria-hidden, the
      // toaster included: a toast then is shown but not heard.
      hidden: document.querySelector('[aria-hidden="true"][data-aria-hidden]') !== null,
    })
    return 0
  })
  try {
    server.use(
      http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json([lessons[0]])),
      http.delete(`${ORIGIN}/api/lessons/:id`, () => HttpResponse.json(tombstoned(lessons[0]))),
    )
    const user = userEvent.setup()
    renderLessons()

    const { trigger, dialog } = await openRowAction(user, "Single Stroke Roll", /^delete$/i)
    await user.click(confirmButton(dialog))
    await waitFor(() => expect(seen).toHaveLength(1))
    expect(seen[0]).toEqual({ dialogOpen: false, focus: trigger, hidden: false })
    expect(spy).toHaveBeenCalledWith("Lesson deleted", { description: "Single Stroke Roll" })
  } finally {
    spy.mockRestore()
  }
})

describe("Delete is offered exactly when the server says the lesson has files, whatever its status", () => {
  // output_dir is deliberately the OPPOSITE of has_files in both helpers: the
  // server's has_files is the predicate, output_dir alone is not.
  const withFiles = (status: LessonDTO["status"], error: string | null): LessonDTO => ({
    ...lessons[0],
    railcontent_id: 600,
    title: `Has files (${status}, ${error ?? "no error"})`,
    status,
    error,
    output_dir: null,
    has_files: true,
    deleting: false,
  })
  const noFiles = (status: LessonDTO["status"]): LessonDTO => ({
    ...lessons[1],
    railcontent_id: 700,
    title: `No files (${status})`,
    status,
    output_dir: "/media/drumeo/700",
    has_files: false,
    deleting: false,
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
        return HttpResponse.json(tombstoned(lesson))
      }),
    )
    const user = userEvent.setup()
    renderLessons()

    const dialog = await openDelete(user, lesson.title)
    await user.click(confirmButton(dialog))
    await waitFor(() => expect(deletedId).toBe("600"))
  })

  it.each([
    noFiles("skipped"),
    noFiles("pending"),
    noFiles("failed"),
    noFiles("downloading"),
    noFiles("downloaded"),
  ])("does not offer Delete for $title", async (lesson) => {
    server.use(http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json([lesson])))
    const user = userEvent.setup()
    renderLessons()

    await user.click(
      await screen.findByRole("button", { name: `Actions for ${lesson.title}` }),
    )
    await screen.findByRole("menuitem", { name: /copy path/i })
    expect(screen.queryByRole("menuitem", { name: /^delete$/i })).not.toBeInTheDocument()
  })
})

// --- Skip --------------------------------------------------------------------

const SKIP_DELETING =
  "This lesson's files are being deleted right now, and that skips it anyway. If it still isn't skipped in a moment, Skip again."

it("a failed skip shows the server's message inside the dialog, not as a toast", async () => {
  server.use(
    http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json([lessons[1]])),
    http.post(`${ORIGIN}/api/lessons/:id/skip`, () =>
      HttpResponse.json({ error: SKIP_DELETING }, { status: 409 }),
    ),
  )
  const user = userEvent.setup()
  renderLessons()

  const { dialog } = await openRowAction(user, "Double Stroke Roll", /^skip$/i)
  expect(dialog).toHaveAccessibleName("Skip “Double Stroke Roll”?")
  await user.click(confirmButton(dialog, /^skip$/i))
  await waitFor(() => expect(within(dialog).getByRole("alert")).toHaveTextContent(SKIP_DELETING))
  // Only inside the dialog: no toast carries it.
  expect(screen.getAllByText(SKIP_DELETING)).toHaveLength(1)
  expect(screen.getByRole("alertdialog")).toBe(dialog)
})

it("a 404 on skip (the lesson was removed meanwhile) closes the dialog as done, naming the lesson, and drops the row", async () => {
  let gone = false
  server.use(
    http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json(gone ? [] : [lessons[1]])),
    http.post(`${ORIGIN}/api/lessons/:id/skip`, () => {
      gone = true
      return HttpResponse.json(
        { error: "This lesson isn't in DrumDrop anymore: its follow was removed meanwhile." },
        { status: 404 },
      )
    }),
  )
  const user = userEvent.setup()
  renderLessons()

  const { dialog } = await openRowAction(user, "Double Stroke Roll", /^skip$/i)
  await user.click(confirmButton(dialog, /^skip$/i))

  await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument())
  expect(await screen.findByText("Already removed")).toBeInTheDocument()
  expect(
    screen.getByText("Double Stroke Roll", { selector: "[data-description]" }),
  ).toBeInTheDocument()
  expect(screen.queryByText("Lesson skipped")).not.toBeInTheDocument()
  expect(screen.queryByText(/isn't in DrumDrop anymore/)).not.toBeInTheDocument()
  // The refresh after the 404 dropped the row: nothing is left to skip again.
  await waitFor(() =>
    expect(
      screen.queryByRole("button", { name: "Actions for Double Stroke Roll" }),
    ).not.toBeInTheDocument(),
  )
})

it("skips with the typed reason, closes, returns focus and then announces it", async () => {
  let body: { reason?: string } | null = null
  server.use(
    http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json([lessons[1]])),
    http.post(`${ORIGIN}/api/lessons/:id/skip`, async ({ request }) => {
      body = (await request.json()) as { reason?: string }
      return HttpResponse.json({ ...lessons[1], status: "skipped" })
    }),
  )
  const user = userEvent.setup()
  renderLessons()

  const { trigger, dialog } = await openRowAction(user, "Double Stroke Roll", /^skip$/i)
  await user.type(within(dialog).getByLabelText("Reason (optional)"), "not for me")
  await user.click(confirmButton(dialog, /^skip$/i))
  expect(await screen.findByText("Lesson skipped")).toBeInTheDocument()
  expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument()
  expect(body).toEqual({ reason: "not for me" })
  await waitFor(() => expect(trigger).toHaveFocus())
})

it("Enter in the skip reason skips, and the reason's label dims with its field while the skip runs", async () => {
  let body: { reason?: string } | null = null
  let answer: () => void = () => {}
  server.use(
    http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json([lessons[1]])),
    http.post(`${ORIGIN}/api/lessons/:id/skip`, async ({ request }) => {
      body = (await request.json()) as { reason?: string }
      await new Promise<void>((resolve) => (answer = resolve))
      return HttpResponse.json({ ...lessons[1], status: "skipped" })
    }),
  )
  const user = userEvent.setup()
  renderLessons()

  const { dialog } = await openRowAction(user, "Double Stroke Roll", /^skip$/i)
  // It says what Skip does now: the download stops and what it wrote goes.
  // It promises nothing about earlier files: yt-dlp's --force-overwrites
  // deletes a kept video as soon as a re-download starts (BACKLOG D66).
  expect(dialog).toHaveAccessibleDescription(
    "Any queued or running download of it stops, and what that download had written is discarded. Syncs leave a skipped lesson alone until you un-skip it.",
  )
  const reason = within(dialog).getByLabelText("Reason (optional)")
  await user.type(reason, "too hard{Enter}")

  await waitFor(() => expect(body).toEqual({ reason: "too hard" }))
  expect(confirmButton(dialog, /skipping/i)).toHaveFocus()
  expect(reason).toBeDisabled()
  // The Label's group-data-[disabled=true] style dims it with the field.
  expect(reason.closest(".group")).toHaveAttribute("data-disabled", "true")
  answer()
  await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument())
})

// --- A delete that already happened elsewhere ---------------------------------

it("a 404 on delete (removed elsewhere first) closes the dialog as done, with no retry offered", async () => {
  server.use(
    http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json([lessons[0]])),
    http.delete(`${ORIGIN}/api/lessons/:id`, () =>
      HttpResponse.json({ error: "lesson not found" }, { status: 404 }),
    ),
  )
  const user = userEvent.setup()
  renderLessons()

  const { trigger, dialog } = await openRowAction(user, "Single Stroke Roll", /^delete$/i)
  await user.click(confirmButton(dialog))
  await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument())
  expect(await screen.findByText("Already removed")).toBeInTheDocument()
  expect(screen.getByText("Single Stroke Roll", { selector: "[data-description]" })).toBeInTheDocument()
  expect(screen.queryByText("lesson not found")).not.toBeInTheDocument()
  await waitFor(() => expect(trigger).toHaveFocus())
})

// --- Row actions never show "HTTP 502" ------------------------------------------

it("a row action that fails without a server message toasts the outcome and our own sentence, never 'HTTP 502'", async () => {
  server.use(
    http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json([lessons[1]])),
    http.post(`${ORIGIN}/api/lessons/200/download`, () => new HttpResponse(null, { status: 502 })),
  )
  const user = userEvent.setup()
  renderLessons()

  await user.click(await screen.findByRole("button", { name: "Actions for Double Stroke Roll" }))
  await user.click(await screen.findByRole("menuitem", { name: /download/i }))

  expect(await screen.findByText("Couldn't queue “Double Stroke Roll”")).toBeInTheDocument()
  expect(
    screen.getByText("Couldn't reach the server, or it answered unexpectedly. Try again.", {
      selector: "[data-description]",
    }),
  ).toBeInTheDocument()
  expect(screen.queryByText(/HTTP 502/)).not.toBeInTheDocument()
  // A toast with a sentence to read stays until closed (the app's one rule).
  expect(screen.getByRole("button", { name: "Close toast" })).toBeInTheDocument()
})

it("a list that fails without a server message says so in a sentence, never 'HTTP 502'", async () => {
  server.use(http.get(`${ORIGIN}/api/lessons`, () => new HttpResponse(null, { status: 502 })))
  renderLessons()
  expect(
    await screen.findByText(
      "Couldn't load the lessons. Check that DrumDrop is running, then retry.",
    ),
  ).toBeInTheDocument()
  expect(screen.queryByText(/HTTP 502/)).not.toBeInTheDocument()
})

// --- What a row says -------------------------------------------------------------

it("a skipped or failed lesson shows its reason, muted, under its title; other statuses do not", async () => {
  const skippedWithReason: LessonDTO = { ...skippedLesson, error: "not for me" }
  const failed: LessonDTO = {
    ...lessons[1],
    railcontent_id: 800,
    title: "Linear Fills",
    status: "failed",
    error: "yt-dlp exited 1",
  }
  const downloadedWithError: LessonDTO = { ...lessons[0], error: "a stale error" }
  server.use(
    http.get(`${ORIGIN}/api/lessons`, () =>
      HttpResponse.json([skippedWithReason, failed, downloadedWithError]),
    ),
  )
  renderLessons()

  const reason = await screen.findByText("not for me")
  expect(reason).toHaveClass("text-muted-foreground")
  expect(reason.closest("td")).toHaveTextContent(/^Flam Tap/)
  expect(screen.getByText("yt-dlp exited 1").closest("td")).toHaveTextContent(/^Linear Fills/)
  expect(screen.queryByText("a stale error")).not.toBeInTheDocument()
})

it("a clamped row note carries its full text in a title", async () => {
  const long = `yt-dlp exited 1: ${"ERROR: [youtube] unable to extract player response ".repeat(4)}`
  const failed: LessonDTO = {
    ...lessons[1],
    railcontent_id: 800,
    title: "Linear Fills",
    status: "failed",
    error: long,
  }
  server.use(http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json([failed])))
  renderLessons()

  const note = await screen.findByText(long.trim())
  expect(note).toHaveClass("line-clamp-2")
  expect(note).toHaveAttribute("title", long.trim())
})

it("a delete's tombstone reads 'Files deleted' under the title, not a bare 'deleted'", async () => {
  server.use(
    http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json([tombstoned(lessons[0])])),
  )
  renderLessons()

  const note = await screen.findByText("Files deleted")
  expect(note.closest("td")).toHaveTextContent(/^Single Stroke Roll/)
  expect(screen.queryByText("deleted", { exact: true })).not.toBeInTheDocument()
})

it("while a lesson is being deleted, its row says so and offers neither Delete nor Download", async () => {
  const beingDeleted: LessonDTO = { ...lessons[0], deleting: true } // downloaded, has files
  const failedBeingDeleted: LessonDTO = {
    ...lessons[1],
    railcontent_id: 900,
    title: "Swiss Army Triplet",
    status: "failed",
    has_files: true,
    deleting: true,
  }
  server.use(
    http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json([beingDeleted, failedBeingDeleted])),
  )
  const user = userEvent.setup()
  renderLessons()

  const table = await screen.findByRole("table")
  expect(await within(table).findAllByText("deleting…")).toHaveLength(2)

  for (const title of ["Single Stroke Roll", "Swiss Army Triplet"]) {
    await user.click(screen.getByRole("button", { name: `Actions for ${title}` }))
    await screen.findByRole("menuitem", { name: /copy path/i })
    expect(screen.queryByRole("menuitem", { name: /^delete$/i })).not.toBeInTheDocument()
    expect(screen.queryByRole("menuitem", { name: /download/i })).not.toBeInTheDocument()
    expect(screen.queryByRole("menuitem", { name: /skip/i })).not.toBeInTheDocument()
    await user.keyboard("{Escape}")
  }
})

it("filtered by a follow, the badge and the card name the follow, not its id", async () => {
  server.use(
    http.get(`${ORIGIN}/api/follows/3/lessons`, () => HttpResponse.json([lessons[0]])),
    http.get(`${ORIGIN}/api/follows`, () =>
      HttpResponse.json([
        {
          id: 3,
          kind: "node",
          railcontent_id: 12345,
          slug: null,
          title: "Stick Control",
          brand: "drumeo",
          quality: "1080p",
          added_at: "2026-05-01T00:00:00Z",
          last_synced_at: null,
        },
      ]),
    ),
  )
  renderWithProviders(<Lessons />, { route: "/lessons?follow=3" })

  expect(await screen.findByText("Filtered by “Stick Control”")).toBeInTheDocument()
  expect(screen.getByText("Lessons of “Stick Control”")).toBeInTheDocument()
  expect(screen.queryByText(/#3/)).not.toBeInTheDocument()
})

it("a long follow name truncates inside the filter badge, with the full name in its title", async () => {
  const title = "The Complete Guide to Every Rudiment You Will Ever Need, Volume Two"
  server.use(
    http.get(`${ORIGIN}/api/follows/3/lessons`, () => HttpResponse.json([lessons[0]])),
    http.get(`${ORIGIN}/api/follows`, () =>
      HttpResponse.json([
        {
          id: 3,
          kind: "node",
          railcontent_id: 12345,
          slug: null,
          title,
          brand: "drumeo",
          quality: "1080p",
          added_at: "2026-05-01T00:00:00Z",
          last_synced_at: null,
        },
      ]),
    ),
  )
  renderWithProviders(<Lessons />, { route: "/lessons?follow=3" })

  const text = await screen.findByText(`Filtered by “${title}”`)
  // jsdom cannot measure the ellipsis: this pins the structure. The text
  // truncates, and the badge may shrink below its content (its default is
  // shrink-0 and w-fit, which pushed a long name past the row).
  expect(text).toHaveClass("truncate")
  const badge = text.closest('[data-slot="badge"]')
  expect(badge).toHaveClass("min-w-0", "shrink")
  expect(badge).not.toHaveClass("shrink-0")
  expect(badge).toHaveAttribute("title", `Filtered by “${title}”`)
})
