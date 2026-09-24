import { afterEach, describe, expect, it, vi } from "vitest"
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { delay, http, HttpResponse } from "msw"
import { Route, Routes, useLocation } from "react-router-dom"
import { newTestQueryClient, ORIGIN, renderWithProviders, server } from "@/test/msw"
import { finishClosing, holdClosingOverlays } from "@/test/closing"
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

it("the Brand column names a brand as the Add follow preview does", async () => {
  server.use(
    http.get(`${ORIGIN}/api/follows`, () =>
      HttpResponse.json([
        { ...follows[0], brand: "pianote" },
        { ...follows[1], brand: "playbass" },
      ]),
    ),
  )
  renderWithProviders(<Follows />)

  const row = async (title: string) =>
    within((await screen.findByText(title)).closest("tr") as HTMLElement)
  expect((await row("Stick Control")).getByRole("cell", { name: "Pianote" })).toBeInTheDocument()
  // No known name: shown as sent.
  expect((await row("Jared Falk")).getByRole("cell", { name: "playbass" })).toBeInTheDocument()
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

  // The outcome is the toast's title and the follow's name its description,
  // like every other toast. Checked against the 200 case's "Already
  // following" too, so blanking the 201 branch cannot pass.
  expect(await screen.findByText("Follow added")).toBeInTheDocument()
  expect(screen.getByText("Stick Control", { selector: "[data-description]" })).toBeInTheDocument()
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

  expect(await screen.findByText("Already following")).toBeInTheDocument()
  expect(screen.getByText("Stick Control", { selector: "[data-description]" })).toBeInTheDocument()
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
  expect(within(dialog).getByRole("button", { name: /^remove$/i })).toBeInTheDocument()
  await user.click(within(dialog).getByRole("checkbox", { name: /also delete downloaded files/i }))
  // The confirm now says what it will do (and "Remove" alone is gone).
  expect(within(dialog).queryByRole("button", { name: /^remove$/i })).not.toBeInTheDocument()
  await user.click(within(dialog).getByRole("button", { name: "Remove and delete files" }))

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

it("the remove dialog's files checkbox is left-aligned at every width, like its failure message", async () => {
  server.use(http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json(follows)))
  const user = userEvent.setup()
  renderWithProviders(<Follows />)

  await user.click(await screen.findByRole("button", { name: /remove stick control/i }))
  const dialog = await screen.findByRole("alertdialog")
  const row = within(dialog).getByRole("checkbox", { name: /also delete downloaded files/i })
    .parentElement!
  // No centring below sm (justify-center), so nothing to undo from sm up.
  expect(row.className).not.toMatch(/justify-/)
  expect(row).toHaveClass("flex", "items-center")
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
  await user.click(within(dialog).getByRole("button", { name: "Remove and delete files" }))

  await waitFor(() => expect(within(dialog).getByRole("alert")).toHaveTextContent(FOLLOW_KEPT))
  // The message arrives together with the refreshed list, not before it.
  expect(qc.getQueryState(qk.follows)?.fetchStatus).toBe("idle")
  expect(screen.getByRole("alertdialog")).toBe(dialog)
  expect(
    within(dialog).getByRole("button", { name: "Remove and delete files" }),
  ).toHaveAccessibleDescription(FOLLOW_KEPT)
  expect(screen.queryByText(/follow removed/i)).not.toBeInTheDocument()

  expect(listFetches).toBe(2)
  expect(qc.getQueryState(qk.lessons())?.isInvalidated).toBe(true)
  expect(qc.getQueryState(qk.jobs({ limit: 10 }))?.isInvalidated).toBe(true)
  expect(qc.getQueryState(qk.summary)?.isInvalidated).toBe(true)

  // The files choice survives the failure, so a retry asks for the same thing.
  expect(files).toBeChecked()
  expect(files).toBeEnabled()
  await user.click(within(dialog).getByRole("button", { name: "Remove and delete files" }))
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

// --- Keyboard access to a follow's lessons ------------------------------------

function Where() {
  const location = useLocation()
  return <p>At {location.pathname + location.search}</p>
}

it("a follow's title is a link to its lessons, reachable and followed by keyboard", async () => {
  server.use(http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json(follows)))
  const user = userEvent.setup()
  renderWithProviders(
    <Routes>
      <Route path="/" element={<Follows />} />
      <Route path="/lessons" element={<Where />} />
    </Routes>,
  )

  const link = await screen.findByRole("link", { name: "Stick Control" })
  expect(link).toHaveAttribute("href", "/lessons?follow=1")
  await user.tab() // Add follow
  await user.tab()
  expect(link).toHaveFocus()
  await user.keyboard("{Enter}")
  expect(await screen.findByText("At /lessons?follow=1")).toBeInTheDocument()
})

// UI review round 5c, Low 1: with 12px between Edit and Remove, a slightly
// missed press lands on the strip between them, or on the cell's padding.
// jsdom has no layout, so "clicking the gap" is a click whose target is the
// element the gap belongs to: the buttons' flex row, and the cell around it.
// A click on another cell is the control: it does navigate.
describe("a click in a follow's actions never opens its lessons", () => {
  function renderRoutes() {
    server.use(http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json(follows)))
    renderWithProviders(
      <Routes>
        <Route path="/" element={<Follows />} />
        <Route path="/lessons" element={<Where />} />
      </Routes>,
    )
  }
  const edit = () => screen.findByRole("button", { name: "Edit Stick Control" })

  it.each([
    ["the gap between Edit and Remove", async () => (await edit()).parentElement!],
    ["the actions cell's padding", async () => (await edit()).closest("td")!],
  ])("%s", async (_, target) => {
    renderRoutes()
    const el = await target()
    expect(el).toContainElement(screen.getByRole("button", { name: "Remove Stick Control" }))
    fireEvent.click(el)
    expect(screen.getByRole("link", { name: "Stick Control" })).toBeInTheDocument()
    expect(screen.queryByText(/^At \/lessons/)).not.toBeInTheDocument()

    // The control: the same row's Kind cell does navigate.
    const row = screen.getByRole("link", { name: "Stick Control" }).closest("tr")!
    fireEvent.click(within(row).getByText("node"))
    expect(await screen.findByText("At /lessons?follow=1")).toBeInTheDocument()
  })

  // UI review round 5d, Low 2: where a click does nothing, the cursor must not
  // promise one. The row's hand cursor is inherited, so the actions cell
  // resets it; the other cells keep the hand, and the buttons set no cursor of
  // their own (they keep the browser's). jsdom loads no CSS, so this pins the
  // classes; the Orca pass checks the rendered cursor.
  it("the actions cell drops the row's hand cursor; the other cells keep it", async () => {
    renderRoutes()
    const cell = (await edit()).closest("td")!
    const row = cell.closest("tr")!
    expect(row).toHaveClass("cursor-pointer")
    expect(cell).toHaveClass("cursor-default")
    for (const other of within(row).getAllByRole("cell")) {
      if (other !== cell) expect(other.className).not.toMatch(/\bcursor-/)
    }
    for (const name of ["Edit Stick Control", "Remove Stick Control"]) {
      expect(screen.getByRole("button", { name }).className).not.toMatch(/\bcursor-/)
    }
  })

  it.each([
    ["Edit", "Edit Stick Control", "dialog"],
    ["Remove", "Remove Stick Control", "alertdialog"],
  ] as const)("%s opens its dialog and stays on Follows", async (_, name, role) => {
    renderRoutes()
    const user = userEvent.setup()
    await user.click(await screen.findByRole("button", { name }))
    expect(await screen.findByRole(role)).toBeInTheDocument()
    expect(screen.queryByText(/^At \/lessons/)).not.toBeInTheDocument()
  })
})

// --- Add follow: the preview belongs to the exact input it was built from ------

function renderAdd() {
  const user = userEvent.setup()
  renderWithProviders(
    <>
      <Follows />
      <Toaster />
    </>,
  )
  return user
}

// Owner's ruling 2026-09-24, (s): the dialog says that adding starts the
// lessons downloading, and it says it truly. Adding kicks a sync, but a
// running one finishes first, and while syncing is paused the daemon drops
// the kick: nothing starts until Resume.
it.each([
  [
    false,
    "Preview a node or instructor, then add it to your follows. Its lessons start downloading right away, or after the sync that's running.",
  ],
  [
    true,
    "Preview a node or instructor, then add it to your follows. Syncing is paused: its lessons start downloading when you Resume.",
  ],
])("the Add follow dialog says when the lessons start downloading (paused: %s)", async (paused, text) => {
  server.use(
    http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json([])),
    http.get(`${ORIGIN}/api/summary`, () =>
      HttpResponse.json({ follows: 0, lessons: {}, jobs: {}, paused }),
    ),
  )
  const user = renderAdd()
  await user.click(await screen.findByRole("button", { name: /add follow/i }))
  const dialog = await screen.findByRole("dialog")
  await waitFor(() => expect(dialog).toHaveAccessibleDescription(text))
})

const previewOf = ({ request }: { request: Request }) => {
  const id = new URL(request.url).searchParams.get("id")
  return HttpResponse.json({ root_id: Number(id), title: `Node ${id}`, lesson_count: 9, kind: "node" })
}

it("editing the input after a preview hides it and disables Add; Add sends exactly the previewed input", async () => {
  let created: CreateFollowRequest | null = null
  server.use(
    http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json([])),
    http.get(`${ORIGIN}/api/preview`, previewOf),
    http.post(`${ORIGIN}/api/follows`, async ({ request }) => {
      created = (await request.json()) as CreateFollowRequest
      return HttpResponse.json(follows[0], { status: 201 })
    }),
  )
  const user = renderAdd()
  await user.click(await screen.findByRole("button", { name: /add follow/i }))
  const dialog = await screen.findByRole("dialog")
  const input = within(dialog).getByLabelText(/url or id/i)
  const add = within(dialog).getByRole("button", { name: /^add$/i })

  await user.type(input, "12345")
  await user.click(within(dialog).getByRole("button", { name: /preview/i }))
  expect(await within(dialog).findByText("Node 12345")).toBeInTheDocument()
  expect(add).toBeEnabled()

  await user.type(input, "6") // now 123456: not what was previewed
  expect(within(dialog).queryByText("Node 12345")).not.toBeInTheDocument()
  expect(add).toBeDisabled()

  await user.type(input, "{Backspace}") // back to exactly 12345
  expect(within(dialog).getByText("Node 12345")).toBeInTheDocument()
  expect(add).toBeEnabled()
  await user.click(add)
  await waitFor(() => expect(created).not.toBeNull())
  expect(created).toMatchObject({ kind: "node", id: "12345" })
})

it("a preview that lands after the kind was switched is not shown and does not enable Add", async () => {
  let answer: () => void = () => {}
  server.use(
    http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json([])),
    http.get(`${ORIGIN}/api/preview`, async (info) => {
      await new Promise<void>((resolve) => (answer = resolve))
      return previewOf(info)
    }),
  )
  const user = renderAdd()
  await user.click(await screen.findByRole("button", { name: /add follow/i }))
  const dialog = await screen.findByRole("dialog")
  await user.type(within(dialog).getByLabelText(/url or id/i), "12345")
  await user.click(within(dialog).getByRole("button", { name: /preview/i }))
  await within(dialog).findByRole("button", { name: /previewing/i })

  await user.click(within(dialog).getByRole("tab", { name: "Instructor" }))
  await user.type(within(dialog).getByLabelText(/slug/i), "jared-falk")
  await act(async () => answer())
  await waitFor(() =>
    expect(within(dialog).getByRole("button", { name: /^preview$/i })).not.toHaveAttribute(
      "aria-disabled",
    ),
  )
  expect(within(dialog).queryByText("Node 12345")).not.toBeInTheDocument()
  expect(within(dialog).getByRole("button", { name: /^add$/i })).toBeDisabled()
})

// --- Remove: already removed elsewhere -----------------------------------------

it("a 404 on remove (removed elsewhere first) closes the dialog as done", async () => {
  server.use(
    http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json(follows)),
    http.delete(`${ORIGIN}/api/follows/:id`, () =>
      HttpResponse.json({ error: "follow not found" }, { status: 404 }),
    ),
  )
  const user = renderAdd()
  await user.click(await screen.findByRole("button", { name: /remove stick control/i }))
  const dialog = await screen.findByRole("alertdialog")
  await user.click(within(dialog).getByRole("button", { name: /^remove$/i }))

  await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument())
  expect(await screen.findByText("Already removed")).toBeInTheDocument()
  expect(screen.queryByText("follow not found")).not.toBeInTheDocument()
})

// --- Add and Edit dialogs ----------------------------------------------------------

it("Edit's Cancel reads Close while a save runs (closing does not cancel it), and Add has a Cancel too", async () => {
  server.use(
    http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json(follows)),
    http.patch(`${ORIGIN}/api/follows/:id`, () => new Promise<Response>(() => {})),
  )
  const user = renderAdd()

  await user.click(await screen.findByRole("button", { name: /edit stick control/i }))
  let dialog = await screen.findByRole("dialog")
  // The footer's button (the dialog's ✕ is also named "Close").
  const footer = () => within(dialog.querySelector<HTMLElement>('[data-slot="dialog-footer"]')!)
  expect(footer().getByRole("button", { name: "Cancel" })).toBeInTheDocument()
  await user.click(within(dialog).getByRole("button", { name: /^save$/i }))
  expect(await footer().findByRole("button", { name: "Close" })).toBeInTheDocument()
  expect(footer().queryByRole("button", { name: "Cancel" })).not.toBeInTheDocument()
  await user.click(footer().getByRole("button", { name: "Close" }))
  await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())

  await user.click(screen.getByRole("button", { name: /add follow/i }))
  dialog = await screen.findByRole("dialog")
  await user.click(within(dialog).getByRole("button", { name: "Cancel" }))
  await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())
})

it("Add and Edit are flex columns, so their empty message region costs no space", async () => {
  server.use(http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json(follows)))
  const user = renderAdd()

  await user.click(await screen.findByRole("button", { name: /add follow/i }))
  let dialog = await screen.findByRole("dialog")
  expect(dialog).toHaveClass("flex", "flex-col")
  expect(dialog).not.toHaveClass("grid")
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())

  await user.click(screen.getByRole("button", { name: /edit stick control/i }))
  dialog = await screen.findByRole("dialog")
  expect(dialog).toHaveClass("flex", "flex-col")
  expect(dialog).not.toHaveClass("grid")
})

it("the title link's focus ring has room around the letters (the heading's treatment)", async () => {
  server.use(http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json(follows)))
  renderWithProviders(<Follows />)
  const link = await screen.findByRole("link", { name: "Stick Control" })
  expect(link).toHaveClass("-mx-1", "px-1", "focus-visible:ring-ring/60")
})

// --- Edit: a follow removed meanwhile, the lock, the held title ---------------

// A follow whose quality is one of the presets, so the Select shows it.
const presetFollow: FollowDTO = { ...follows[0], quality: "1080" }
const qualityOf = (dialog: HTMLElement) => within(dialog).getByRole("combobox", { name: /quality/i })

it("a 404 on save (the follow was removed meanwhile) closes Edit as done, naming the follow, and drops the row", async () => {
  let gone = false
  server.use(
    http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json(gone ? [follows[1]] : follows)),
    http.patch(`${ORIGIN}/api/follows/:id`, () => {
      gone = true
      return HttpResponse.json(
        { error: "This follow isn't in DrumDrop anymore: it was removed elsewhere." },
        { status: 404 },
      )
    }),
  )
  const user = renderAdd()
  await user.click(await screen.findByRole("button", { name: /edit stick control/i }))
  const dialog = await screen.findByRole("dialog")
  await user.click(within(dialog).getByRole("button", { name: /^save$/i }))

  await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())
  expect(await screen.findByText("Removed elsewhere")).toBeInTheDocument()
  expect(screen.getByText("Stick Control", { selector: "[data-description]" })).toBeInTheDocument()
  expect(screen.queryByText(/quality updated/i)).not.toBeInTheDocument()
  expect(screen.queryByText(/isn't in DrumDrop anymore/)).not.toBeInTheDocument()
  // The refresh dropped the row: no Edit is left that could only fail again.
  await waitFor(() =>
    expect(screen.queryByRole("button", { name: /edit stick control/i })).not.toBeInTheDocument(),
  )
})

it("while Edit saves, its Select is locked and focus stays on Save", async () => {
  let answer: () => void = () => {}
  server.use(
    http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json([presetFollow])),
    http.patch(`${ORIGIN}/api/follows/:id`, async () => {
      await new Promise<void>((resolve) => (answer = resolve))
      return HttpResponse.json({ ...presetFollow, quality: "720" })
    }),
  )
  const user = renderAdd()
  await user.click(await screen.findByRole("button", { name: /edit stick control/i }))
  const dialog = await screen.findByRole("dialog")
  await user.click(qualityOf(dialog))
  await user.click(await screen.findByRole("option", { name: "720" }))
  expect(qualityOf(dialog)).toHaveFocus()

  // fireEvent: a press that does not move focus, as in Safari. Focus was on
  // the Select, which is about to be disabled.
  fireEvent.click(within(dialog).getByRole("button", { name: /^save$/i }))
  const saving = await within(dialog).findByRole("button", { name: /saving/i })
  expect(qualityOf(dialog)).toBeDisabled()
  expect(saving).toHaveFocus()
  await act(async () => answer())
})

it("Edit starts from the follow's saved quality each time it opens, even for the same row", async () => {
  server.use(http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json([presetFollow])))
  const user = renderAdd()
  const opener = await screen.findByRole("button", { name: /edit stick control/i })

  await user.click(opener)
  let dialog = await screen.findByRole("dialog")
  expect(qualityOf(dialog)).toHaveTextContent("1080")
  await user.click(qualityOf(dialog))
  await user.click(await screen.findByRole("option", { name: "720" }))
  expect(qualityOf(dialog)).toHaveTextContent("720")
  await user.click(within(dialog).getByRole("button", { name: "Cancel" }))
  await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())

  // Same row, same follow object (nothing was saved): the abandoned 720 must
  // not survive.
  await user.click(opener)
  dialog = await screen.findByRole("dialog")
  expect(qualityOf(dialog)).toHaveTextContent("1080")
})

describe("while Edit closes", () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it("it keeps naming the follow in its title until it is gone", async () => {
    holdClosingOverlays()
    server.use(http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json(follows)))
    const user = renderAdd()
    await user.click(await screen.findByRole("button", { name: /edit stick control/i }))
    const dialog = await screen.findByRole("dialog", { name: "Edit “Stick Control”" })
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }))

    // Positive control: the closing dialog is still mounted (held by Radix).
    await waitFor(() => expect(dialog).toHaveAttribute("data-state", "closed"))
    expect(dialog).toBeInTheDocument()
    expect(dialog).toHaveAccessibleName("Edit “Stick Control”")

    act(() => finishClosing())
    await waitFor(() => expect(dialog).not.toBeInTheDocument())
  })
})

// --- Add follow: labels, keyboard, stale answers -------------------------------

const footerOf = (dialog: HTMLElement) =>
  within(dialog.querySelector<HTMLElement>('[data-slot="dialog-footer"]')!)

it("Add's dismiss button reads Cancel while a preview runs, and Close only while Add runs", async () => {
  let answerPreview: () => void = () => {}
  server.use(
    http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json([])),
    http.get(`${ORIGIN}/api/preview`, async (info) => {
      await new Promise<void>((resolve) => (answerPreview = resolve))
      return previewOf(info)
    }),
    http.post(`${ORIGIN}/api/follows`, () => new Promise<Response>(() => {})),
  )
  const user = renderAdd()
  await user.click(await screen.findByRole("button", { name: /add follow/i }))
  const dialog = await screen.findByRole("dialog")
  await user.type(within(dialog).getByLabelText(/url or id/i), "12345")
  await user.click(within(dialog).getByRole("button", { name: /^preview$/i }))
  await within(dialog).findByRole("button", { name: /previewing/i })
  // A preview's answer is dropped on close, so closing cancels it.
  expect(footerOf(dialog).getByRole("button", { name: "Cancel" })).toBeInTheDocument()
  expect(footerOf(dialog).queryByRole("button", { name: "Close" })).not.toBeInTheDocument()

  await act(async () => answerPreview())
  await within(dialog).findByText("Node 12345")
  await user.click(within(dialog).getByRole("button", { name: /^add$/i }))
  await within(dialog).findByRole("button", { name: /adding/i })
  // Closing does not stop the add: its answer arrives as a notification.
  expect(footerOf(dialog).getByRole("button", { name: "Close" })).toBeInTheDocument()
})

it("a late add failure for a preview with no title names 'the follow', not empty quotes", async () => {
  let fail: () => void = () => {}
  server.use(
    http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json([])),
    http.get(`${ORIGIN}/api/preview`, () =>
      HttpResponse.json({ root_id: 999, title: "", lesson_count: 0, kind: "node" }),
    ),
    http.post(`${ORIGIN}/api/follows`, async () => {
      await new Promise<void>((resolve) => (fail = resolve))
      return HttpResponse.json(
        { error: "The follow couldn't be added, and nothing was changed." },
        { status: 500 },
      )
    }),
  )
  const user = renderAdd()
  await user.click(await screen.findByRole("button", { name: /add follow/i }))
  const dialog = await screen.findByRole("dialog")
  await user.type(within(dialog).getByLabelText(/url or id/i), "999")
  await user.click(within(dialog).getByRole("button", { name: /^preview$/i }))
  await within(dialog).findByText(/0 lessons/i)
  await user.click(within(dialog).getByRole("button", { name: /^add$/i }))
  await user.click(await footerOf(dialog).findByRole("button", { name: "Close" }))
  await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())

  await act(async () => fail())
  expect(await screen.findByText("Couldn't add the follow")).toBeInTheDocument()
  expect(screen.queryByText(/Couldn't add “/)).not.toBeInTheDocument()
})

it("Enter in a field runs the next step: Preview first, then Add once the preview matches", async () => {
  let created: CreateFollowRequest | null = null
  server.use(
    http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json([])),
    http.get(`${ORIGIN}/api/preview`, previewOf),
    http.post(`${ORIGIN}/api/follows`, async ({ request }) => {
      created = (await request.json()) as CreateFollowRequest
      return HttpResponse.json(follows[0], { status: 201 })
    }),
  )
  const user = renderAdd()
  await user.click(await screen.findByRole("button", { name: /add follow/i }))
  const dialog = await screen.findByRole("dialog")
  await user.type(within(dialog).getByLabelText(/url or id/i), "12345{Enter}")
  expect(await within(dialog).findByText("Node 12345")).toBeInTheDocument()
  expect(created).toBeNull()

  await user.keyboard("{Enter}")
  await waitFor(() => expect(created).toMatchObject({ kind: "node", id: "12345" }))
  expect(await screen.findByText("Follow added")).toBeInTheDocument()
})

// The Enter guards (AddFollowDialog's onFieldEnter), each pinned by a test
// that fails without it.

it("Enter after editing a previewed input previews the new input, and adds nothing", async () => {
  const previews: (string | null)[] = []
  let created: CreateFollowRequest | null = null
  server.use(
    http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json([])),
    http.get(`${ORIGIN}/api/preview`, (info) => {
      previews.push(new URL(info.request.url).searchParams.get("id"))
      return previewOf(info)
    }),
    http.post(`${ORIGIN}/api/follows`, async ({ request }) => {
      created = (await request.json()) as CreateFollowRequest
      return HttpResponse.json(follows[0], { status: 201 })
    }),
  )
  const user = renderAdd()
  await user.click(await screen.findByRole("button", { name: /add follow/i }))
  const dialog = await screen.findByRole("dialog")
  const input = within(dialog).getByLabelText(/url or id/i)
  await user.type(input, "12345{Enter}")
  await within(dialog).findByText("Node 12345")

  // The preview on record is of 12345; the field now says 123456.
  await user.type(input, "6{Enter}")
  expect(await within(dialog).findByText("Node 123456")).toBeInTheDocument()
  expect(previews).toEqual(["12345", "123456"])
  expect(created).toBeNull()
})

it("Enter while a preview runs does nothing, even while an earlier preview of the same input is shown", async () => {
  let held = false
  let answer: () => void = () => {}
  let created = false
  server.use(
    http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json([])),
    http.get(`${ORIGIN}/api/preview`, async (info) => {
      if (held) await new Promise<void>((resolve) => (answer = resolve))
      return previewOf(info)
    }),
    http.post(`${ORIGIN}/api/follows`, () => {
      created = true
      return HttpResponse.json(follows[0], { status: 201 })
    }),
  )
  const user = renderAdd()
  await user.click(await screen.findByRole("button", { name: /add follow/i }))
  const dialog = await screen.findByRole("dialog")
  const input = within(dialog).getByLabelText(/url or id/i)
  await user.type(input, "12345{Enter}")
  await within(dialog).findByText("Node 12345")

  // Preview the same input again, and hold its answer.
  held = true
  await user.click(within(dialog).getByRole("button", { name: /^preview$/i }))
  await within(dialog).findByRole("button", { name: /previewing/i })
  await user.click(input)
  await user.keyboard("{Enter}")

  // Nothing started: Add is not running, and the form is not locked.
  expect(within(dialog).queryByRole("button", { name: /adding/i })).not.toBeInTheDocument()
  expect(input).toBeEnabled()
  await act(async () => answer())
  await within(dialog).findByRole("button", { name: /^preview$/i })
  expect(created).toBe(false)
})

it("an Enter that confirms an input method's candidate previews nothing", async () => {
  const previews: (string | null)[] = []
  server.use(
    http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json([])),
    http.get(`${ORIGIN}/api/preview`, (info) => {
      previews.push(new URL(info.request.url).searchParams.get("id"))
      return previewOf(info)
    }),
  )
  const user = renderAdd()
  await user.click(await screen.findByRole("button", { name: /add follow/i }))
  const dialog = await screen.findByRole("dialog")
  const input = within(dialog).getByLabelText(/url or id/i)
  await user.type(input, "12345")

  fireEvent.keyDown(input, { key: "Enter", isComposing: true })
  expect(within(dialog).queryByRole("button", { name: /previewing/i })).not.toBeInTheDocument()

  // Control: the same press outside a composition previews.
  fireEvent.keyDown(input, { key: "Enter" })
  expect(await within(dialog).findByText("Node 12345")).toBeInTheDocument()
  expect(previews).toEqual(["12345"])
})

it("a held Enter does not add: only a fresh press adds, once the preview is shown", async () => {
  let created: CreateFollowRequest | null = null
  server.use(
    http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json([])),
    http.get(`${ORIGIN}/api/preview`, previewOf),
    http.post(`${ORIGIN}/api/follows`, async ({ request }) => {
      created = (await request.json()) as CreateFollowRequest
      return HttpResponse.json(follows[0], { status: 201 })
    }),
  )
  const user = renderAdd()
  await user.click(await screen.findByRole("button", { name: /add follow/i }))
  const dialog = await screen.findByRole("dialog")
  const input = within(dialog).getByLabelText(/url or id/i)
  await user.type(input, "12345")
  fireEvent.keyDown(input, { key: "Enter" })
  await within(dialog).findByText("Node 12345")

  // The key is still down: its auto-repeat must not add what was just shown.
  fireEvent.keyDown(input, { key: "Enter", repeat: true })
  expect(within(dialog).queryByRole("button", { name: /adding/i })).not.toBeInTheDocument()
  expect(created).toBeNull()

  // A fresh press adds.
  fireEvent.keyDown(input, { key: "Enter" })
  await waitFor(() => expect(created).toMatchObject({ kind: "node", id: "12345" }))
})

// --- Add follow: the next step is the filled button, and the preview is heard --

const variantOf = (button: HTMLElement) => button.getAttribute("data-variant")

it("the one filled button is the next step: Preview until a preview is shown, then Add", async () => {
  server.use(
    http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json([])),
    http.get(`${ORIGIN}/api/preview`, previewOf),
  )
  const user = renderAdd()
  await user.click(await screen.findByRole("button", { name: /add follow/i }))
  const dialog = await screen.findByRole("dialog")
  const footer = dialog.querySelector<HTMLElement>('[data-slot="dialog-footer"]')!
  const filled = () => footer.querySelectorAll('button[data-variant="default"]')
  const preview = within(dialog).getByRole("button", { name: /^preview$/i })
  const add = within(dialog).getByRole("button", { name: /^add$/i })

  expect(variantOf(preview)).toBe("default")
  expect(variantOf(add)).toBe("outline")
  expect(add).toBeDisabled()
  expect(filled()).toHaveLength(1)

  const input = within(dialog).getByLabelText(/url or id/i)
  await user.type(input, "12345{Enter}")
  await within(dialog).findByText("Node 12345")
  expect(variantOf(add)).toBe("default")
  expect(variantOf(preview)).toBe("outline")
  expect(filled()).toHaveLength(1)

  // An edit leaves the preview unshown: Preview is the next step again.
  await user.type(input, "6")
  expect(variantOf(preview)).toBe("default")
  expect(variantOf(add)).toBe("outline")
  expect(filled()).toHaveLength(1)
})

it("the preview lands in a status region that is always there, so a screen reader hears it", async () => {
  server.use(
    http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json([])),
    http.get(`${ORIGIN}/api/preview`, () =>
      HttpResponse.json({
        title: "Jared Falk",
        lesson_count: 40,
        kind: "instructor",
        slug: "jared-falk",
        brand: "drumeo",
      }),
    ),
  )
  const user = renderAdd()
  await user.click(await screen.findByRole("button", { name: /add follow/i }))
  const dialog = await screen.findByRole("dialog")
  // Present and empty before any preview: a region inserted together with
  // its text is missed by some screen readers.
  const status = within(dialog).getByRole("status")
  expect(status).toBeEmptyDOMElement()

  await user.click(within(dialog).getByRole("tab", { name: "Instructor" }))
  await user.type(within(dialog).getByRole("textbox", { name: "Name, slug or link" }), "Jared Falk{Enter}")
  // textContent keeps a space between the parts, not "Falk@jared-falk40".
  // Chrome's accessibility tree keeps them apart even without the spaces;
  // they are for other browsers, which may read the joined text.
  await waitFor(() =>
    expect(status).toHaveTextContent(/^Jared Falk @jared-falk 40 lessons on Drumeo$/),
  )
  expect(within(dialog).getByRole("status")).toBe(status)
})

it("Enter previews from either instructor field, where a form with two fields would not submit", async () => {
  const previews: string[] = []
  server.use(
    http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json([])),
    http.get(`${ORIGIN}/api/preview`, ({ request }) => {
      previews.push(new URL(request.url).search)
      return HttpResponse.json({ root_id: 0, title: "Jared Falk", lesson_count: 40, kind: "instructor" })
    }),
  )
  const user = renderAdd()
  await user.click(await screen.findByRole("button", { name: /add follow/i }))
  const dialog = await screen.findByRole("dialog")
  await user.click(within(dialog).getByRole("tab", { name: "Instructor" }))
  await user.type(within(dialog).getByLabelText(/slug/i), "jared-falk")
  await user.type(within(dialog).getByLabelText(/brand/i), "drumeo{Enter}")
  expect(await within(dialog).findByText(/40 lessons/i)).toBeInTheDocument()
  expect(previews).toEqual(["?slug=jared-falk&brand=drumeo"])
})

it("the instructor field's hint gives an example of each thing it takes", async () => {
  server.use(http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json([])))
  const user = renderAdd()
  await user.click(await screen.findByRole("button", { name: /add follow/i }))
  const dialog = await screen.findByRole("dialog")
  await user.click(within(dialog).getByRole("tab", { name: "Instructor" }))

  const field = within(dialog).getByRole("textbox", { name: "Name, slug or link" })
  expect(field).toHaveAccessibleDescription(
    "For example Jared Falk, jared-falk, or a link to their coach page.",
  )
})

it("an instructor preview ends its count with the brand the follow would use", async () => {
  const sent: { slug: string | null; brand: string | null }[] = []
  server.use(
    http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json([])),
    http.get(`${ORIGIN}/api/preview`, ({ request }) => {
      const q = new URL(request.url).searchParams
      sent.push({ slug: q.get("slug"), brand: q.get("brand") })
      // The server settles the brand from the link, as Brand was left empty.
      return HttpResponse.json({
        title: "Jared Falk",
        lesson_count: 12,
        kind: "instructor",
        slug: "jared-falk",
        brand: "pianote",
      })
    }),
  )
  const user = renderAdd()
  await user.click(await screen.findByRole("button", { name: /add follow/i }))
  const dialog = await screen.findByRole("dialog")
  await user.click(within(dialog).getByRole("tab", { name: "Instructor" }))
  const link = "https://www.musora.com/pianote/coaches/jared-falk/314120"
  await user.type(within(dialog).getByRole("textbox", { name: "Name, slug or link" }), `${link}{Enter}`)

  expect(await within(dialog).findByText("12 lessons on Pianote")).toBeInTheDocument()
  expect(sent).toEqual([{ slug: link, brand: null }])
})

it("a preview of one lesson says 'lesson', not 'lessons'", async () => {
  server.use(
    http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json([])),
    http.get(`${ORIGIN}/api/preview`, () =>
      HttpResponse.json({
        title: "Jared Falk",
        lesson_count: 1,
        kind: "instructor",
        slug: "jared-falk",
        brand: "pianote",
      }),
    ),
  )
  const user = renderAdd()
  await user.click(await screen.findByRole("button", { name: /add follow/i }))
  const dialog = await screen.findByRole("dialog")
  await user.click(within(dialog).getByRole("tab", { name: "Instructor" }))
  await user.type(within(dialog).getByRole("textbox", { name: "Name, slug or link" }), "jared-falk{Enter}")

  expect(await within(dialog).findByText("1 lesson on Pianote")).toBeInTheDocument()
})

it("a brand without a known name previews as the server sent it", async () => {
  server.use(
    http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json([])),
    http.get(`${ORIGIN}/api/preview`, () =>
      HttpResponse.json({
        title: "Jared Falk",
        lesson_count: 3,
        kind: "instructor",
        slug: "jared-falk",
        brand: "playbass",
      }),
    ),
  )
  const user = renderAdd()
  await user.click(await screen.findByRole("button", { name: /add follow/i }))
  const dialog = await screen.findByRole("dialog")
  await user.click(within(dialog).getByRole("tab", { name: "Instructor" }))
  await user.type(within(dialog).getByRole("textbox", { name: "Name, slug or link" }), "jared-falk{Enter}")

  expect(await within(dialog).findByText("3 lessons on playbass")).toBeInTheDocument()
})

it("an instructor preview shows the slug the server normalised to, and Add still sends what was typed", async () => {
  const previews: (string | null)[] = []
  let created: CreateFollowRequest | null = null
  server.use(
    http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json([])),
    http.get(`${ORIGIN}/api/preview`, ({ request }) => {
      previews.push(new URL(request.url).searchParams.get("slug"))
      return HttpResponse.json({
        title: "Jared Falk",
        lesson_count: 40,
        kind: "instructor",
        slug: "jared-falk",
      })
    }),
    http.post(`${ORIGIN}/api/follows`, async ({ request }) => {
      created = (await request.json()) as CreateFollowRequest
      return HttpResponse.json(follows[1], { status: 201 })
    }),
  )
  const user = renderAdd()
  await user.click(await screen.findByRole("button", { name: /add follow/i }))
  const dialog = await screen.findByRole("dialog")
  await user.click(within(dialog).getByRole("tab", { name: "Instructor" }))
  await user.type(within(dialog).getByRole("textbox", { name: "Name, slug or link" }), "Jared Falk{Enter}")

  expect(await within(dialog).findByText("@jared-falk")).toBeInTheDocument()
  expect(previews).toEqual(["Jared Falk"])

  await user.keyboard("{Enter}")
  await waitFor(() => expect(created).toMatchObject({ kind: "instructor", slug: "Jared Falk" }))
})

it("a node preview shows no slug and no brand", async () => {
  server.use(
    http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json([])),
    http.get(`${ORIGIN}/api/preview`, previewOf),
  )
  const user = renderAdd()
  await user.click(await screen.findByRole("button", { name: /add follow/i }))
  const dialog = await screen.findByRole("dialog")
  await user.type(within(dialog).getByLabelText(/url or id/i), "12345{Enter}")

  expect(await within(dialog).findByText("Node 12345")).toBeInTheDocument()
  expect(within(dialog).queryByText(/^@/)).not.toBeInTheDocument()
  expect(within(dialog).getByText("9 lessons")).toBeInTheDocument()
})

it("an instructor input nothing can normalise shows the server's sentence inline", async () => {
  // msgBadSlug, verbatim from internal/server/messages.go.
  const BAD =
    "That instructor can't be looked up. Enter a name or slug in unaccented letters, digits, spaces and hyphens, like Jared Falk, or a coach page's link; a lesson or course link goes under Node. Then Preview again."
  server.use(
    http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json([])),
    http.get(`${ORIGIN}/api/preview`, () => HttpResponse.json({ error: BAD }, { status: 400 })),
  )
  const user = renderAdd()
  await user.click(await screen.findByRole("button", { name: /add follow/i }))
  const dialog = await screen.findByRole("dialog")
  await user.click(within(dialog).getByRole("tab", { name: "Instructor" }))
  await user.type(within(dialog).getByRole("textbox", { name: "Name, slug or link" }), "??{Enter}")

  await waitFor(() => expect(within(dialog).getByRole("alert")).toHaveTextContent(BAD))
  expect(within(dialog).getByRole("button", { name: /^add$/i })).toBeDisabled()
})

// A coach page is refused as a node whether or not its link carries the
// coach's id: the server checks for the /<brand>/coaches/ path before it
// looks for a number (musora.IsCoachLink in handlePreview), so the id is
// never taken for a lesson's. The link goes to the server as typed, and its
// sentence shows inline.
it.each([
  ["with its id", "https://www.musora.com/drumeo/coaches/jared-falk/31880"],
  ["without its id", "https://www.musora.com/drumeo/coaches/jared-falk"],
])("a coach link %s typed into the Node tab shows the server's sentence inline", async (_, link) => {
  // msgCoachLinkAsNode, verbatim from internal/server/messages.go.
  const COACH =
    "That's a coach page, not a lesson or course, so it can't be followed as a node. Choose Instructor, paste the link there, then Preview again."
  const sent: (string | null)[] = []
  server.use(
    http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json([])),
    http.get(`${ORIGIN}/api/preview`, ({ request }) => {
      sent.push(new URL(request.url).searchParams.get("id"))
      return HttpResponse.json({ error: COACH }, { status: 400 })
    }),
  )
  const user = renderAdd()
  await user.click(await screen.findByRole("button", { name: /add follow/i }))
  const dialog = await screen.findByRole("dialog")
  await user.type(within(dialog).getByLabelText(/url or id/i), `${link}{Enter}`)

  await waitFor(() => expect(within(dialog).getByRole("alert")).toHaveTextContent(COACH))
  expect(sent).toEqual([link])
  // No preview is shown (its region is always there, empty without one).
  expect(within(dialog).getByRole("status")).toBeEmptyDOMElement()
  expect(within(dialog).getByRole("button", { name: /^add$/i })).toBeDisabled()
})

it("a failed preview's message clears as soon as the input is edited", async () => {
  // msgNoContentID, verbatim from internal/server/messages.go: the server's
  // answer to "abc", which has no number in it.
  const NOPE =
    "No content id was found in “URL or id”: it needs the number of a lesson or course. Enter the id, or a link that contains it, then Preview again."
  server.use(
    http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json([])),
    http.get(`${ORIGIN}/api/preview`, () => HttpResponse.json({ error: NOPE }, { status: 400 })),
  )
  const user = renderAdd()
  await user.click(await screen.findByRole("button", { name: /add follow/i }))
  const dialog = await screen.findByRole("dialog")
  const input = within(dialog).getByLabelText(/url or id/i)
  await user.type(input, "abc")
  await user.click(within(dialog).getByRole("button", { name: /^preview$/i }))
  await waitFor(() => expect(within(dialog).getByRole("alert")).toHaveTextContent(NOPE))

  await user.type(input, "d")
  expect(within(dialog).getByRole("alert")).toBeEmptyDOMElement()
  expect(within(dialog).getByRole("button", { name: /^preview$/i })).not.toHaveAttribute(
    "aria-describedby",
  )
})

it("a preview that lands after the dialog was closed and reopened is dropped, even for the same input", async () => {
  let answer: () => void = () => {}
  let previews = 0
  server.use(
    http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json([])),
    http.get(`${ORIGIN}/api/preview`, async (info) => {
      previews++
      await new Promise<void>((resolve) => (answer = resolve))
      return previewOf(info)
    }),
  )
  const user = renderAdd()
  await user.click(await screen.findByRole("button", { name: /add follow/i }))
  let dialog = await screen.findByRole("dialog")
  await user.type(within(dialog).getByLabelText(/url or id/i), "12345")
  await user.click(within(dialog).getByRole("button", { name: /^preview$/i }))
  await within(dialog).findByRole("button", { name: /previewing/i })
  await user.click(footerOf(dialog).getByRole("button", { name: "Cancel" }))
  await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())

  // Reopened, and typed the same input: the old answer would match it.
  await user.click(screen.getByRole("button", { name: /add follow/i }))
  dialog = await screen.findByRole("dialog")
  await user.type(within(dialog).getByLabelText(/url or id/i), "12345")
  await act(async () => answer())
  await waitFor(() => expect(previews).toBe(1))
  // Let the stale answer's promise chain run out before asserting.
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 20))
  })
  expect(within(dialog).queryByText("Node 12345")).not.toBeInTheDocument()
  expect(within(dialog).getByRole("button", { name: /^add$/i })).toBeDisabled()
})
