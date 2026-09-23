import * as React from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { api } from "@/lib/api"
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
// typed. Quality is not part of it (it does not change what is followed).
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
// (node | instructor), an input (URL-or-id for node, slug + optional brand for
// instructor), a Preview button that fetches the title + lesson_count, and an
// Add button that registers the follow. createFollow resolves to { status,
// data }: 201 → newly created ("Follow added"), 200 → already following.
//
// The preview belongs to the exact input it was built from: it is shown, and
// Add is enabled, only while the kind and fields still match it, and Add
// sends that previewed input. So an edit after the preview, or a preview that
// lands after the kind was switched, can never add something other than what
// the dialog shows.
//
// A failure of either step shows inside the dialog (the 400 "check the URL or
// slug" and 502 carry a clean server message), so the user can correct the
// input; a toast would not be heard while the modal hides the rest of the page.
export function AddFollowDialog({
  open,
  onOpenChange,
  returnFocus,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  returnFocus: () => FocusTarget[]
}) {
  const qc = useQueryClient()
  const [kind, setKind] = React.useState<Kind>("node")
  const [id, setId] = React.useState("")
  const [slug, setSlug] = React.useState("")
  const [brand, setBrand] = React.useState("")
  const [quality, setQuality] = React.useState("best")
  const [preview, setPreview] = React.useState<Preview | null>(null)
  const [step, setStep] = React.useState<"preview" | "add">("preview")
  const { pending, error, run, onCloseAutoFocus } = useDialogRequest({ open, returnFocus })
  const errorId = React.useId()
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

  const target: Target = kind === "node" ? { kind, id } : { kind, slug, brand }
  const shown = preview !== null && preview.key === targetKey(target) ? preview : null

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
        failure: `Couldn't add “${shown.data.title}”`,
      },
    )
  }

  const canPreview = kind === "node" ? id.trim() !== "" : slug.trim() !== ""

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      {/* flex, not the primitive's grid: see InlineError. */}
      <DialogContent
        onCloseAutoFocus={onCloseAutoFocus}
        className="flex max-h-[calc(100dvh-2rem)] flex-col overflow-y-auto"
      >
        <DialogHeader>
          <DialogTitle className="leading-snug">Add follow</DialogTitle>
          <DialogDescription>
            Preview a node or instructor, then add it to your follows.
          </DialogDescription>
        </DialogHeader>

        <Tabs value={kind} onValueChange={(v) => setKind(v as Kind)}>
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
              onChange={(e) => setId(e.target.value)}
            />
          </TabsContent>

          <TabsContent value="instructor" className="flex flex-col gap-2 pt-2">
            <Label htmlFor="follow-slug">Slug</Label>
            <Input
              id="follow-slug"
              placeholder="jared-falk"
              value={slug}
              disabled={adding}
              onChange={(e) => setSlug(e.target.value)}
            />
            <Label htmlFor="follow-brand">Brand (optional)</Label>
            <Input
              id="follow-brand"
              placeholder="drumeo"
              value={brand}
              disabled={adding}
              onChange={(e) => setBrand(e.target.value)}
            />
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

        {shown && (
          <div className="flex flex-col gap-1 rounded-md border bg-muted/40 p-3">
            <span className="font-medium">{shown.data.title}</span>
            <span className="text-sm text-muted-foreground">
              {shown.data.lesson_count} lessons
            </span>
          </div>
        )}

        <InlineError id={errorId} error={error} stale={pending} />

        <DialogFooter>
          {/* "Close" while a request runs: closing does not stop it, its
              result then arrives as a notification. */}
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            <StackedLabel labels={{ cancel: "Cancel", close: "Close" }} active={pending ? "close" : "cancel"} />
          </Button>
          <PendingButton
            variant="outline"
            pending={pending && step === "preview"}
            pendingLabel="Previewing…"
            disabled={!canPreview || (pending && step !== "preview")}
            aria-describedby={
              error !== null && !pending && step === "preview" ? errorId : undefined
            }
            onClick={runPreview}
          >
            Preview
          </PendingButton>
          <PendingButton
            ref={addRef}
            pending={pending && step === "add"}
            pendingLabel="Adding…"
            disabled={!shown || (pending && step !== "add")}
            aria-describedby={error !== null && !pending && step === "add" ? errorId : undefined}
            onClick={runAdd}
          >
            Add
          </PendingButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
