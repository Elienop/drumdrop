import * as React from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { api } from "@/lib/api"
import { brandName, countOf } from "@/lib/format"
import { qk } from "@/lib/queryKeys"
import { useDialogRequest } from "@/lib/dialog-request"
import type { FocusTarget } from "@/lib/focus"
import { useOpenedNow } from "@/lib/use-held"
import type { CreateFollowRequest, PreviewResponse } from "@/types"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { InlineError } from "@/components/InlineError"
import { PendingButton } from "@/components/PendingButton"
import { StackedLabel } from "@/components/StackedLabel"

type Kind = "node" | "instructor"

// Quality presets passed through to the follow; "best" lets yt-dlp pick the
// highest available, the rest cap the video height (e.g. 1080 → ≤1080p).
export const QUALITY_OPTIONS = ["best", "2160", "1440", "1080", "720", "480"] as const

// What a preview is built from: the kind and the identity fields, exactly as
// typed. Quality is not part of it (it does not change what is followed). The
// instructor field is sent raw: the server normalises it the same way on
// preview and on add, and the preview reports the slug it arrived at.
type Target =
  | { kind: "node"; id: string }
  | { kind: "instructor"; slug: string; brand: string }

interface Preview {
  key: string // targetKey(target)
  target: Target
  data: PreviewResponse
}

const targetKey = (t: Target) => JSON.stringify(t)

// AddFollowDialog is the preview-then-add flow: a segmented kind control
// (node | instructor), an input (URL-or-id for node; name, slug or coach link,
// + optional brand, for instructor), a Preview button that fetches the title +
// lesson_count (and, for an instructor, the slug and brand it resolves to), and an
// Add button that registers the follow. createFollow resolves to { status,
// data }: 201 → newly created ("Follow added"), 200 → already following.
//
// The preview belongs to the exact input it was built from: it is shown, and
// Add is enabled, only while the kind and fields still match it, and Add
// sends that previewed input. So an edit after the preview, or a preview that
// lands after the kind was switched, can never add something other than what
// the dialog shows.
//
// A failure of either step shows inside the dialog (a 400 saying what input
// is accepted, and a 502, carry a clean server message), so the user can
// correct the input; a toast would not be heard while the modal hides the
// rest of the page.
export function AddFollowDialog({
  open,
  onOpenChange,
  returnFocus,
}: Readonly<{
  open: boolean
  onOpenChange: (open: boolean) => void
  returnFocus: () => FocusTarget[]
}>) {
  const qc = useQueryClient()
  // The daemon's pause flag, from the top bar's query and read as it reads
  // it. Only while open, so opening the dialog re-reads a stale flag. An
  // unknown flag (the summary still loading, failed, or without the field)
  // reads as not paused, as the top bar reads it (it then offers Pause): a
  // paused line would point to a Resume the top bar isn't showing.
  const summary = useQuery({ queryKey: qk.summary, queryFn: api.summary, enabled: open })
  const paused = summary.data?.paused ?? false
  const [kind, setKind] = React.useState<Kind>("node")
  const [id, setId] = React.useState("")
  const [slug, setSlug] = React.useState("")
  const [brand, setBrand] = React.useState("")
  const [quality, setQuality] = React.useState("best")
  const [preview, setPreview] = React.useState<Preview | null>(null)
  const [step, setStep] = React.useState<"preview" | "add">("preview")
  const { pending, error, run, onCloseAutoFocus, dismissError } = useDialogRequest({
    open,
    returnFocus,
  })
  const errorId = React.useId()
  const slugHintId = React.useId()
  const addRef = React.useRef<HTMLButtonElement>(null)

  // A reopen starts clean. Reset as it opens, not as it closes, so the dialog
  // fades out showing what it showed.
  if (useOpenedNow(open)) {
    setKind("node")
    setId("")
    setSlug("")
    setBrand("")
    setQuality("best")
    setPreview(null)
    setStep("preview")
  }

  // While Add runs the form is locked: what was sent cannot change under it
  // (and the Add button cannot lose its preview and drop focus). During a
  // preview it stays editable; an edit just leaves that preview unshown.
  const adding = pending && step === "add"
  // While a preview runs, Preview shows it and Add waits. `step` is one of
  // the two, so a running request is exactly one of `adding` and `previewing`
  // and each button is disabled while the other one's request runs.
  const previewing = pending && step === "preview"

  const target: Target = kind === "node" ? { kind, id } : { kind, slug, brand }
  // No preview has no key, and a target's key is always a string.
  const shown = preview?.key === targetKey(target) ? preview : null

  const runPreview = () => {
    setStep("preview")
    const sent = target
    void run(
      () =>
        sent.kind === "node"
          ? api.preview({ id: sent.id })
          : api.preview({ slug: sent.slug, brand: sent.brand || undefined }),
      { keepOpen: true, done: (data) => setPreview({ key: targetKey(sent), target: sent, data }) },
    )
  }

  const runAdd = () => {
    if (!shown) return
    // Focus the button first: Safari does not focus a clicked button, and
    // focus left in a field that is about to be disabled drops to <body>.
    addRef.current?.focus()
    setStep("add")
    const t = shown.target
    const body: CreateFollowRequest =
      t.kind === "node"
        ? { kind: "node", id: t.id, quality }
        : { kind: "instructor", slug: t.slug, brand: t.brand || undefined, quality }
    void run(
      async () => {
        const res = await api.createFollow(body)
        await Promise.all([
          qc.invalidateQueries({ queryKey: qk.follows }),
          qc.invalidateQueries({ queryKey: qk.summary }),
        ])
        return res
      },
      {
        done: () => onOpenChange(false),
        announce: ({ status, data }) => {
          if (status === 201) toast.success("Follow added", { description: data.title })
          else toast.message("Already following", { description: data.title })
        },
        // An unknown node id previews with an empty title.
        failure: shown.data.title.trim()
          ? `Couldn't add “${shown.data.title}”`
          : "Couldn't add the follow",
      },
    )
  }

  const canPreview = kind === "node" ? id.trim() !== "" : slug.trim() !== ""

  // Enter in a field runs the next step: Add when the preview shown is of
  // exactly this input, else Preview. Handled per field rather than by a
  // form: with two fields (slug and brand) and no submit button, a form
  // does not submit on Enter at all.
  //
  // - isComposing: that Enter confirms an input method's candidate, not the
  //   field.
  // - repeat: a held Enter repeats. Only a fresh press counts, so holding it
  //   can never add a follow whose preview the user has not seen yet.
  // - pending: a press while a request runs does nothing (a second preview
  //   of the same input would only repeat it, and Add must not start while
  //   a preview is still on its way).
  const onFieldEnter = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key !== "Enter" || e.nativeEvent.isComposing) return
    e.preventDefault()
    if (e.repeat || pending) return
    if (shown) runAdd()
    else if (canPreview) runPreview()
  }

  // An edit makes the last failure stale: it was about what was sent, which
  // the fields no longer say.
  const edit = (set: (value: string) => void) => (e: React.ChangeEvent<HTMLInputElement>) => {
    set(e.target.value)
    dismissError()
  }

  // The failure describes the button of the step that failed. Not while a
  // request runs: the message is about the last attempt.
  const describedBy = (of: typeof step) =>
    error !== null && !pending && step === of ? errorId : undefined

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      {/* flex, not the primitive's grid: see InlineError. */}
      <DialogContent
        onCloseAutoFocus={onCloseAutoFocus}
        className="flex max-h-[calc(100dvh-2rem)] flex-col overflow-y-auto"
      >
        <DialogHeader>
          <DialogTitle className="leading-snug">Add follow</DialogTitle>
          {/* What adding does next (owner's ruling 2026-09-24, (s)), true in
              every state. Adding asks the daemon for a sync now, but one
              already running finishes first, and while syncing is paused
              the daemon drops the request: nothing starts until Resume,
              which asks again (internal/scheduler/daemon.go, Run). */}
          <DialogDescription>
            Preview a node or instructor, then add it to your follows.{" "}
            {paused
              ? "Syncing is paused: its lessons start downloading when you Resume."
              : "Its lessons start downloading right away, or after any sync already running."}
          </DialogDescription>
        </DialogHeader>

        <Tabs
          value={kind}
          onValueChange={(v) => {
            setKind(v as Kind)
            dismissError()
          }}
        >
          <TabsList className="w-full">
            <TabsTrigger value="node" disabled={adding}>
              Node
            </TabsTrigger>
            <TabsTrigger value="instructor" disabled={adding}>
              Instructor
            </TabsTrigger>
          </TabsList>

          <TabsContent value="node" className="flex flex-col gap-2 pt-2">
            <Label htmlFor="follow-node-id">URL or id</Label>
            <Input
              id="follow-node-id"
              placeholder="https://drumeo.com/… or 12345"
              value={id}
              disabled={adding}
              onChange={edit(setId)}
              onKeyDown={onFieldEnter}
            />
          </TabsContent>

          {/* One group per field, so the hint reads as the name field's and
              not as a caption over the brand below it. */}
          <TabsContent value="instructor" className="flex flex-col gap-4 pt-2">
            <div className="flex flex-col gap-2">
              {/* Named for what to type, like the node tab's "URL or id"; the
                  tab already says it is an instructor. A link is a coach
                  page's, whose brand the follow takes when Brand is empty. */}
              <Label htmlFor="follow-slug">Name, slug or link</Label>
              <Input
                id="follow-slug"
                placeholder="Jared Falk"
                aria-describedby={slugHintId}
                value={slug}
                disabled={adding}
                onChange={edit(setSlug)}
                onKeyDown={onFieldEnter}
              />
              <p id={slugHintId} className="text-sm text-muted-foreground">
                For example Jared Falk, jared-falk, or a link to their coach page.
              </p>
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="follow-brand">Brand (optional)</Label>
              <Input
                id="follow-brand"
                placeholder="Drumeo"
                value={brand}
                disabled={adding}
                onChange={edit(setBrand)}
                onKeyDown={onFieldEnter}
              />
            </div>
          </TabsContent>
        </Tabs>

        <div className="flex flex-col gap-2">
          <Label htmlFor="follow-quality">Quality</Label>
          <Select value={quality} onValueChange={setQuality} disabled={adding}>
            <SelectTrigger id="follow-quality" aria-label="Quality">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectGroup>
                {QUALITY_OPTIONS.map((q) => (
                  <SelectItem key={q} value={q}>
                    {q}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
        </div>

        {/* The preview is announced as it lands: a keyboard user can press
            Enter twice (Preview, then Add) and must hear what Add will
            follow. Radix announces nothing that changes inside a dialog, so
            this is a live region, and like InlineError's it is ALWAYS
            rendered, only its content changes (a region inserted together
            with its text is missed by some screen readers). Empty, it costs
            no space: empty:-mt-4 cancels the flex column's gap-4.
            <output> is the native status region (polite and atomic, as
            role="status" is). Inline by default, but as an item of this
            flex column it is blockified: the same box the <div> had. It
            holds phrasing content only, so the boxes inside are spans; each
            sets its own display (flex), so a span draws a div's box. */}
        <output className="empty:-mt-4">
          {shown && (
            <span className="flex flex-col gap-1 rounded-md border bg-muted/40 p-3">
              {/* The slug beside the name is what will be followed, whatever
                  was typed: "Jared Falk" previews as @jared-falk. The brand
                  ends the count line, as the count is of that brand's
                  lessons: a pianote link with Brand empty reads "… lessons on
                  Pianote".
                  The {" "} between the parts: without them the region's
                  textContent runs them together ("Falk@jared-falk40").
                  Chrome's accessibility tree keeps each flex item's text
                  apart either way (checked, round 5b); the spaces stay for
                  other browsers and screen readers, which may read the
                  joined text. Blank text between flex items is not
                  rendered, so nothing moves on screen. */}
              <span className="flex flex-wrap items-baseline gap-x-2">
                <span className="font-medium">{shown.data.title}</span>{" "}
                {shown.data.slug && (
                  <span className="text-sm wrap-anywhere text-muted-foreground">
                    @{shown.data.slug}
                  </span>
                )}
              </span>{" "}
              <span className="text-sm text-muted-foreground">
                {countOf(shown.data.lesson_count, "lesson", "lessons")}
                {shown.data.brand && ` on ${brandName(shown.data.brand)}`}
              </span>
            </span>
          )}
        </output>

        <InlineError id={errorId} error={error} stale={pending} />

        <DialogFooter>
          {/* "Close" only while Add runs: closing does not stop it, and its
              result then arrives as a notification. A preview's result is
              dropped when the dialog closes, so then it is a plain Cancel. */}
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            <StackedLabel labels={{ cancel: "Cancel", close: "Close" }} active={adding ? "close" : "cancel"} />
          </Button>
          {/* The fill marks the next step, and only one button has it:
              Preview until a preview of this input is shown, then Add
              (owner, 2026-09-24). The fill follows the step, not whether
              the button can run: Preview stays filled, dimmed, while an
              empty field disables it, and Add stays outline until a
              preview is shown. */}
          <PendingButton
            variant={shown ? "outline" : "default"}
            pending={previewing}
            pendingLabel="Previewing…"
            disabled={!canPreview || adding}
            aria-describedby={describedBy("preview")}
            onClick={runPreview}
          >
            Preview
          </PendingButton>
          <PendingButton
            ref={addRef}
            variant={shown ? "default" : "outline"}
            pending={adding}
            pendingLabel="Adding…"
            disabled={!shown || previewing}
            aria-describedby={describedBy("add")}
            onClick={runAdd}
          >
            Add
          </PendingButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
