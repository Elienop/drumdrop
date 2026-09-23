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

// DeleteOutcome is how a DELETE ended: "deleted" by this request, or
// "already-gone" when the server answered 404 because something else removed
// it first (another tab, a sync). The user asked for it to be gone and it is,
// so that is not a failure: offering Retry would only repeat the 404.
export type DeleteOutcome = "deleted" | "already-gone"

// deleteOutcome maps a DELETE request to its DeleteOutcome. Only the API's own
// 404 counts: a proxy's 404 page (no server message) stays a failure, since
// then nothing says the item is gone.
export function deleteOutcome(request: Promise<unknown>): Promise<DeleteOutcome> {
  return request.then(
    () => "deleted" as const,
    (err: unknown) => {
      if (err instanceof ApiHttpError && err.fromServer && err.status === 404) {
        return "already-gone" as const
      }
      throw err
    },
  )
}
