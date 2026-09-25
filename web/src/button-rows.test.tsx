import type { ReactElement } from "react"
import { describe, expect, it } from "vitest"
import { screen } from "@testing-library/react"
import { http, HttpResponse } from "msw"
import { ORIGIN, renderWithProviders, server } from "@/test/msw"
import type { FollowDTO, JobDTO, LessonDTO } from "@/types"
import { Dashboard } from "@/pages/Dashboard"
import { Follows } from "@/pages/Follows"
import { Lessons } from "@/pages/Lessons"
import { Queue } from "@/pages/Queue"
import { Settings } from "@/pages/Settings"

// Owner's ruling 2026-09-24, (k): 12px between buttons wherever they pair,
// page rows as well as dialog footers (design-tokens.test.ts checks those).
// A filled button's focus ring reaches 5px out (a 2px offset plus the 3px
// ring, lib/ring.ts), so in an 8px row it came within 3px of its neighbour.
//
// This finds every row of paired buttons a page renders, rather than
// listing them, so a new pair is checked too: an element with two or more
// children that are buttons (or a wrapper around exactly one button).
// jsdom computes no layout, so it reads the row's gap class; the browser
// pass measures the space.

const TAILWIND_STEP_PX = 4

function isButtonChild(el: Element): boolean {
  if (el.matches('[data-slot="button"]')) return true
  return el.children.length === 1 && el.firstElementChild!.matches('[data-slot="button"]')
}

// Every element holding two or more buttons side by side, with the space its
// gap class puts between them (0 without one).
function buttonRows(root: HTMLElement) {
  return [...root.querySelectorAll("*")]
    .filter((el) => [...el.children].filter(isButtonChild).length >= 2)
    .map((row) => {
      const gap = row.className
        .toString()
        .split(/\s+/)
        .map((c) => c.match(/^gap-(?:x-)?(\d+(?:\.\d+)?)$/))
        .find((m) => m !== null)
      const names = [...row.querySelectorAll('[data-slot="button"]')].map(
        (b) => b.getAttribute("aria-label") ?? b.textContent,
      )
      return { names: names.join(" | "), gapPx: gap ? Number(gap[1]) * TAILWIND_STEP_PX : 0 }
    })
}

const lesson: LessonDTO = {
  railcontent_id: 100,
  title: "Single Stroke Roll",
  parent_railcontent_id: null,
  brand: "drumeo",
  status: "failed",
  quality: "1080p",
  output_dir: null,
  has_files: false,
  deleting: false,
  video_path: null,
  bytes: null,
  error: null,
  follow_id: null,
  first_seen_at: "2026-05-01T00:00:00Z",
  downloaded_at: null,
  updated_at: "2026-05-29T00:00:00Z",
}

const job: JobDTO = {
  id: 11,
  follow_id: null,
  railcontent_id: 100,
  status: "failed",
  attempts: 3,
  error: "ffmpeg exited 1",
  created_at: "2026-05-29T00:00:00Z",
  started_at: "2026-05-29T00:01:00Z",
  finished_at: "2026-05-29T00:02:00Z",
}

const follow: FollowDTO = {
  id: 1,
  kind: "node",
  railcontent_id: 12345,
  slug: null,
  title: "Stick Control",
  brand: "drumeo",
  quality: "1080p",
  added_at: "2026-05-01T00:00:00Z",
  last_synced_at: "2026-05-29T00:00:00Z",
}

describe("paired buttons stand 12px apart on every page", () => {
  it.each<[string, () => ReactElement, string, number]>([
    ["Dashboard (Run sync, Dry-run)", () => <Dashboard />, "Run sync", 1],
    ["Settings (Save, Clear)", () => <Settings />, "Clear", 1],
    ["Lessons (Prev, Next)", () => <Lessons />, "Next", 1],
    ["Queue (Retry, Cancel)", () => <Queue />, "Retry", 1],
    ["Follows (Edit, Remove)", () => <Follows />, "Edit Stick Control", 1],
  ])("%s", async (_, page, marker, rows) => {
    server.use(
      http.get(`${ORIGIN}/healthz`, () => HttpResponse.json({ status: "ok", version: "v1" })),
      http.get(`${ORIGIN}/api/lessons`, () => HttpResponse.json([lesson])),
      http.get(`${ORIGIN}/api/jobs`, () => HttpResponse.json([job])),
      http.get(`${ORIGIN}/api/follows`, () => HttpResponse.json([follow])),
    )
    const { container } = renderWithProviders(page())
    await screen.findByRole("button", { name: marker })

    const found = buttonRows(container)
    // The page's pairs are found at all (a scan that finds nothing passes).
    expect(found).toHaveLength(rows)
    for (const row of found) expect(row, row.names).toMatchObject({ gapPx: 12 })
  })
})
