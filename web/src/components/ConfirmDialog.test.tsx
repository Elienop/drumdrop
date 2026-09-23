import * as React from "react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { toast } from "sonner"
import { Toaster } from "@/components/ui/sonner"
import { ApiHttpError } from "@/lib/api"
import { finishClosing, holdClosingOverlays } from "@/test/closing"
import { ConfirmDialog, type ConfirmDialogProps } from "./ConfirmDialog"

// A request the test settles by hand.
function deferred() {
  let resolve: () => void = () => {}
  let reject: (e: unknown) => void = () => {}
  const promise = new Promise<void>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

type Props = ConfirmDialogProps<unknown>

function Harness(props: Partial<Props> & Pick<Props, "onConfirm">) {
  const [open, setOpen] = React.useState(false)
  const opener = React.useRef<HTMLButtonElement>(null)
  return (
    <>
      <button ref={opener} onClick={() => setOpen(true)}>
        Open
      </button>
      <ConfirmDialog
        open={open}
        onOpenChange={setOpen}
        title="Delete “Thing”?"
        description="It goes away."
        confirmLabel="Delete"
        pendingLabel="Deleting…"
        announce={() => {}}
        failureTitle="Couldn't delete “Thing”"
        returnFocus={() => [opener.current]}
        {...props}
      />
      <Toaster />
    </>
  )
}

afterEach(() => {
  vi.restoreAllMocks()
})

it("confirming moves focus to the confirm button, even when the press did not (Safari does not focus clicked buttons)", async () => {
  const req = deferred()
  const user = userEvent.setup()
  render(<Harness onConfirm={() => req.promise} />)
  await user.click(screen.getByRole("button", { name: "Open" }))
  const dialog = await screen.findByRole("alertdialog")
  expect(within(dialog).getByRole("button", { name: "Cancel" })).toHaveFocus()

  // fireEvent.click, unlike userEvent, does not move focus: as in Safari.
  fireEvent.click(within(dialog).getByRole("button", { name: "Delete" }))
  const confirm = await within(dialog).findByRole("button", { name: "Deleting…" })
  expect(confirm).toHaveFocus()
  // Cancel was focused a moment ago and is now disabled: focus did not stay there.
  expect(within(dialog).getByRole("button", { name: "Cancel" })).toBeDisabled()
  await act(async () => req.resolve())
})

it("after the lock expires the dialog can be closed, and a late failure arrives as a toast naming the item", async () => {
  const req = deferred()
  const user = userEvent.setup()
  render(<Harness onConfirm={() => req.promise} releaseAfterMs={30} />)
  await user.click(screen.getByRole("button", { name: "Open" }))
  const dialog = await screen.findByRole("alertdialog")
  await user.click(within(dialog).getByRole("button", { name: "Delete" }))
  expect(within(dialog).getByRole("button", { name: "Cancel" })).toBeDisabled()
  expect(within(dialog).getByRole("status")).toBeEmptyDOMElement()

  // Released: the dismiss button is back as "Close" and the dialog says why.
  const close = await within(dialog).findByRole("button", { name: "Close" })
  expect(close).toBeEnabled()
  expect(within(dialog).getByRole("status")).toHaveTextContent(/taking longer than usual/i)
  expect(within(dialog).getByRole("button", { name: "Deleting…" })).toBeInTheDocument()

  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument())
  await waitFor(() => expect(screen.getByRole("button", { name: "Open" })).toHaveFocus())

  const toastError = vi.spyOn(toast, "error")
  req.reject(new ApiHttpError(500, "could not delete the files"))
  // Title: the item and the outcome. Description: the server's sentence.
  expect(await screen.findByText("Couldn't delete “Thing”")).toBeInTheDocument()
  expect(
    screen.getByText("could not delete the files", { selector: "[data-description]" }),
  ).toBeInTheDocument()
  // It stays until dismissed (it may land long after the user moved on).
  expect(screen.getByRole("button", { name: "Close toast" })).toBeInTheDocument()
  expect(toastError).toHaveBeenCalledWith(
    "Couldn't delete “Thing”",
    expect.objectContaining({ duration: Infinity, closeButton: true }),
  )
})

it("a success that lands after the dialog was closed is still announced", async () => {
  const req = deferred()
  const announce = vi.fn()
  const user = userEvent.setup()
  render(<Harness onConfirm={() => req.promise} releaseAfterMs={30} announce={announce} />)
  await user.click(screen.getByRole("button", { name: "Open" }))
  const dialog = await screen.findByRole("alertdialog")
  await user.click(within(dialog).getByRole("button", { name: "Delete" }))
  await user.click(await within(dialog).findByRole("button", { name: "Close" }))
  await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument())

  req.resolve()
  await waitFor(() => expect(announce).toHaveBeenCalledTimes(1))
})

it("a late result does not leak into the next dialog", async () => {
  const first = deferred()
  const second = deferred()
  const requests = [first, second]
  const user = userEvent.setup()
  render(<Harness onConfirm={() => requests.shift()!.promise} releaseAfterMs={30} />)

  await user.click(screen.getByRole("button", { name: "Open" }))
  let dialog = await screen.findByRole("alertdialog")
  await user.click(within(dialog).getByRole("button", { name: "Delete" }))
  await user.click(await within(dialog).findByRole("button", { name: "Close" }))
  await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument())

  // Reopened: a fresh, idle dialog; the first request's failure is a toast.
  await user.click(screen.getByRole("button", { name: "Open" }))
  dialog = await screen.findByRole("alertdialog")
  expect(within(dialog).getByRole("button", { name: "Delete" })).not.toHaveAttribute(
    "aria-disabled",
  )
  first.reject(new ApiHttpError(500, "first failed"))
  expect(await screen.findByText("first failed")).toBeInTheDocument()
  expect(within(dialog).getByRole("alert")).toBeEmptyDOMElement()
  await act(async () => second.resolve())
})

it("confirming re-anchors the dialog by its bottom edge where it already is, so the footer cannot move", async () => {
  const req = deferred()
  const user = userEvent.setup()
  render(<Harness onConfirm={() => req.promise} />)
  await user.click(screen.getByRole("button", { name: "Open" }))
  const dialog = await screen.findByRole("alertdialog")
  expect(dialog).not.toHaveAttribute("data-anchored")

  vi.spyOn(dialog, "getBoundingClientRect").mockReturnValue(
    DOMRect.fromRect({ x: 0, y: 300, width: 500, height: 200 }),
  )
  await user.click(within(dialog).getByRole("button", { name: "Delete" }))
  expect(dialog).toHaveAttribute("data-anchored", "bottom")
  // bottom edge at y=500 → window.innerHeight - 500 from the viewport bottom.
  expect(dialog.style.bottom).toBe(`${window.innerHeight - 500}px`)
  // Growth goes up, but never past 16px from the top: it scrolls instead
  // (bottom edge at 500 → at most 500 - 16 tall).
  expect(dialog.style.maxHeight).toBe("484px")

  // A resize makes the frozen position stale: back to centred.
  fireEvent(window, new Event("resize"))
  await waitFor(() => expect(dialog).not.toHaveAttribute("data-anchored"))
  expect(dialog.style.bottom).toBe("")
  expect(dialog.style.maxHeight).toBe("")
  await act(async () => req.resolve())
})

it("re-anchoring is instant: the dialog transitions nothing, so the footer does not glide on confirm", async () => {
  // The primitive's duration-200 with transition-property's initial value
  // (all) animated top, bottom, the translate and max-height for 200ms when
  // the dialog was re-anchored. jsdom computes no Tailwind CSS: this pins the
  // class the browser check measured (the enter/exit fade and zoom are CSS
  // animations, which transition-none does not touch).
  const req = deferred()
  const user = userEvent.setup()
  render(<Harness onConfirm={() => req.promise} />)
  await user.click(screen.getByRole("button", { name: "Open" }))
  const dialog = await screen.findByRole("alertdialog")
  expect(dialog).toHaveClass("transition-none")
  await user.click(within(dialog).getByRole("button", { name: "Delete" }))
  expect(dialog).toHaveAttribute("data-anchored", "bottom")
  expect(dialog).toHaveClass("transition-none")
  expect(dialog.className).not.toMatch(/(^|\s)transition(-all|-\[|\s|$)/)
  await act(async () => req.resolve())
})

it("capped by the viewport, only the body scrolls: the message and the buttons stay outside it, the title inside", async () => {
  // jsdom cannot measure layout: this pins the structure. With the scroll on
  // the whole dialog, growth past the cap spilled the footer out of view.
  const req = deferred()
  const user = userEvent.setup()
  render(<Harness onConfirm={() => req.promise} />)
  await user.click(screen.getByRole("button", { name: "Open" }))
  const dialog = await screen.findByRole("alertdialog")
  expect(dialog).toHaveClass("max-h-[calc(100dvh-2rem)]")
  expect(dialog).not.toHaveClass("overflow-y-auto")
  expect(dialog).not.toHaveClass("overflow-auto")

  const scrollers = [...dialog.querySelectorAll<HTMLElement>(".overflow-y-auto, .overflow-auto")]
  expect(scrollers).toHaveLength(1)
  const body = scrollers[0]
  // It can shrink below its content, so it gives way before anything else.
  expect(body).toHaveClass("min-h-0")
  expect(body).toContainElement(within(dialog).getByRole("heading", { name: "Delete “Thing”?" }))
  expect(body).toContainElement(within(dialog).getByText("It goes away."))

  await user.click(within(dialog).getByRole("button", { name: "Delete" }))
  await act(async () => req.reject(new ApiHttpError(500, "could not delete the files")))
  expect(body).not.toContainElement(within(dialog).getByRole("alert"))
  expect(body).not.toContainElement(within(dialog).getByRole("button", { name: "Close" }))
  expect(body).not.toContainElement(within(dialog).getByRole("button", { name: "Delete" }))
})

it("the dialog body is a flex column, so an empty message region collapses into the gap before it", async () => {
  // jsdom cannot measure layout: this pins the structure the browser check
  // measured (a grid row cannot shrink below zero; a flex item's -mt-4 can
  // cancel the gap-4 before it).
  const user = userEvent.setup()
  render(<Harness onConfirm={() => Promise.resolve()} />)
  await user.click(screen.getByRole("button", { name: "Open" }))
  const dialog = await screen.findByRole("alertdialog")
  expect(dialog).toHaveClass("flex", "flex-col")
  expect(dialog).not.toHaveClass("grid")
  expect(within(dialog).getByRole("alert")).toHaveClass("empty:-mt-4")
  expect(within(dialog).getByRole("status")).toHaveClass("empty:-mt-4")
})

it("the error icon is inline with the message, and the message is left-aligned at every width", async () => {
  const req = deferred()
  const user = userEvent.setup()
  render(<Harness onConfirm={() => req.promise} />)
  await user.click(screen.getByRole("button", { name: "Open" }))
  const dialog = await screen.findByRole("alertdialog")
  await user.click(within(dialog).getByRole("button", { name: "Delete" }))
  await act(async () => req.reject(new ApiHttpError(500, "could not delete the files")))

  const message = within(dialog).getByText("could not delete the files")
  expect(message.tagName).toBe("P")
  // Not a flex row (that strands the icon at the edge of a centred block).
  expect(message).not.toHaveClass("flex")
  const icon = message.firstElementChild
  expect(icon?.tagName.toLowerCase()).toBe("svg")
  expect(icon).toHaveClass("inline-block")
  // Never centred, not even below `sm`: several centred lines of uneven
  // length are hard to read.
  expect(message).toHaveClass("text-left")
  expect(message.className).not.toMatch(/text-center/)
})

it("labels are stacked: the buttons keep their width through pending and failure", async () => {
  const req = deferred()
  const user = userEvent.setup()
  render(<Harness onConfirm={() => req.promise} />)
  await user.click(screen.getByRole("button", { name: "Open" }))
  const dialog = await screen.findByRole("alertdialog")

  // Idle: both labels are laid out; only the active one is seen or named.
  const confirm = within(dialog).getByRole("button", { name: "Delete" })
  const pendingLabel = confirm.querySelector('[data-label="pending"]')
  expect(pendingLabel).toHaveTextContent("Deleting…")
  expect(pendingLabel).toHaveClass("invisible")
  expect(pendingLabel).toHaveAttribute("aria-hidden", "true")
  const cancel = within(dialog).getByRole("button", { name: "Cancel" })
  expect(cancel.querySelector('[data-label="close"]')).toHaveClass("invisible")

  await user.click(confirm)
  expect(confirm).toHaveAccessibleName("Deleting…")
  expect(confirm.querySelector('[data-label="idle"]')).toHaveClass("invisible")

  await act(async () => req.reject(new ApiHttpError(500, "nope")))
  expect(cancel).toHaveAccessibleName("Close")
  expect(cancel.querySelector('[data-label="cancel"]')).toHaveClass("invisible")
})

it("while a retry runs, the last failure is muted and no longer describes the confirm button", async () => {
  const attempts = [deferred(), deferred()]
  let n = 0
  const user = userEvent.setup()
  render(<Harness onConfirm={() => attempts[n++].promise} />)
  await user.click(screen.getByRole("button", { name: "Open" }))
  const dialog = await screen.findByRole("alertdialog")
  const confirm = within(dialog).getByRole("button", { name: "Delete" })

  await user.click(confirm)
  await act(async () => attempts[0].reject(new ApiHttpError(500, "first failure")))
  const first = within(dialog).getByText("first failure")
  expect(first).not.toHaveAttribute("data-stale")
  expect(confirm).toHaveAccessibleDescription("first failure")

  await user.click(confirm) // retry
  expect(confirm).toHaveAccessibleName("Deleting…")
  expect(first).toHaveAttribute("data-stale", "true")
  expect(first).toHaveClass("text-muted-foreground")
  expect(confirm).not.toHaveAttribute("aria-describedby")

  await act(async () => attempts[1].reject(new ApiHttpError(500, "second failure")))
  const second = within(dialog).getByText("second failure")
  expect(second).not.toHaveAttribute("data-stale")
  expect(confirm).toHaveAccessibleDescription("second failure")
})

// --- Closing animation -------------------------------------------------------
//
// Every test above would pass whatever the dialog shows while closing: jsdom
// unmounts it at once. holdClosingOverlays (test/closing.ts) keeps it mounted
// as Radix does in a browser.

// A page as the real ones are: it clears the item as the dialog closes, and
// resets the slot's checkbox then.
function PageLike({ onConfirm }: { onConfirm: () => Promise<unknown> }) {
  const [item, setItem] = React.useState<string | null>(null)
  const [files, setFiles] = React.useState(false)
  const opener = React.useRef<HTMLButtonElement>(null)
  React.useEffect(() => {
    if (!item) setFiles(false)
  }, [item])
  return (
    <>
      <button ref={opener} onClick={() => setItem("Thing")}>
        Open
      </button>
      <ConfirmDialog
        open={item !== null}
        onOpenChange={(open) => {
          if (!open) setItem(null)
        }}
        title={item ? `Delete “${item}”?` : "Delete?"}
        description="It goes away."
        confirmLabel="Delete"
        pendingLabel="Deleting…"
        onConfirm={onConfirm}
        announce={() => {}}
        failureTitle={`Couldn't delete “${item ?? "it"}”`}
        returnFocus={() => [opener.current]}
      >
        {({ pending }) => (
          <label>
            <input
              type="checkbox"
              checked={files}
              disabled={pending}
              onChange={(e) => setFiles(e.target.checked)}
            />
            Also delete files
          </label>
        )}
      </ConfirmDialog>
    </>
  )
}

describe("while it closes, the dialog shows exactly what it last showed", () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it("after a success: its title, the pending label and the slot's state stay until it is gone", async () => {
    holdClosingOverlays()
    const req = deferred()
    const user = userEvent.setup()
    render(<PageLike onConfirm={() => req.promise} />)
    await user.click(screen.getByRole("button", { name: "Open" }))
    const dialog = await screen.findByRole("alertdialog")
    await user.click(within(dialog).getByRole("checkbox", { name: "Also delete files" }))
    await user.click(within(dialog).getByRole("button", { name: "Delete" }))
    await act(async () => req.resolve())

    // Positive control: the closing dialog is still mounted (held by Radix).
    await waitFor(() => expect(dialog).toHaveAttribute("data-state", "closed"))
    expect(dialog).toBeInTheDocument()
    expect(dialog).toHaveAccessibleName("Delete “Thing”?")
    expect(within(dialog).getByRole("button", { name: "Deleting…" })).toBeInTheDocument()
    expect(within(dialog).getByRole("checkbox", { name: "Also delete files" })).toBeChecked()

    act(() => finishClosing())
    await waitFor(() => expect(dialog).not.toBeInTheDocument())
    await waitFor(() => expect(screen.getByRole("button", { name: "Open" })).toHaveFocus())
  })

  it("after a failure: the message, the Close label and the pinned position stay, and a press on Delete sends nothing", async () => {
    holdClosingOverlays()
    const onConfirm = vi.fn(() => Promise.reject(new ApiHttpError(500, "could not delete the files")))
    const user = userEvent.setup()
    render(<PageLike onConfirm={onConfirm} />)
    await user.click(screen.getByRole("button", { name: "Open" }))
    const dialog = await screen.findByRole("alertdialog")
    vi.spyOn(dialog, "getBoundingClientRect").mockReturnValue(
      DOMRect.fromRect({ x: 0, y: 300, width: 500, height: 200 }),
    )
    await user.click(within(dialog).getByRole("button", { name: "Delete" }))
    await within(dialog).findByText("could not delete the files")
    expect(dialog).toHaveAttribute("data-anchored", "bottom")

    await user.click(within(dialog).getByRole("button", { name: "Close" }))
    await waitFor(() => expect(dialog).toHaveAttribute("data-state", "closed"))
    expect(dialog).toBeInTheDocument()
    expect(dialog).toHaveAccessibleName("Delete “Thing”?")
    expect(within(dialog).getByRole("alert")).toHaveTextContent("could not delete the files")
    expect(within(dialog).getByRole("button", { name: "Close" })).toBeInTheDocument()
    expect(dialog).toHaveAttribute("data-anchored", "bottom")
    expect(dialog.style.bottom).toBe(`${window.innerHeight - 500}px`)

    // A press that lands during the fade must not start a request.
    fireEvent.click(within(dialog).getByRole("button", { name: "Delete" }))
    expect(onConfirm).toHaveBeenCalledTimes(1)

    act(() => finishClosing())
    await waitFor(() => expect(dialog).not.toBeInTheDocument())
  })

  it("reopening starts clean: centred, idle, no message", async () => {
    holdClosingOverlays()
    const onConfirm = vi.fn(() => Promise.reject(new ApiHttpError(500, "nope")))
    const user = userEvent.setup()
    render(<PageLike onConfirm={onConfirm} />)
    await user.click(screen.getByRole("button", { name: "Open" }))
    let dialog = await screen.findByRole("alertdialog")
    vi.spyOn(dialog, "getBoundingClientRect").mockReturnValue(
      DOMRect.fromRect({ x: 0, y: 300, width: 500, height: 200 }),
    )
    await user.click(within(dialog).getByRole("button", { name: "Delete" }))
    await within(dialog).findByText("nope")
    await user.click(within(dialog).getByRole("button", { name: "Close" }))
    act(() => finishClosing())
    await waitFor(() => expect(dialog).not.toBeInTheDocument())

    await user.click(screen.getByRole("button", { name: "Open" }))
    dialog = await screen.findByRole("alertdialog")
    expect(dialog).not.toHaveAttribute("data-anchored")
    expect(within(dialog).getByRole("alert")).toBeEmptyDOMElement()
    expect(within(dialog).getByRole("button", { name: "Cancel" })).toBeEnabled()
    expect(within(dialog).getByRole("button", { name: "Delete" })).not.toHaveAttribute(
      "aria-disabled",
    )
  })
})
