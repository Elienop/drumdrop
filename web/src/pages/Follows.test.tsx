import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { delay, http, HttpResponse } from "msw"
import { newTestQueryClient, ORIGIN, renderWithProviders, server } from "@/test/msw"
import { qk } from "@/lib/queryKeys"
import { Toaster } from "@/components/ui/sonner"
import type { CreateFollowRequest, FollowDTO } from "@/types"
import { Follows } from "./Follows"

const follows: FollowDTO[] = [
  {
    id: 1,
    kind: "node",
    railcontent_id: 12345,
    slug: null,
    title: "Stick Control",
    brand: "drumeo",
    quality: "1080p",
    added_at: "2026-05-01T00:00:00Z",
    last_synced_at: "2026-05-29T00:00:00Z",
  },
  {
    id: 2,
    kind: "instructor",
    railcontent_id: null,
    slug: "jared-falk",
    title: "Jared Falk",
    brand: "drumeo",
    quality: "720p",
    added_at: "2026-05-02T00:00:00Z",
    last_synced_at: null,
  },
]

it("renders both follow titles from the list", async () => {
  server.use(http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json(follows)))
  renderWithProviders(<Follows />)

  expect(await screen.findByText("Stick Control")).toBeInTheDocument()
  expect(screen.getByText("Jared Falk")).toBeInTheDocument()
})

it("previews a node then adds it, surfacing a 201 'Following' toast and closing the dialog", async () => {
  server.use(
    http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json([])),
    http.get(`${ORIGIN}/api/preview`, () =>
      HttpResponse.json({ root_id: 12345, title: "Stick Control", lesson_count: 9, kind: "node" }),
    ),
    http.post(`${ORIGIN}/api/follows`, () =>
      HttpResponse.json(follows[0], { status: 201 }),
    ),
  )
  const user = userEvent.setup()
  renderWithProviders(
    <>
      <Follows />
      <Toaster />
    </>,
  )

  await user.click(await screen.findByRole("button", { name: /add follow/i }))
  const dialog = await screen.findByRole("dialog")

  await user.type(within(dialog).getByLabelText(/url or id/i), "12345")
  await user.click(within(dialog).getByRole("button", { name: /preview/i }))

  expect(await within(dialog).findByText("Stick Control")).toBeInTheDocument()
  expect(within(dialog).getByText(/9 lessons/i)).toBeInTheDocument()

  await user.click(within(dialog).getByRole("button", { name: /^add$/i }))

  // Anchor so this cannot match the 200 case's "Already following ..." text —
  // otherwise blanking the 201 branch would still pass.
  expect(await screen.findByText(/^following stick control/i)).toBeInTheDocument()
  expect(screen.queryByText(/already following/i)).not.toBeInTheDocument()
  await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())
})

it("sends the chosen quality in the create-follow POST body", async () => {
  let postedBody: CreateFollowRequest | null = null
  server.use(
    http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json([])),
    http.get(`${ORIGIN}/api/preview`, () =>
      HttpResponse.json({ root_id: 12345, title: "Stick Control", lesson_count: 9, kind: "node" }),
    ),
    http.post(`${ORIGIN}/api/follows`, async ({ request }) => {
      postedBody = (await request.json()) as CreateFollowRequest
      return HttpResponse.json(follows[0], { status: 201 })
    }),
  )
  const user = userEvent.setup()
  renderWithProviders(
    <>
      <Follows />
      <Toaster />
    </>,
  )

  await user.click(await screen.findByRole("button", { name: /add follow/i }))
  const dialog = await screen.findByRole("dialog")

  await user.type(within(dialog).getByLabelText(/url or id/i), "12345")

  // Pick 1080 from the quality Select.
  await user.click(within(dialog).getByRole("combobox", { name: /quality/i }))
  await user.click(await screen.findByRole("option", { name: "1080" }))

  await user.click(within(dialog).getByRole("button", { name: /preview/i }))
  await within(dialog).findByText(/9 lessons/i)
  await user.click(within(dialog).getByRole("button", { name: /^add$/i }))

  await waitFor(() => expect(postedBody).not.toBeNull())
  expect(postedBody!.quality).toBe("1080")
})

it("shows the 'Already following' toast when create returns 200", async () => {
  server.use(
    http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json([])),
    http.get(`${ORIGIN}/api/preview`, () =>
      HttpResponse.json({ root_id: 12345, title: "Stick Control", lesson_count: 9, kind: "node" }),
    ),
    http.post(`${ORIGIN}/api/follows`, () =>
      HttpResponse.json(follows[0], { status: 200 }),
    ),
  )
  const user = userEvent.setup()
  renderWithProviders(
    <>
      <Follows />
      <Toaster />
    </>,
  )

  await user.click(await screen.findByRole("button", { name: /add follow/i }))
  const dialog = await screen.findByRole("dialog")

  await user.type(within(dialog).getByLabelText(/url or id/i), "12345")
  await user.click(within(dialog).getByRole("button", { name: /preview/i }))
  await within(dialog).findByText(/9 lessons/i)
  await user.click(within(dialog).getByRole("button", { name: /^add$/i }))

  expect(await screen.findByText(/already following stick control/i)).toBeInTheDocument()
})

it("edits a follow's quality via PATCH /api/follows/{id}, shows a toast, and invalidates the follows list", async () => {
  let patchedBody: { quality?: string } | null = null
  let patchedUrl: string | null = null
  // Count GET /api/follows: the page mounts one list query, so a successful
  // edit that invalidates qk.follows must trigger a SECOND list fetch. A
  // mis-keyed/dropped invalidation leaves listFetches at 1 and fails here.
  let listFetches = 0
  server.use(
    http.get(`${ORIGIN}/api/follows`, () => {
      listFetches++
      return HttpResponse.json(follows)
    }),
    http.patch(`${ORIGIN}/api/follows/:id`, async ({ request }) => {
      patchedUrl = request.url
      patchedBody = (await request.json()) as { quality?: string }
      return HttpResponse.json({ ...follows[0], quality: patchedBody.quality ?? "" })
    }),
  )
  const user = userEvent.setup()
  renderWithProviders(
    <>
      <Follows />
      <Toaster />
    </>,
  )

  // Open the Edit dialog for the first follow (after the initial list load).
  await user.click(await screen.findByRole("button", { name: /edit stick control/i }))
  await waitFor(() => expect(listFetches).toBe(1))
  const dialog = await screen.findByRole("dialog")

  // Pick 720 from the quality Select, then Save.
  await user.click(within(dialog).getByRole("combobox", { name: /quality/i }))
  await user.click(await screen.findByRole("option", { name: "720" }))
  await user.click(within(dialog).getByRole("button", { name: /^save$/i }))

  await waitFor(() => expect(patchedBody).not.toBeNull())
  expect(patchedBody!.quality).toBe("720")
  expect(new URL(patchedUrl!).pathname).toBe("/api/follows/1")
  expect(await screen.findByText(/quality updated/i)).toBeInTheDocument()
  // The invalidate refetched the list (mis-keyed invalidation would stay at 1).
  await waitFor(() => expect(listFetches).toBe(2))
})

it("unfollows without ?files= by default (files left on disk) and invalidates the follows list", async () => {
  let deletedUrl: string | null = null
  // Same refetch proof as the edit test: a successful unfollow invalidates
  // qk.follows, so the mounted list query must refetch (1 -> 2).
  let listFetches = 0
  server.use(
    http.get(`${ORIGIN}/api/follows`, () => {
      listFetches++
      return HttpResponse.json(follows)
    }),
    http.delete(`${ORIGIN}/api/follows/:id`, ({ request }) => {
      deletedUrl = request.url
      return new HttpResponse(null, { status: 204 })
    }),
  )
  const user = userEvent.setup()
  renderWithProviders(
    <>
      <Follows />
      <Toaster />
    </>,
  )

  await user.click(await screen.findByRole("button", { name: /remove stick control/i }))
  await waitFor(() => expect(listFetches).toBe(1))
  const dialog = await screen.findByRole("alertdialog")
  // Confirm without ticking the checkbox.
  await user.click(within(dialog).getByRole("button", { name: /^remove$/i }))

  await waitFor(() => expect(deletedUrl).not.toBeNull())
  expect(new URL(deletedUrl!).pathname).toBe("/api/follows/1")
  // Default off => no files param sent (server treats absence as false).
  expect(new URL(deletedUrl!).searchParams.get("files")).toBeNull()
  expect(await screen.findByText(/follow removed/i)).toBeInTheDocument()
  // The invalidate refetched the list (dropped/mis-keyed invalidation stays at 1).
  await waitFor(() => expect(listFetches).toBe(2))
})

it("unfollows with ?files=true when 'Also delete downloaded files' is checked", async () => {
  let deletedUrl: string | null = null
  server.use(
    http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json(follows)),
    http.delete(`${ORIGIN}/api/follows/:id`, ({ request }) => {
      deletedUrl = request.url
      return new HttpResponse(null, { status: 204 })
    }),
  )
  const user = userEvent.setup()
  renderWithProviders(
    <>
      <Follows />
      <Toaster />
    </>,
  )

  await user.click(await screen.findByRole("button", { name: /remove stick control/i }))
  const dialog = await screen.findByRole("alertdialog")
  await user.click(within(dialog).getByRole("checkbox", { name: /also delete downloaded files/i }))
  await user.click(within(dialog).getByRole("button", { name: /^remove$/i }))

  await waitFor(() => expect(deletedUrl).not.toBeNull())
  expect(new URL(deletedUrl!).searchParams.get("files")).toBe("true")
})


// The server's fixed 500 when some lesson's files could not be removed. By
// then it has removed the follow's jobs and tombstoned every lesson whose
// files did go, so lessons, jobs and summary all changed although the follow
// was kept.
const FOLLOW_KEPT =
  "could not delete every lesson's files; the follow was kept (see the server log)"

it("the remove dialog says removing stops the follow's queued and running downloads", async () => {
  server.use(http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json(follows)))
  const user = userEvent.setup()
  renderWithProviders(<Follows />)

  await user.click(await screen.findByRole("button", { name: /remove stick control/i }))
  const dialog = await screen.findByRole("alertdialog", { name: "Remove “Stick Control”?" })
  expect(dialog).toHaveAccessibleDescription(/stops its queued and running downloads/i)
})

it("a failed unfollow (500) keeps the dialog open with the server's message and the files choice, and refreshes follows, lessons, jobs and summary", async () => {
  let listFetches = 0
  const deletedUrls: string[] = []
  server.use(
    http.get(`${ORIGIN}/api/follows`, async () => {
      listFetches++
      if (listFetches > 1) await delay(20) // lands after the DELETE, as in a browser
      return HttpResponse.json(follows)
    }),
    http.delete(`${ORIGIN}/api/follows/:id`, ({ request }) => {
      deletedUrls.push(request.url)
      return HttpResponse.json({ error: FOLLOW_KEPT }, { status: 500 })
    }),
  )
  // Seed the keys other pages mount (Lessons, Queue, Dashboard, the top bar):
  // not active here, so the oracle is that each is marked stale.
  const qc = newTestQueryClient()
  qc.setQueryData(qk.lessons(), [])
  qc.setQueryData(qk.jobs({ limit: 10 }), [])
  qc.setQueryData(qk.summary, { follows: 2, lessons: {}, jobs: {}, paused: false })
  const user = userEvent.setup()
  renderWithProviders(
    <>
      <Follows />
      <Toaster />
    </>,
    { client: qc },
  )

  await user.click(await screen.findByRole("button", { name: /remove stick control/i }))
  await waitFor(() => expect(listFetches).toBe(1))
  const dialog = await screen.findByRole("alertdialog")
  const files = within(dialog).getByRole("checkbox", { name: /also delete downloaded files/i })
  await user.click(files)
  await user.click(within(dialog).getByRole("button", { name: /^remove$/i }))

  await waitFor(() => expect(within(dialog).getByRole("alert")).toHaveTextContent(FOLLOW_KEPT))
  // The message arrives together with the refreshed list, not before it.
  expect(qc.getQueryState(qk.follows)?.fetchStatus).toBe("idle")
  expect(screen.getByRole("alertdialog")).toBe(dialog)
  expect(
    within(dialog).getByRole("button", { name: /^remove$/i }),
  ).toHaveAccessibleDescription(FOLLOW_KEPT)
  expect(screen.queryByText(/follow removed/i)).not.toBeInTheDocument()

  expect(listFetches).toBe(2)
  expect(qc.getQueryState(qk.lessons())?.isInvalidated).toBe(true)
  expect(qc.getQueryState(qk.jobs({ limit: 10 }))?.isInvalidated).toBe(true)
  expect(qc.getQueryState(qk.summary)?.isInvalidated).toBe(true)

  // The files choice survives the failure, so a retry asks for the same thing.
  expect(files).toBeChecked()
  expect(files).toBeEnabled()
  await user.click(within(dialog).getByRole("button", { name: /^remove$/i }))
  await waitFor(() => expect(deletedUrls).toHaveLength(2))
  expect(new URL(deletedUrls[1]).searchParams.get("files")).toBe("true")
})

it("while the unfollow runs, the files checkbox, Cancel and Escape are locked and the button reads Removing…; closing after a failure clears it", async () => {
  let answer: (r: Response) => void = () => {}
  server.use(
    http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json(follows)),
    http.delete(
      `${ORIGIN}/api/follows/:id`,
      () => new Promise<Response>((resolve) => (answer = resolve)),
    ),
  )
  const user = userEvent.setup()
  renderWithProviders(<Follows />)

  const opener = await screen.findByRole("button", { name: /remove stick control/i })
  await user.click(opener)
  let dialog = await screen.findByRole("alertdialog")
  await user.click(within(dialog).getByRole("button", { name: /^remove$/i }))

  const pending = await within(dialog).findByRole("button", { name: "Removing…" })
  expect(pending).toHaveAttribute("aria-disabled", "true")
  expect(pending).toHaveFocus()
  expect(within(dialog).getByRole("checkbox", { name: /also delete/i })).toBeDisabled()
  expect(within(dialog).getByRole("button", { name: /^cancel$/i })).toBeDisabled()
  await user.keyboard("{Escape}")
  expect(screen.getByRole("alertdialog")).toBe(dialog)

  answer(HttpResponse.json({ error: FOLLOW_KEPT }, { status: 500 }))
  await waitFor(() => expect(within(dialog).getByRole("alert")).toHaveTextContent(FOLLOW_KEPT))

  await user.click(within(dialog).getByRole("button", { name: "Close" }))
  await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument())
  // Focus is back on the Remove button that opened it.
  await waitFor(() => expect(opener).toHaveFocus())
  await user.click(opener)
  dialog = await screen.findByRole("alertdialog")
  expect(within(dialog).getByRole("alert")).toBeEmptyDOMElement()
})

it("after a follow is removed, focus goes to the next follow's Remove button", async () => {
  let listed = [...follows]
  server.use(
    http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json(listed)),
    http.delete(`${ORIGIN}/api/follows/:id`, ({ params }) => {
      listed = listed.filter((f) => String(f.id) !== params.id)
      return new HttpResponse(null, { status: 204 })
    }),
  )
  const user = userEvent.setup()
  renderWithProviders(
    <>
      <Follows />
      <Toaster />
    </>,
  )

  await user.click(await screen.findByRole("button", { name: /remove stick control/i }))
  const dialog = await screen.findByRole("alertdialog")
  await user.click(within(dialog).getByRole("button", { name: /^remove$/i }))
  await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument())
  expect(within(screen.getByRole("table")).queryByText("Stick Control")).not.toBeInTheDocument()
  await waitFor(() =>
    expect(screen.getByRole("button", { name: /remove jared falk/i })).toHaveFocus(),
  )
  expect(await screen.findByText(/follow removed/i)).toBeInTheDocument()
})

it("a failed add shows the server's message inside the dialog, not as a toast, and closing returns focus to Add follow", async () => {
  const BAD = "could not resolve that URL or id; check the URL or slug"
  server.use(
    http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json([])),
    http.get(`${ORIGIN}/api/preview`, () =>
      HttpResponse.json({ root_id: 12345, title: "Stick Control", lesson_count: 9, kind: "node" }),
    ),
    http.post(`${ORIGIN}/api/follows`, () => HttpResponse.json({ error: BAD }, { status: 400 })),
  )
  const user = userEvent.setup()
  renderWithProviders(
    <>
      <Follows />
      <Toaster />
    </>,
  )

  const opener = await screen.findByRole("button", { name: /add follow/i })
  await user.click(opener)
  const dialog = await screen.findByRole("dialog")
  await user.type(within(dialog).getByLabelText(/url or id/i), "12345")
  await user.click(within(dialog).getByRole("button", { name: /preview/i }))
  await within(dialog).findByText(/9 lessons/i)
  await user.click(within(dialog).getByRole("button", { name: /^add$/i }))

  await waitFor(() => expect(within(dialog).getByRole("alert")).toHaveTextContent(BAD))
  expect(screen.getAllByText(BAD)).toHaveLength(1) // no toast carries it
  expect(within(dialog).getByRole("button", { name: /^add$/i })).toHaveAccessibleDescription(BAD)

  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())
  await waitFor(() => expect(opener).toHaveFocus())
})

it("a failed preview shows the server's message inside the dialog, and a successful one clears it", async () => {
  const NOPE = "no lessons found for that id"
  let previews = 0
  server.use(
    http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json([])),
    http.get(`${ORIGIN}/api/preview`, () => {
      previews++
      if (previews === 1) return HttpResponse.json({ error: NOPE }, { status: 400 })
      return HttpResponse.json({ root_id: 12345, title: "Stick Control", lesson_count: 9, kind: "node" })
    }),
  )
  const user = userEvent.setup()
  renderWithProviders(
    <>
      <Follows />
      <Toaster />
    </>,
  )

  await user.click(await screen.findByRole("button", { name: /add follow/i }))
  const dialog = await screen.findByRole("dialog")
  await user.type(within(dialog).getByLabelText(/url or id/i), "12345")
  await user.click(within(dialog).getByRole("button", { name: /preview/i }))
  await waitFor(() => expect(within(dialog).getByRole("alert")).toHaveTextContent(NOPE))

  await user.click(within(dialog).getByRole("button", { name: /preview/i }))
  await within(dialog).findByText(/9 lessons/i)
  expect(within(dialog).getByRole("alert")).toBeEmptyDOMElement()
})

it("a failed quality edit shows the server's message inside the dialog, not as a toast", async () => {
  server.use(
    http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json(follows)),
    http.patch(`${ORIGIN}/api/follows/:id`, () =>
      HttpResponse.json({ error: "invalid quality" }, { status: 400 }),
    ),
  )
  const user = userEvent.setup()
  renderWithProviders(
    <>
      <Follows />
      <Toaster />
    </>,
  )

  const opener = await screen.findByRole("button", { name: /edit stick control/i })
  await user.click(opener)
  const dialog = await screen.findByRole("dialog", { name: "Edit “Stick Control”" })
  await user.click(within(dialog).getByRole("button", { name: /^save$/i }))
  await waitFor(() =>
    expect(within(dialog).getByRole("alert")).toHaveTextContent("invalid quality"),
  )
  expect(screen.getAllByText("invalid quality")).toHaveLength(1)
  expect(screen.getByRole("dialog")).toBe(dialog)

  await user.click(within(dialog).getByRole("button", { name: /^cancel$/i }))
  await waitFor(() => expect(opener).toHaveFocus())
})
