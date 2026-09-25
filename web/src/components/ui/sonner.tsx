import {
  CircleCheckIcon,
  InfoIcon,
  Loader2Icon,
  OctagonXIcon,
  TriangleAlertIcon,
} from "lucide-react"
import { Toaster as Sonner, type ToasterProps } from "sonner"

// WHY THE "!" SUFFIXES: sonner injects its stylesheet as an UNLAYERED
// <style> in <head> (wt() in node_modules/sonner/dist/index.mjs), while
// Tailwind v4 puts every utility in @layer utilities. Under CSS Cascade 5 an
// unlayered normal declaration beats a layered one WHATEVER its specificity,
// so sonner's :where() rules still win over a plain class (seen in a browser:
// the × kept top:0, its 1px border and its 50% radius, and the toast its 16px
// padding). Tailwind's "!" suffix emits !important, which beats any normal
// declaration. Only the properties sonner also sets need it: box-shadow (the
// ring), top, border, border-radius, padding and align-items. Custom
// properties (the --toast-close-button-* below, --tw-ring-color), opacity
// and the icon's margin-top are not contested and need none.
//
// The one focus ring (decisions #68): ring-ring/60 at 3px, on keyboard focus
// only. A failure toast stays until closed (failureToast), so a keyboard user
// reaches it (Alt+T, then Tab) and must see where focus is. sonner's own
// focus style is an rgba(0,0,0,.2) box-shadow, invisible on this dark theme.
const FOCUS_RING = "outline-none focus-visible:ring-[3px]! focus-visible:ring-ring/60"

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
// - Icon: level with the title's FIRST line, like the ×. sonner centres the
//   icon against the whole text (align-items: center), so beside a title that
//   wraps, or a title and its sentence, it sat lower than the ×. The toast
//   aligns to the top instead (items-start!, sonner sets align-items), and the
//   16px icon drops by half of what the title's line box (1.5em, sonner's
//   line-height) has over it: centred on that line. A one-line toast looks as
//   before, since there the line box is the whole text.
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
          toast: `${FOCUS_RING} has-[[data-close-button]]:pr-10! items-start!`,
          icon: "mt-[calc((1.5em_-_16px)/2)]",
          closeButton: `${FOCUS_RING} top-4! rounded-xs! border-0! opacity-70 hover:opacity-100 focus-visible:opacity-100`,
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
