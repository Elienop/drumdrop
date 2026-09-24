import { afterEach, beforeAll, describe, expect, it, vi } from "vitest"
import { act, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { delay, http, HttpResponse } from "msw"
import { toast } from "sonner"
import { newTestQueryClient, ORIGIN, renderWithProviders, sendEvent, server } from "@/test/msw"
import { finishClosing, holdClosingOverlays } from "@/test/closing"
import { clearToken, setToken } from "@/lib/auth"
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

it("the Brand column names a brand as the Add follow preview does", async () => {
  server.use(
    http.get(`${ORIGIN}/api/lessons`, () =>
      HttpResponse.json([
        { ...lessons[0], brand: "guitareo" },
        { ...lessons[1], brand: "playbass" },
      ]),
    ),
  )
  renderWithProviders(<Lessons />)

  const row = async (title: string) =>
    within((await screen.findByText(title)).closest("tr") as HTMLElement)
  expect((await row("Single Stroke Roll")).getByRole("cell", { name: "Guitareo" })).toBeInTheDocument()
  // No known name: shown as sent.
  expect((await row("Double Stroke Roll")).getByRole("cell", { name: "playbass" })).toBeInTheDocument()
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

// --- Row actions that race something done elsewhere ---------------------------

// The server's sentences, verbatim from internal/server/messages.go.
const DOWNLOAD_GONE =
  "This lesson is no longer in DrumDrop: its follow was removed meanwhile. There's nothing left to download."
const DOWNLOAD_JOB_GONE =
  "It won't download: a Skip or delete elsewhere took it off the queue right after it was queued."
const UNSKIP_GONE =
  "This lesson is no longer in DrumDrop: its follow was removed meanwhile. There's nothing left to un-skip."
const CANCEL_GONE =
  "This download is no longer in DrumDrop: it was removed meanwhile, elsewhere. There's nothing left to cancel."
const JOB_ENDED = "This download has already ended, so there's nothing to cancel."
const SERVER_ERROR =
  "This may not have finished: something went wrong on the server. Check the server log, fix the problem, then try again."

// A neutral note: not red, and no close button, since it goes away by itself
// (failureToast's toasts have one, and stay until it is pressed).
async function expectNeutralNote(title: string, lesson: string) {
  const t = (await screen.findByText(title)).closest<HTMLElement>("[data-sonner-toast]")!
  expect(within(t).getByText(lesson, { selector: "[data-description]" })).toBeInTheDocument()
  expect(t).not.toHaveAttribute("data-type", "error")
  expect(within(t).queryByRole("button", { name: "Close toast" })).not.toBeInTheDocument()
  expect(screen.queryByText(/^Couldn't/)).not.toBeInTheDocument()
}

describe("a row action raced by something done elsewhere is a neutral note, and the list refreshes", () => {
  // The title says what happened. "Already removed" would misread here: the
  // press wanted the lesson downloaded, or back.
  it.each([
    [
      "Download",
      lessons[1],
      /^download$/i,
      "download",
      DOWNLOAD_GONE,
      "Won't download: skipped or removed elsewhere",
    ],
    ["Un-skip", skippedLesson, /un-?skip/i, "unskip", UNSKIP_GONE, "Removed elsewhere"],
  ])("%s answered 404", async (_, lesson, item, path, sentence, title) => {
    let gone = false
    server.use(
      http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json(gone ? [] : [lesson])),
      http.post(`${ORIGIN}/api/lessons/:id/${path}`, () => {
        gone = true
        return HttpResponse.json({ error: sentence }, { status: 404 })
      }),
    )
    const user = userEvent.setup()
    renderLessons()

    await user.click(await screen.findByRole("button", { name: `Actions for ${lesson.title}` }))
    await user.click(await screen.findByRole("menuitem", { name: item }))

    await expectNeutralNote(title, lesson.title)
    await waitFor(() =>
      expect(
        screen.queryByRole("button", { name: `Actions for ${lesson.title}` }),
      ).not.toBeInTheDocument(),
    )
  })

  // Download's other 404 (msgDownloadJobGone): a Skip or delete elsewhere
  // took the new download off the queue. The lesson stays listed, now
  // skipped, so the note must not say it was removed.
  it("Download answered 404 because a Skip elsewhere took it off the queue: the same note, and the row stays", async () => {
    let skipped = false
    server.use(
      http.get(`${ORIGIN}/api/lessons`, () =>
        HttpResponse.json([skipped ? { ...lessons[1], status: "skipped" } : lessons[1]]),
      ),
      http.post(`${ORIGIN}/api/lessons/:id/download`, () => {
        skipped = true
        return HttpResponse.json({ error: DOWNLOAD_JOB_GONE }, { status: 404 })
      }),
    )
    const user = userEvent.setup()
    renderLessons()

    await user.click(await screen.findByRole("button", { name: `Actions for ${lessons[1].title}` }))
    await user.click(await screen.findByRole("menuitem", { name: /^download$/i }))

    await expectNeutralNote("Won't download: skipped or removed elsewhere", lessons[1].title)
    expect(screen.queryByText("Already removed")).not.toBeInTheDocument()
    expect(
      screen.getByRole("button", { name: `Actions for ${lessons[1].title}` }),
    ).toBeInTheDocument()
  })

  it.each([
    ["404: the download was removed meanwhile", 404, CANCEL_GONE, "Already removed"],
    ["409: the download had already ended", 409, JOB_ENDED, "Already ended"],
  ])("Cancel download answered %s", async (_, status, sentence, title) => {
    let answered = false
    server.use(
      http.get(`${ORIGIN}/api/lessons`, () =>
        HttpResponse.json(answered ? [{ ...lessons[2], status: "downloaded" }] : [lessons[2]]),
      ),
      http.get(`${ORIGIN}/api/jobs`, () => HttpResponse.json(answered ? [] : [runningJob])),
      http.post(`${ORIGIN}/api/jobs/:id/cancel`, () => {
        answered = true
        return HttpResponse.json({ error: sentence }, { status })
      }),
    )
    const user = userEvent.setup()
    renderLessons()

    await user.click(await screen.findByRole("button", { name: "Actions for Paradiddle" }))
    await user.click(await screen.findByRole("menuitem", { name: "Cancel download" }))

    await expectNeutralNote(title, "Paradiddle")
    // The refresh shows the row as it is now.
    await waitFor(() => {
      const row = screen.getByRole("button", { name: "Actions for Paradiddle" }).closest("tr")!
      expect(within(row).getByText("downloaded")).toBeInTheDocument()
    })
  })
})

it("a row action that really failed stays red with the server's sentence until closed, and the rows refresh", async () => {
  let listFetches = 0
  server.use(
    http.get(`${ORIGIN}/api/lessons`, () => {
      listFetches++
      return HttpResponse.json([skippedLesson])
    }),
    http.post(`${ORIGIN}/api/lessons/:id/unskip`, () =>
      HttpResponse.json({ error: SERVER_ERROR }, { status: 500 }),
    ),
  )
  const user = userEvent.setup()
  renderLessons()

  await user.click(await screen.findByRole("button", { name: "Actions for Flam Tap" }))
  const fetchesBefore = listFetches
  await user.click(await screen.findByRole("menuitem", { name: /un-?skip/i }))

  const t = (await screen.findByText("Couldn't un-skip “Flam Tap”")).closest<HTMLElement>(
    "[data-sonner-toast]",
  )!
  expect(t).toHaveAttribute("data-type", "error")
  expect(within(t).getByText(SERVER_ERROR, { selector: "[data-description]" })).toBeInTheDocument()
  expect(within(t).getByRole("button", { name: "Close toast" })).toBeInTheDocument()
  await waitFor(() => expect(listFetches).toBeGreaterThan(fetchesBefore))
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
      // msgSkipGone, verbatim from internal/server/messages.go.
      return HttpResponse.json(
        {
          error:
            "This lesson is no longer in DrumDrop: its follow was removed meanwhile. There's nothing left to skip.",
        },
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
  expect(screen.queryByText(/no longer in DrumDrop/)).not.toBeInTheDocument()
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
  // It says what Skip does now: the download stops and what it wrote goes,
  // while the files of earlier downloads stay (true again since D66; the
  // README's "Skipping a lesson" paragraph says the same).
  expect(dialog).toHaveAccessibleDescription(
    "Any queued or running download of it stops, and what that download had written is discarded; its earlier files stay. Syncs leave a skipped lesson alone until you un-skip it.",
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
      "Couldn't load the lessons. Check that DrumDrop is running, then Retry.",
    ),
  ).toBeInTheDocument()
  // The sentence names the button beside it.
  expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument()
  expect(screen.queryByText(/HTTP 502/)).not.toBeInTheDocument()
})

// --- What a row says -------------------------------------------------------------

it("a skipped or failed lesson shows its reason, muted, under its title; pending and downloading ones do not", async () => {
  const skippedWithReason: LessonDTO = { ...skippedLesson, error: "not for me" }
  const failed: LessonDTO = {
    ...lessons[1],
    railcontent_id: 800,
    title: "Linear Fills",
    status: "failed",
    error: "yt-dlp exited 1",
  }
  const pendingWithError: LessonDTO = { ...lessons[1], error: "a pending error" }
  const downloadingWithError: LessonDTO = { ...lessons[2], error: "a downloading error" }
  server.use(
    http.get(`${ORIGIN}/api/lessons`, () =>
      HttpResponse.json([skippedWithReason, failed, pendingWithError, downloadingWithError]),
    ),
  )
  renderLessons()

  const reason = await screen.findByText("not for me")
  expect(reason).toHaveClass("text-muted-foreground")
  expect(reason.closest("td")).toHaveTextContent(/^Flam Tap/)
  expect(screen.getByText("yt-dlp exited 1").closest("td")).toHaveTextContent(/^Linear Fills/)
  expect(screen.queryByText("a pending error")).not.toBeInTheDocument()
  expect(screen.queryByText("a downloading error")).not.toBeInTheDocument()
})

// --- A re-download that failed and kept the earlier download ---------------------
//
// Owner's ruling 2026-09-24, (h): the lesson stays "downloaded" with the
// server's note, and syncs leave it alone, so the row offers Download to try
// again. A downloaded lesson without a note keeps its menu.

// The server's note for a plain failure, verbatim (internal/scheduler).
const KEPT_NOTE =
  "The re-download failed, so the earlier download was kept. Check the server log, fix the problem, then Download again."

const keptAfterFailure: LessonDTO = {
  ...lessons[0],
  railcontent_id: 1000,
  title: "Moeller Whip",
  error: KEPT_NOTE,
}

describe("a downloaded lesson whose re-download failed", () => {
  it("shows the server's note like any other row note: muted, clamped, all of it on hover", async () => {
    server.use(http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json([keptAfterFailure])))
    renderLessons()

    const note = await screen.findByText(KEPT_NOTE)
    expect(note).toHaveClass("text-muted-foreground", "line-clamp-3", "xl:line-clamp-2")
    expect(note).toHaveAttribute("title", KEPT_NOTE)
    expect(note.closest("td")).toHaveTextContent(/^Moeller Whip/)
    const row = note.closest("tr")!
    expect(within(row).getByText("downloaded")).toBeInTheDocument()
  })

  it("offers Download beside Copy path and Delete; without a note the menu has no Download", async () => {
    const plain: LessonDTO = lessons[0] // downloaded, has files, no note
    server.use(
      http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json([keptAfterFailure, plain])),
    )
    const user = userEvent.setup()
    renderLessons()

    const items = async (title: string) => {
      await user.click(await screen.findByRole("button", { name: `Actions for ${title}` }))
      await screen.findByRole("menuitem", { name: /copy path/i })
      const names = screen.getAllByRole("menuitem").map((m) => m.textContent)
      await user.keyboard("{Escape}")
      return names
    }
    expect(await items(keptAfterFailure.title)).toEqual(["Download", "Copy path", "Delete"])
    expect(await items(plain.title)).toEqual(["Copy path", "Delete"])
  })

  // Download there behaves as on every other row.
  it.each([
    ["202: queued", 202, { id: 5, status: "queued" }, "Queued", "success"],
    ["200: already queued", 200, { id: 5, status: "queued" }, "Already queued", "neutral"],
    ["404: skipped or removed elsewhere", 404, { error: DOWNLOAD_JOB_GONE }, "Won't download: skipped or removed elsewhere", "neutral"],
    ["500: a real failure", 500, { error: SERVER_ERROR }, "Couldn't queue “Moeller Whip”", "error"],
  ] as const)("Download answered %s", async (_, status, body, title, type) => {
    let listFetches = 0
    let posted: string | null = null
    server.use(
      http.get(`${ORIGIN}/api/lessons`, () => {
        listFetches++
        return HttpResponse.json([keptAfterFailure])
      }),
      http.post(`${ORIGIN}/api/lessons/:id/download`, ({ params }) => {
        posted = params.id as string
        return HttpResponse.json(body, { status })
      }),
    )
    const user = userEvent.setup()
    renderLessons()

    await user.click(await screen.findByRole("button", { name: "Actions for Moeller Whip" }))
    const fetchesBefore = listFetches
    await user.click(await screen.findByRole("menuitem", { name: /^download$/i }))

    if (type === "neutral") {
      await expectNeutralNote(title, "Moeller Whip")
    } else {
      const t = (await screen.findByText(title)).closest<HTMLElement>("[data-sonner-toast]")!
      expect(t).toHaveAttribute("data-type", type)
      const description = type === "error" ? SERVER_ERROR : "Moeller Whip"
      expect(within(t).getByText(description, { selector: "[data-description]" })).toBeInTheDocument()
    }
    expect(posted).toBe("1000")
    await waitFor(() => expect(listFetches).toBeGreaterThan(fetchesBefore))
  })
})

// Owner's ruling 2026-09-24, (n): a re-download Musora answers with "no such
// lesson" (locked or removed), for a lesson that has files, leaves it
// "downloaded" with the not-returned note, as (h) does. The row treats it like
// (h)'s note: shown under the title, and the menu offers Download.

// The server's not-returned note, verbatim (internal/scheduler, msgNotReturnedKept).
const NOT_RETURNED_NOTE =
  "Musora didn't return this lesson, so the earlier download was kept. The lesson may be locked for your account, or removed."

it("a downloaded lesson Musora didn't return shows the not-returned note, and its menu offers Download", async () => {
  const notReturned: LessonDTO = {
    ...lessons[0],
    railcontent_id: 1001,
    title: "Swiss Army Triplet",
    error: NOT_RETURNED_NOTE,
  }
  server.use(http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json([notReturned])))
  const user = userEvent.setup()
  renderLessons()

  const note = await screen.findByText(NOT_RETURNED_NOTE)
  expect(note).toHaveClass("text-muted-foreground", "line-clamp-3", "xl:line-clamp-2")
  expect(note.closest("td")).toHaveTextContent(/^Swiss Army Triplet/)
  expect(within(note.closest("tr")!).getByText("downloaded")).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Actions for Swiss Army Triplet" }))
  await screen.findByRole("menuitem", { name: /copy path/i })
  expect(screen.getAllByRole("menuitem").map((m) => m.textContent)).toEqual([
    "Download",
    "Copy path",
    "Delete",
  ])
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
  expect(note).toHaveClass("line-clamp-3", "xl:line-clamp-2")
  expect(note).toHaveAttribute("title", long.trim())
})

// UI review round 5e, Low D, and the owner's ruling 2026-09-24, (t). Titles
// don't wrap, so the title column is as wide as the longest title on the page:
// with short titles only it left a note 244px wide at 1024px, and the clamp cut
// the sentence. From xl (1280px) the note keeps 28rem and two lines, which fits
// every sentence the server writes (the longest, 147 characters, measured at
// about 27rem in Chromium). Below xl it keeps 22rem and three lines, where the
// same sentence fits, and the table then fits a 1024px window. jsdom has no
// layout, so this pins the classes, both halves, and that no unprefixed width
// or clamp class but these is left to compete with them; the widths were
// measured in a browser.
it("a row note keeps its width, however short the titles on the page", async () => {
  // failMusora's lesson sentence, the longest (internal/scheduler/messages.go).
  const NOTE =
    "Couldn't get this lesson from Musora: it didn't answer, or its answer couldn't be read. Check the server log, fix the problem, then Download again."
  const failed: LessonDTO = {
    ...lessons[1],
    railcontent_id: 801,
    title: "Flams",
    status: "failed",
    error: NOTE,
  }
  server.use(http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json([failed])))
  renderLessons()

  const note = await screen.findByText(NOTE)
  // Below xl: 22rem, three lines.
  expect(note).toHaveClass("min-w-88", "line-clamp-3", "max-w-md")
  // From xl: 28rem, two lines.
  expect(note).toHaveClass("xl:min-w-md", "xl:line-clamp-2")
  const classes = note.className.split(/\s+/)
  expect(classes.filter((c) => /^(min-w|max-w|line-clamp)-/.test(c)).sort()).toEqual([
    "line-clamp-3",
    "max-w-md",
    "min-w-88",
  ])
  expect(classes.filter((c) => /^\w+:(min-w|max-w|line-clamp)-/.test(c)).sort()).toEqual([
    "xl:line-clamp-2",
    "xl:min-w-md",
  ])
})

// Owner's ruling 2026-09-24, (t). Below xl (1280px) the Brand and Quality
// columns are hidden, header and cells, to give a row note room on a narrow
// window. Every other column stays. jsdom has no layout, so this pins
// the classes, by column: a header hidden without its cells (or the reverse)
// would put every cell after it under the wrong header. The widths were
// measured in a browser.
// Owner's ruling 2026-09-24, (w). jsdom has no layout, so this pins only
// where the opt-out sits: on the page's own root, which holds the list and
// the paging below it, and so every row the app shell's <main> could anchor
// to. Its effect was measured in headless Chromium (round 5h): a started
// download moving the top visible row to the top of All no longer scrolls
// the view with it.
it("the Lessons page takes itself out of scroll anchoring, table and paging included", async () => {
  server.use(http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json(lessons)))
  renderLessons()

  const heading = await screen.findByRole("heading", { level: 1, name: "Lessons" })
  await screen.findByRole("table")
  const root = heading.parentElement!.parentElement!
  expect(root.className.split(/\s+/)).toContain("[overflow-anchor:none]")
  expect(root).toContainElement(screen.getByRole("table"))
  expect(root).toContainElement(screen.getByRole("button", { name: /next/i }))
})

it("below xl the table hides Brand and Quality, header and cells, and nothing else", async () => {
  server.use(http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json(lessons)))
  renderLessons()
  await screen.findByText(lessons[0].title)

  const table = screen.getByRole("table")
  const headers = within(table).getAllByRole("columnheader")
  expect(headers.map((h) => h.textContent)).toEqual([
    "Title",
    "Status",
    "Brand",
    "Quality",
    "Size",
    "Updated",
    "",
  ])
  const rows = within(table).getAllByRole("row").slice(1)
  expect(rows).toHaveLength(lessons.length)
  headers.forEach((header, i) => {
    const column = [header, ...rows.map((row) => within(row).getAllByRole("cell")[i])]
    for (const el of column) {
      if (header.textContent === "Brand" || header.textContent === "Quality") {
        expect(el).toHaveClass("hidden", "xl:table-cell")
      } else {
        expect(el).not.toHaveClass("hidden")
        expect(el.className).not.toMatch(/(^|\s)\w+:(hidden|table-cell)(\s|$)/)
      }
    }
  })
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

// The row moves to "downloading" as soon as the server has saved it so, not
// when the download ends (UI review round 5d, Medium 1). The worker emits
// download_started right after StartDownload saves the lesson as
// 'downloading' with its job running (internal/scheduler/worker.go), and the
// page refreshes on it.
describe("when a download starts", () => {
  // This hook runs before RTL unmounts the page, so the stream's teardown on
  // the token change is a React update: wrap it.
  afterEach(() => act(() => clearToken({ silent: true })))

  const FAILED_NOTE = "This download attempt failed. Check the server log, then Download again."

  it("the row reads downloading, drops its old note and offers Cancel, with no flicker and no lost progress", async () => {
    const failed: LessonDTO = {
      ...lessons[1],
      railcontent_id: 101,
      title: "Flam Accent",
      status: "failed",
      error: FAILED_NOTE,
    }
    // What the server stores once StartDownload has run: the lesson is
    // 'downloading' and keeps its old error (only a success clears it), and
    // its job is running.
    const downloading: LessonDTO = { ...failed, status: "downloading" }
    const running: JobDTO = { ...job, id: 77, railcontent_id: 101, status: "running", attempts: 1 }
    let started = false
    // Holds the refresh the event triggers, so the test can look at the page
    // while it is in flight.
    let release: () => void = () => {}
    const held = new Promise<void>((r) => (release = r))
    server.use(
      http.get(`${ORIGIN}/api/lessons`, async () => {
        if (!started) return HttpResponse.json([failed])
        await held
        return HttpResponse.json([downloading])
      }),
      http.get(`${ORIGIN}/api/jobs`, () => HttpResponse.json(started ? [running] : [])),
    )
    setToken("test-token") // the event stream opens only with a stored token
    const user = userEvent.setup()
    renderLessons()

    const row = (await screen.findByText(FAILED_NOTE)).closest("tr")!
    expect(within(row).getByText("failed")).toBeInTheDocument()

    started = true
    act(() =>
      sendEvent({
        kind: "download_started",
        job_id: 77,
        railcontent_id: 101,
        title: "Flam Accent",
        attempt: 1,
        max_attempts: 3,
      }),
    )
    act(() => sendEvent({ kind: "download_progress", job_id: 77, pct: 42 }))

    // While the refresh is in flight the row stays on screen with its live
    // progress: no "Loading…" swap.
    const bar = await within(row).findByRole("progressbar")
    expect(bar).toHaveAttribute("aria-valuenow", "42")
    expect(screen.queryByText("Loading…")).not.toBeInTheDocument()
    expect(row).toBeInTheDocument()

    release()
    const current = () =>
      screen.getByRole("button", { name: "Actions for Flam Accent" }).closest("tr")!
    await waitFor(() => expect(within(current()).getByText("downloading")).toBeInTheDocument())
    expect(within(current()).queryByText("failed")).not.toBeInTheDocument()
    expect(screen.queryByText(FAILED_NOTE)).not.toBeInTheDocument()
    // The progress the page had is still there after the refresh.
    expect(within(current()).getByRole("progressbar")).toHaveAttribute("aria-valuenow", "42")

    // The running jobs were refreshed too, so Cancel knows its job.
    await user.click(screen.getByRole("button", { name: "Actions for Flam Accent" }))
    const cancel = await screen.findByRole("menuitem", { name: "Cancel download" })
    expect(cancel).not.toHaveAttribute("aria-disabled")
    expect(screen.getAllByRole("menuitem").map((m) => m.textContent)).toEqual([
      "Copy path",
      "Cancel download",
    ])
  })

  // Owner's ruling 2026-09-24, (v), superseding (u). The menu is built from
  // the live row, so a download that starts or stops while it is open would
  // swap its items under the user: Radix moves the highlight onto whatever is
  // first now, and a resting pointer ends up over another item (UI review
  // round 5f/5g, finding 1; code review I1). So the menu closes, focus goes
  // back to the ⋯ trigger as Escape leaves it, and the Enter meant for the
  // old item only reopens the menu, with the current items.
  //
  // Each test counts every request an item of either menu could send.
  const countActions = () => {
    const sent = { download: 0, cancel: 0 }
    server.use(
      http.post(`${ORIGIN}/api/lessons/:id/download`, () => {
        sent.download++
        return HttpResponse.json({}, { status: 202 })
      }),
      http.post(`${ORIGIN}/api/jobs/:id/cancel`, () => {
        sent.cancel++
        return HttpResponse.json({})
      }),
    )
    return sent
  }

  // Opens a row's menu from the keyboard and moves the highlight to `item`.
  const openAt = async (user: ReturnType<typeof userEvent.setup>, title: string, item: string) => {
    const trigger = await screen.findByRole("button", { name: `Actions for ${title}` })
    trigger.focus()
    await user.keyboard("{Enter}")
    const target = await screen.findByRole("menuitem", { name: item })
    for (let i = 0; i < 5 && !target.hasAttribute("data-highlighted"); i++) {
      await user.keyboard("{ArrowDown}")
    }
    await waitFor(() => expect(target).toHaveFocus())
    expect(target).toHaveAttribute("data-highlighted")
    return trigger
  }

  // hidden: while a menu is open, Radix hides the rest of the page from the
  // accessibility tree, and the row must be found either way.
  const rowOf = (title: string) =>
    screen.getByRole("button", { name: `Actions for ${title}`, hidden: true }).closest("tr")!

  it.each([
    ["pending", { ...lessons[1] }],
    // A failed lesson with a note: the most common Download (finding 1).
    ["failed", { ...lessons[1], status: "failed" as const, error: FAILED_NOTE }],
    // A re-download that failed and kept the earlier files: Download, then
    // Copy path, then Delete.
    [
      "downloaded-with-a-note",
      { ...lessons[0], error: "The re-download failed, so the earlier download was kept." },
    ],
  ])(
    "a %s lesson starts downloading under its open menu, Download highlighted: the menu closes, focus is on ⋯, and Enter sends nothing",
    async (_, before) => {
      const lesson: LessonDTO = { ...before, railcontent_id: 250, title: "Swiss Army Triplet" }
      const downloading: LessonDTO = { ...lesson, status: "downloading", output_dir: "/media/drumeo/250" }
      // The running job is listed from the start, so an unclosed menu's
      // Cancel download would be enabled at once: the worst case.
      const running: JobDTO = { ...job, id: 88, railcontent_id: 250, status: "running", attempts: 1 }
      let started = false
      server.use(
        http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json([started ? downloading : lesson])),
        http.get(`${ORIGIN}/api/jobs`, () => HttpResponse.json([running])),
      )
      const sent = countActions()
      setToken("test-token") // the event stream opens only with a stored token
      const user = userEvent.setup()
      renderLessons()

      const trigger = await openAt(user, "Swiss Army Triplet", "Download")

      started = true
      act(() =>
        sendEvent({
          kind: "download_started",
          job_id: 88,
          railcontent_id: 250,
          title: "Swiss Army Triplet",
          attempt: 1,
          max_attempts: 3,
        }),
      )

      await waitFor(() => expect(rowOf("Swiss Army Triplet")).toHaveTextContent("downloading"))
      expect(screen.queryByRole("menu")).not.toBeInTheDocument()
      await waitFor(() => expect(trigger).toHaveFocus())

      // The Enter meant for Download reopens the menu with the current items,
      // the harmless one highlighted ((q)'s order), and sends nothing.
      await user.keyboard("{Enter}")
      await waitFor(() =>
        expect(screen.getAllByRole("menuitem").map((m) => m.textContent)).toEqual(
          lesson.has_files
            ? ["Copy path", "Cancel download", "Delete"]
            : ["Copy path", "Cancel download"],
        ),
      )
      await waitFor(() => expect(screen.getByRole("menuitem", { name: "Copy path" })).toHaveFocus())
      expect(sent).toEqual({ download: 0, cancel: 0 })
    },
  )

  it("a downloading lesson's attempt fails under its open menu, Cancel download highlighted: the menu closes, focus is on ⋯, and Enter sends nothing", async () => {
    const downloading: LessonDTO = {
      ...lessons[1],
      railcontent_id: 260,
      title: "Swiss Army Triplet",
      status: "downloading",
    }
    const failed: LessonDTO = { ...downloading, status: "failed", error: FAILED_NOTE }
    const running: JobDTO = { ...job, id: 89, railcontent_id: 260, status: "running", attempts: 3 }
    let ended = false
    let jobsServed = false
    server.use(
      http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json([ended ? failed : downloading])),
      http.get(`${ORIGIN}/api/jobs`, () => {
        jobsServed = true
        return HttpResponse.json(ended ? [] : [running])
      }),
    )
    const sent = countActions()
    setToken("test-token")
    const user = userEvent.setup()
    renderLessons()

    // Cancel download is enabled (and so can be highlighted) only once the
    // running jobs are in.
    await waitFor(() => expect(jobsServed).toBe(true))
    const trigger = await openAt(user, "Swiss Army Triplet", "Cancel download")

    ended = true
    act(() =>
      sendEvent({
        kind: "attempt_failed",
        job_id: 89,
        railcontent_id: 260,
        title: "Swiss Army Triplet",
        attempt: 3,
        max_attempts: 3,
        error: "The download failed.",
      }),
    )

    await waitFor(() => expect(rowOf("Swiss Army Triplet")).toHaveTextContent("failed"))
    expect(screen.queryByRole("menu")).not.toBeInTheDocument()
    await waitFor(() => expect(trigger).toHaveFocus())

    await user.keyboard("{Enter}")
    await waitFor(() =>
      expect(screen.getAllByRole("menuitem").map((m) => m.textContent)).toEqual([
        "Download",
        "Skip",
        "Copy path",
      ]),
    )
    expect(sent).toEqual({ download: 0, cancel: 0 })
  })

  it("a menu closed by a start stays closed when that attempt fails at once", async () => {
    const pending: LessonDTO = { ...lessons[1], railcontent_id: 280, title: "Swiss Army Triplet" }
    let now: LessonDTO = pending
    server.use(http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json([now])))
    setToken("test-token")
    const user = userEvent.setup()
    renderLessons()

    const trigger = await openAt(user, "Swiss Army Triplet", "Download")
    const event = { job_id: 91, railcontent_id: 280, title: "Swiss Army Triplet", attempt: 1, max_attempts: 1 }

    now = { ...pending, status: "downloading" }
    act(() => sendEvent({ kind: "download_started", ...event }))
    await waitFor(() => expect(rowOf("Swiss Army Triplet")).toHaveTextContent("downloading"))
    expect(screen.queryByRole("menu")).not.toBeInTheDocument()

    // Back to what it was when the menu opened: the menu must not come back
    // by itself.
    now = { ...pending, status: "failed", error: FAILED_NOTE }
    act(() => sendEvent({ kind: "attempt_failed", ...event, error: "The download failed." }))
    await waitFor(() => expect(rowOf("Swiss Army Triplet")).toHaveTextContent("failed"))
    expect(screen.queryByRole("menu")).not.toBeInTheDocument()
    expect(trigger).toHaveFocus()
  })

  // A closing Radix menu stays mounted for its exit fade, and in Chromium it
  // then showed the new items and still took a click: one at Download's old
  // spot canceled the download it had just started (round 5h). So the close
  // a start or an end makes has no fade: the class below sets `animation:
  // none !important` on the closed menu, and Presence then removes it before
  // a paint. jsdom has no stylesheet, so holdClosingOverlays stands in for
  // the fade and this checks which close gets the class; the browser run
  // checked what the class does.
  it("a menu closed by a start leaves with no exit fade; one the user closes keeps its fade", async () => {
    const NO_FADE = "data-[state=closed]:animate-none!"
    holdClosingOverlays()
    try {
      const pending: LessonDTO = { ...lessons[1], railcontent_id: 290, title: "Swiss Army Triplet" }
      let now: LessonDTO = pending
      server.use(http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json([now])))
      setToken("test-token")
      const user = userEvent.setup()
      renderLessons()
      const closing = () => document.querySelector('[role="menu"][data-state="closed"]')

      await openAt(user, "Swiss Army Triplet", "Download")
      now = { ...pending, status: "downloading" }
      act(() =>
        sendEvent({
          kind: "download_started",
          job_id: 92,
          railcontent_id: 290,
          title: "Swiss Army Triplet",
          attempt: 1,
          max_attempts: 3,
        }),
      )
      await waitFor(() => expect(closing()).not.toBeNull())
      expect(closing()!.className.split(/\s+/)).toContain(NO_FADE)
      act(() => finishClosing())
      expect(closing()).toBeNull()

      // The user's own close after that keeps the fade.
      await openAt(user, "Swiss Army Triplet", "Copy path")
      expect(screen.getByRole("menu").className.split(/\s+/)).not.toContain(NO_FADE)
      await user.keyboard("{Escape}")
      await waitFor(() => expect(closing()).not.toBeNull())
      expect(closing()!.className.split(/\s+/)).not.toContain(NO_FADE)
      act(() => finishClosing())
    } finally {
      vi.unstubAllGlobals()
    }
  })

  it("an open menu stays open, its highlight where it was, when a refetch changes nothing about its lesson", async () => {
    const mine: LessonDTO = { ...lessons[1], railcontent_id: 270, title: "Swiss Army Triplet" }
    const other: LessonDTO = { ...lessons[1], railcontent_id: 271, title: "Flam Accent" }
    let started = false
    let fetches = 0
    server.use(
      http.get(`${ORIGIN}/api/lessons`, () => {
        fetches++
        // Every answer is a fresh object for the same lesson: equal, never
        // the same reference.
        return HttpResponse.json([{ ...mine }, started ? { ...other, status: "downloading" } : other])
      }),
    )
    setToken("test-token")
    const user = userEvent.setup()
    renderLessons()

    await openAt(user, "Swiss Army Triplet", "Download")
    const before = fetches

    // Another lesson starts downloading: the list refetches.
    started = true
    act(() =>
      sendEvent({
        kind: "download_started",
        job_id: 90,
        railcontent_id: 271,
        title: "Flam Accent",
        attempt: 1,
        max_attempts: 3,
      }),
    )
    await waitFor(() => expect(fetches).toBeGreaterThan(before))
    await waitFor(() => expect(rowOf("Flam Accent")).toHaveTextContent("downloading"))

    expect(screen.getByRole("menu")).toBeInTheDocument()
    expect(screen.getAllByRole("menuitem").map((m) => m.textContent)).toEqual([
      "Download",
      "Skip",
      "Copy path",
    ])
    expect(screen.getByRole("menuitem", { name: "Download" })).toHaveFocus()
  })
})

// Owner's ruling 2026-09-24, (q): while a lesson downloads, Copy path comes
// first, Cancel download after it, and Delete stays last.
it.each([
  [true, ["Copy path", "Cancel download", "Delete"]],
  [false, ["Copy path", "Cancel download"]],
])("a downloading lesson's menu (has files: %s) reads %j", async (hasFiles, items) => {
  server.use(
    http.get(`${ORIGIN}/api/lessons`, () =>
      HttpResponse.json([{ ...lessons[2], has_files: hasFiles }]),
    ),
    http.get(`${ORIGIN}/api/jobs`, () => HttpResponse.json([runningJob])),
  )
  const user = userEvent.setup()
  renderLessons()

  await user.click(await screen.findByRole("button", { name: "Actions for Paradiddle" }))
  await screen.findByRole("menuitem", { name: "Copy path" })
  expect(screen.getAllByRole("menuitem").map((m) => m.textContent)).toEqual(items)
})
