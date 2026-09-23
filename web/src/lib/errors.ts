import { ApiHttpError } from "@/lib/api"

// UNREACHABLE is the copy for a failure that carries no server message: the
// request never reached the server, or the answer was not the API's JSON (a
// reverse proxy's 502 page, a body that does not parse). It names the next
// step, which "HTTP 502" or "Delete failed" did not.
export const UNREACHABLE = "Couldn't reach the server, or it answered unexpectedly. Try again."

// errorMessage is the text a dialog shows for a failed request: the server's
// own message, verbatim (the server owns that copy), or `fallback` when there
// is none.
export function errorMessage(err: unknown, fallback: string = UNREACHABLE): string {
  return err instanceof ApiHttpError && err.fromServer ? err.message : fallback
}
