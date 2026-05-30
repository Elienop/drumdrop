import { beforeAll, expect, it } from "vitest"
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { http, HttpResponse } from "msw"
import { ORIGIN, renderWithProviders, server } from "@/test/msw"
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
