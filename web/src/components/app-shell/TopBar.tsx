import { NavLink } from "react-router-dom"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Pause, Play, RefreshCw } from "lucide-react"
import { api } from "@/lib/api"
import { errorMessage, failureToast } from "@/lib/errors"
import { qk } from "@/lib/queryKeys"
import { cn } from "@/lib/utils"
import { Button } from "@/components/ui/button"
import { PendingButton } from "@/components/PendingButton"
import { statusTone } from "@/components/StatusBadge"
import { GlobalProgress } from "./GlobalProgress"

// TopBar is a thin header: the live activity strip in the center, a Pause/Resume
// toggle driven by the daemon's pause flag (summary.paused), the Musora
// account-session pill on the right (GET /api/session — distinct from the SSE
// stream status), and a "Sync" link to the Dashboard where the real mutation
// lives.
export function TopBar() {
  const qc = useQueryClient()
  const session = useQuery({ queryKey: qk.session, queryFn: api.getSession })
  const connected = session.data?.connected ?? false

  const summary = useQuery({ queryKey: qk.summary, queryFn: api.summary })
  const paused = summary.data?.paused ?? false

  // `resume` is what the press asked for, fixed when it was pressed. A
  // failure says so with the server's sentence (no daemon attached, or a
  // server error), like every other action, and the flag is re-read after
  // either outcome.
  const toggle = useMutation({
    mutationFn: (resume: boolean) => (resume ? api.resume() : api.pause()),
    onError: (err, resume) =>
      failureToast(resume ? "Couldn't resume syncing" : "Couldn't pause syncing", errorMessage(err)),
    // RETURN the refetch, never wrap it in { }: TanStack waits for a returned
    // promise before it ends the mutation, so the spinner lasts until the new
    // flag is in. Without it the button is live again for one round trip,
    // still offering "Pause" on a daemon that is already paused.
    onSettled: () => qc.invalidateQueries({ queryKey: qk.summary }),
  })
  // While a press runs the button keeps the verb that was pressed, even if
  // the flag is re-read meanwhile.
  const showResume = toggle.isPending ? toggle.variables === true : paused

  return (
    <header className="flex h-14 items-center gap-4 border-b px-6">
      <GlobalProgress paused={paused} />
      {/* PendingButton, not `disabled`: a disabled button drops keyboard
          focus to <body> while the request runs. Compact (owner's ruling
          2026-09-24, (l)): the spinner takes the icon's place and the label
          stays "Pause" or "Resume", so it is as wide as Sync beside it and
          reserves no room for a longer pending label. */}
      <PendingButton
        size="sm"
        variant="secondary"
        pending={toggle.isPending}
        icon={showResume ? Play : Pause}
        onClick={() => toggle.mutate(paused)}
      >
        {showResume ? "Resume" : "Pause"}
      </PendingButton>
      <span
        className={cn(
          "rounded-full border px-2.5 py-1 text-xs font-medium",
          connected ? statusTone.success : statusTone.neutral,
        )}
      >
        {connected ? "Musora connected" : "Musora disconnected"}
      </span>
      <Button asChild size="sm" variant="secondary">
        <NavLink to="/">
          <RefreshCw /> Sync
        </NavLink>
      </Button>
    </header>
  )
}
