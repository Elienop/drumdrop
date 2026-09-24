import {
  CircleCheckIcon,
  InfoIcon,
  Loader2Icon,
  OctagonXIcon,
  TriangleAlertIcon,
} from "lucide-react"
import { Toaster as Sonner, type ToasterProps } from "sonner"

// The one focus ring (decisions #68): ring-ring/60 at 3px, on keyboard focus
// only. A failure toast stays until closed (failureToast), so a keyboard user
// reaches it (Alt+T, then Tab) and must see where focus is. sonner's own
// focus styles are an rgba(0,0,0,.2) shadow, invisible on this dark theme,
// and the browser's outline on the close button; both sit in :where(), with
// zero specificity, so these classes win.
const FOCUS_RING = "outline-none focus-visible:ring-[3px] focus-visible:ring-ring/60"

// Dark-only app: there is no ThemeProvider, so the theme is hardcoded to "dark"
// instead of reading next-themes' useTheme().
//
// LOCAL EDIT (owner, 2026-09-24): the close × is a normal close button inside
// the toast's top-right corner, level with the title, like the dialogs' X
// (dialog.tsx), and not sonner's round badge half over the top-left corner.
// - Position: sonner's own variables (--toast-close-button-start/end/
//   transform) move it to the right edge, 12px in, so its 12px glyph lines up
//   with the toast's 16px padding; top-4 (16px) sets it on the title's line.
//   `auto`, not `unset`: a custom property set to unset inherits instead.
// - Look: no border and no fill of its own. sonner paints its background in
//   the toast's own colour, and on hover in --normal-bg-hover, which only the
//   close button reads, so that is made transparent; hover brightens it as
//   the dialogs' X does.
// - Room: a toast with a close button keeps 40px clear on the right, so a
//   long title or sentence never runs under the ×.
const Toaster = ({ ...props }: ToasterProps) => {
  return (
    <Sonner
      theme="dark"
      className="toaster group"
      icons={{
        success: <CircleCheckIcon className="size-4" />,
        info: <InfoIcon className="size-4" />,
        warning: <TriangleAlertIcon className="size-4" />,
        error: <OctagonXIcon className="size-4" />,
        loading: <Loader2Icon className="size-4 animate-spin" />,
      }}
      toastOptions={{
        classNames: {
          toast: `${FOCUS_RING} has-[[data-close-button]]:pr-10`,
          closeButton: `${FOCUS_RING} top-4 rounded-xs border-0 opacity-70 hover:opacity-100 focus-visible:opacity-100`,
        },
      }}
      style={
        {
          "--normal-bg": "var(--popover)",
          "--normal-bg-hover": "transparent",
          "--normal-text": "var(--popover-foreground)",
          "--normal-border": "var(--border)",
          "--border-radius": "var(--radius)",
          "--toast-close-button-start": "auto",
          "--toast-close-button-end": "0.75rem",
          "--toast-close-button-transform": "none",
        } as React.CSSProperties
      }
      {...props}
    />
  )
}

export { Toaster }
