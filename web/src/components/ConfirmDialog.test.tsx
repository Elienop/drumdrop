import * as React from "react"
import { afterEach, expect, it, vi } from "vitest"
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Toaster } from "@/components/ui/sonner"
import { ApiHttpError } from "@/lib/api"
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

function Harness(props: Partial<ConfirmDialogProps> & Pick<ConfirmDialogProps, "onConfirm">) {
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
        subject="Thing"
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
  req.resolve()
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

  req.reject(new ApiHttpError(500, "could not delete the files"))
  expect(await screen.findByText("could not delete the files")).toBeInTheDocument()
  expect(screen.getByText("Thing")).toBeInTheDocument() // the toast's description
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
  second.resolve()
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

  // A resize makes the frozen position stale: back to centred.
  fireEvent(window, new Event("resize"))
  await waitFor(() => expect(dialog).not.toHaveAttribute("data-anchored"))
  expect(dialog.style.bottom).toBe("")
  req.resolve()
})
