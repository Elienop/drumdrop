import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { http, HttpResponse } from "msw"
import { ORIGIN, renderWithProviders, server } from "@/test/msw"
import { Toaster } from "@/components/ui/sonner"
import type { FollowDTO } from "@/types"
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
