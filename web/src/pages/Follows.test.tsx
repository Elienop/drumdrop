import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { http, HttpResponse } from "msw"
import { ORIGIN, renderWithProviders, server } from "@/test/msw"
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
  const dialog = await screen.findByRole("dialog")
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
  const dialog = await screen.findByRole("dialog")
  await user.click(within(dialog).getByRole("checkbox", { name: /also delete downloaded files/i }))
  await user.click(within(dialog).getByRole("button", { name: /^remove$/i }))

  await waitFor(() => expect(deletedUrl).not.toBeNull())
  expect(new URL(deletedUrl!).searchParams.get("files")).toBe("true")
})
