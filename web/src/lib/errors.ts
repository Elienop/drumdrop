import { toast } from "sonner"
import { ApiHttpError } from "@/lib/api"

// UNREACHABLE is the copy for a failure that carries no server message: the
// request never reached the server, or the answer was not the API's JSON (a
// reverse proxy's 502 page, a body that does not parse). It names the next
// step, which "HTTP 502" or "Delete failed" did not.
export const UNREACHABLE = "Couldn't reach the server, or it answered unexpectedly. Try again."

// errorMessage is the text shown for a failed request: the server's own
// message, verbatim (the server owns that copy), or `fallback` when there is
// none. Every toast and inline error goes through it, so a user never reads
// "HTTP 502" or a JSON parse error.
export function errorMessage(err: unknown, fallback: string = UNREACHABLE): string {
  return err instanceof ApiHttpError && err.fromServer ? err.message : fallback
}

// failureToast is the one way a failure with a description is shown as a
// toast: the outcome and the item as the title ("Couldn't queue “Six”"), the
// sentence (usually errorMessage) below, and it stays until the user closes
// it with its close button.
//
// Why not a longer timer: the descriptions are the server's full sentences,
// up to about 220 characters (35 words, 10 s or more for many readers), so
// any fixed time is a guess tuned to one length, and WCAG 2.2.1 asks that
// text a user must read does not time out. A dialog's late failure already
// stayed until dismissed; this makes it one rule for every such toast. A
// title-only toast ("No daemon attached") keeps sonner's default.
export function failureToast(title: string, description: string): void {
  toast.error(title, { description, duration: Infinity, closeButton: true })
}

// ItemOutcome is how a request on one item (a lesson, a follow) ended: "done"
// by this request, or "already-gone" when the server answered 404 because
// something else removed the item first (elsewhere, or with its follow). For
// a delete the user asked for it to be gone and it is; for a skip or an edit
// nothing is left to act on. Either way it is not a failure: a dialog that
// offered a retry would only repeat the 404.
export type ItemOutcome = "done" | "already-gone"

// itemOutcome maps a request on one item to its ItemOutcome. Only the API's
// own 404 counts: a proxy's 404 page (no server message) stays a failure,
// since then nothing says the item is gone.
export function itemOutcome(request: Promise<unknown>): Promise<ItemOutcome> {
  return request.then(
    () => "done" as const,
    (err: unknown) => {
      if (err instanceof ApiHttpError && err.fromServer && err.status === 404) {
        return "already-gone" as const
      }
      throw err
    },
  )
}
