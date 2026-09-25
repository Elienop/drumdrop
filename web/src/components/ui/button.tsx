import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { Slot } from "radix-ui"

import { FILLED_RING_OFFSET } from "@/lib/ring"
import { cn } from "@/lib/utils"

const buttonVariants = cva(
  // LOCAL EDIT (owner's rulings 2026-09-23, decisions #68): the focus ring is
  // ring-ring/60, not upstream's /50, as in every other primitive here: at
  // least 3.4:1 on the background, cards, popovers and muted tab lists (/50
  // was under 3:1 on all but the background). design-tokens.test.ts checks it.
  "inline-flex shrink-0 items-center justify-center gap-2 rounded-md text-sm font-medium whitespace-nowrap transition-all outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/60 disabled:pointer-events-none disabled:opacity-50 aria-invalid:border-destructive aria-invalid:ring-destructive/20 dark:aria-invalid:ring-destructive/40 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
  {
    variants: {
      variant: {
        // LOCAL EDIT (owner's ruling 2026-09-24, (g)): every FILLED variant
        // (default, destructive, secondary) sets its ring 2px off the fill,
        // over the page background (FILLED_RING_OFFSET, lib/ring.ts), as the
        // red one did first; outline, ghost and link keep the ring flush.
        // Re-apply when updating the component from upstream.
        default: `bg-primary text-primary-foreground hover:bg-primary/90 ${FILLED_RING_OFFSET}`,
        // LOCAL EDIT (owner's rulings 2026-09-23, decisions #68). Re-apply
        // all three when updating the component from upstream:
        // - Ring colour: upstream's red focus ring (focus-visible:ring-
        //   destructive/20, dark:…/40) is removed, so the button keeps the
        //   base amber ring like every other control (red/40 was under 2:1).
        // - Ring offset: a 2px strip of the dialog's background between the
        //   red fill and the amber ring. The two colours are equally bright
        //   (1:1), so without it the ring reads as a fatter button.
        // - Dark hover: dark:hover:bg-destructive/50. Upstream's
        //   hover:bg-destructive/90 loses to dark:bg-destructive/60 (same
        //   specificity, later in the CSS), so the button had no hover in
        //   dark mode. It dims like the primary button's hover; white text
        //   on it is 7.7:1.
        destructive:
          `bg-destructive text-white hover:bg-destructive/90 ${FILLED_RING_OFFSET} dark:bg-destructive/60 dark:hover:bg-destructive/50`,
        // LOCAL EDIT (owner's ruling 2026-09-25): dark:focus-visible:border-
        // ring. The base focus-visible:border-ring loses to dark:border-input
        // (same specificity, later in the CSS, as the dark hover above), so
        // in the dark theme an outline button's border never turned amber on
        // focus, as an Input's and a Select's do. Re-apply when updating the
        // component from upstream.
        outline:
          "border bg-background shadow-xs hover:bg-accent hover:text-accent-foreground dark:border-input dark:bg-input/30 dark:hover:bg-input/50 dark:focus-visible:border-ring",
        secondary: `bg-secondary text-secondary-foreground hover:bg-secondary/80 ${FILLED_RING_OFFSET}`,
        ghost:
          "hover:bg-accent hover:text-accent-foreground dark:hover:bg-accent/50",
        link: "text-primary underline-offset-4 hover:underline",
      },
      size: {
        default: "h-9 px-4 py-2 has-[>svg]:px-3",
        xs: "h-6 gap-1 rounded-md px-2 text-xs has-[>svg]:px-1.5 [&_svg:not([class*='size-'])]:size-3",
        sm: "h-8 gap-1.5 rounded-md px-3 has-[>svg]:px-2.5",
        lg: "h-10 rounded-md px-6 has-[>svg]:px-4",
        icon: "size-9",
        "icon-xs": "size-6 rounded-md [&_svg:not([class*='size-'])]:size-3",
        "icon-sm": "size-8",
        "icon-lg": "size-10",
      },
    },
    defaultVariants: {
      variant: "default",
      size: "default",
    },
  }
)

function Button({
  className,
  variant = "default",
  size = "default",
  asChild = false,
  ...props
}: React.ComponentProps<"button"> &
  VariantProps<typeof buttonVariants> & {
    asChild?: boolean
  }) {
  const Comp = asChild ? Slot.Root : "button"

  return (
    <Comp
      data-slot="button"
      data-variant={variant}
      data-size={size}
      className={cn(buttonVariants({ variant, size, className }))}
      {...props}
    />
  )
}

export { Button, buttonVariants }
