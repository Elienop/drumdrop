import { NavLink } from "react-router-dom"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Pause, Play, RefreshCw } from "lucide-react"
import { api } from "@/lib/api"
import { qk } from "@/lib/queryKeys"
import { cn } from "@/lib/utils"
import { Button } from "@/components/ui/button"
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

  const toggle = useMutation({
    mutationFn: () => (paused ? api.resume() : api.pause()),
    onSuccess: () => qc.invalidateQueries({ queryKey: qk.summary }),
  })

  return (
    <header className="flex h-14 items-center gap-4 border-b px-6">
      <GlobalProgress paused={paused} />
      <Button
        size="sm"
        variant="secondary"
        onClick={() => toggle.mutate()}
        disabled={toggle.isPending}
      >
        {paused ? <Play /> : <Pause />}
        {paused ? "Resume" : "Pause"}
      </Button>
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
