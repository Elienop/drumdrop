import { NavLink } from "react-router-dom"
import { LayoutDashboard, Heart, ListVideo, ListChecks, Settings } from "lucide-react"
import { FILLED_RING_OFFSET } from "@/lib/ring"
import { cn } from "@/lib/utils"

// A nav link draws the one focus ring (decisions #68): ring-ring/60 at 3px,
// on keyboard focus only, instead of the browser's own outline.
const LINK =
  "flex items-center gap-2 rounded-md px-3 py-2 text-sm font-medium outline-none focus-visible:ring-[3px] focus-visible:ring-ring/60"

// The active link is filled amber, like a primary button, so its ring sits
// 2px off the fill (owner's ruling (g), lib/ring.ts). It is the common case:
// nothing moves focus when the route changes, so a keyboard navigation
// leaves focus on the link that just became active. An idle link has no
// fill and keeps the ring flush.
const ACTIVE = cn("bg-primary text-primary-foreground", FILLED_RING_OFFSET)
const IDLE = "text-muted-foreground hover:bg-accent hover:text-foreground"

const items = [
  { to: "/", label: "Dashboard", icon: LayoutDashboard, end: true },
  { to: "/follows", label: "Follows", icon: Heart },
  { to: "/lessons", label: "Lessons", icon: ListVideo },
  { to: "/queue", label: "Queue", icon: ListChecks },
]

export function Sidebar() {
  return (
    <aside className="flex w-56 flex-col border-r bg-card/40 p-3">
      <div className="mb-6 px-2 text-lg font-bold tracking-tight">🥁 drumdrop</div>
      <nav aria-label="Primary" className="flex flex-1 flex-col gap-1">
        {items.map(({ to, label, icon: Icon, end }) => (
          <NavLink
            key={to}
            to={to}
            end={end}
            className={({ isActive }) => cn(LINK, isActive ? ACTIVE : IDLE)}
          >
            {({ isActive }) => (
              <span
                aria-current={isActive ? "page" : undefined}
                className="flex items-center gap-2"
              >
                <Icon className="size-4" /> {label}
              </span>
            )}
          </NavLink>
        ))}
        <div className="mt-auto">
          <NavLink
            to="/settings"
            className={({ isActive }) => cn(LINK, isActive ? ACTIVE : IDLE)}
          >
            {({ isActive }) => (
              <span
                aria-current={isActive ? "page" : undefined}
                className="flex items-center gap-2"
              >
                <Settings className="size-4" /> Settings
              </span>
            )}
          </NavLink>
        </div>
      </nav>
    </aside>
  )
}
