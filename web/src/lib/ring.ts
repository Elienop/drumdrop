// FILLED_RING_OFFSET sets a filled control's focus ring 2px off its fill,
// with a strip of the page background between them (owner's ruling
// 2026-09-24, (g); the red button had it first). The ring is amber at 60%:
// flush against an amber or red fill the two read as one fatter shape, not a
// ring. Every control with a solid fill while focused takes it: the default
// (amber), destructive and secondary buttons, the active sidebar link, and a
// checked checkbox (checkbox.tsx spells it with data-[state=checked]:).
// Outline, ghost and link controls keep the ring flush. design-tokens.test.ts
// checks both halves.
//
// The offset ring reaches 5px out, so buttons that sit side by side are 12px
// apart (gap-3), in dialog footers and page rows alike (owner's ruling
// 2026-09-24, (k)); in gap-2 the ring came within 3px of the next button.
// button-rows.test.tsx finds every pair a page renders and checks it.
export const FILLED_RING_OFFSET = "focus-visible:ring-offset-2 focus-visible:ring-offset-background"
