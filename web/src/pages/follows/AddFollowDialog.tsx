import * as React from "react"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { api, ApiHttpError } from "@/lib/api"
import { qk } from "@/lib/queryKeys"
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

type Kind = "node" | "instructor"

// Quality presets passed through to the follow; "best" lets yt-dlp pick the
// highest available, the rest cap the video height (e.g. 1080 → ≤1080p).
const QUALITY_OPTIONS = ["best", "2160", "1440", "1080", "720", "480"] as const

// AddFollowDialog is the preview-then-add flow: a segmented kind control
// (node | instructor), an input (URL-or-id for node, slug + optional brand for
// instructor), a Preview button that fetches the title + lesson_count, and an
// Add button that registers the follow. createFollow resolves to { status,
// data }: 201 → newly created ("Following …"), 200 → already following.
export function AddFollowDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const qc = useQueryClient()
  const [kind, setKind] = React.useState<Kind>("node")
  const [id, setId] = React.useState("")
  const [slug, setSlug] = React.useState("")
  const [brand, setBrand] = React.useState("")
  const [quality, setQuality] = React.useState("best")
  const [preview, setPreview] = React.useState<PreviewResponse | null>(null)

  // Reset the form whenever the dialog closes so a reopen starts clean.
  React.useEffect(() => {
    if (!open) {
      setKind("node")
      setId("")
      setSlug("")
      setBrand("")
      setQuality("best")
      setPreview(null)
    }
  }, [open])

  // Switching tabs invalidates any preview built for the other kind.
  const switchKind = (next: Kind) => {
    setKind(next)
    setPreview(null)
  }

  const previewMut = useMutation({
    mutationFn: () =>
      kind === "node"
        ? api.preview({ id })
        : api.preview({ slug, brand: brand || undefined }),
    onSuccess: (data) => setPreview(data),
    onError: (err) => {
      if (err instanceof ApiHttpError) toast.error(err.message)
    },
  })

  const addMut = useMutation({
    mutationFn: () => {
      const body: CreateFollowRequest =
        kind === "node"
          ? { kind: "node", id, quality }
          : { kind: "instructor", slug, brand: brand || undefined, quality }
      return api.createFollow(body)
    },
    onSuccess: ({ status, data }) => {
      if (status === 201) toast.success(`Following ${data.title}`)
      else toast.message(`Already following ${data.title}`)
      qc.invalidateQueries({ queryKey: qk.follows })
      qc.invalidateQueries({ queryKey: qk.summary })
      onOpenChange(false)
    },
    onError: (err) => {
      // 400 ("check the URL or slug") / 502 carry a clean server message; keep
      // the dialog open so the user can correct the input.
      if (err instanceof ApiHttpError) toast.error(err.message)
    },
  })

  const canPreview = kind === "node" ? id.trim() !== "" : slug.trim() !== ""

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add follow</DialogTitle>
          <DialogDescription>
            Preview a node or instructor, then add it to your follows.
          </DialogDescription>
        </DialogHeader>

        <Tabs value={kind} onValueChange={(v) => switchKind(v as Kind)}>
          <TabsList className="w-full">
            <TabsTrigger value="node">Node</TabsTrigger>
            <TabsTrigger value="instructor">Instructor</TabsTrigger>
          </TabsList>

          <TabsContent value="node" className="flex flex-col gap-2 pt-2">
            <Label htmlFor="follow-node-id">URL or id</Label>
            <Input
              id="follow-node-id"
              placeholder="https://drumeo.com/… or 12345"
              value={id}
              onChange={(e) => setId(e.target.value)}
            />
          </TabsContent>

          <TabsContent value="instructor" className="flex flex-col gap-2 pt-2">
            <Label htmlFor="follow-slug">Slug</Label>
            <Input
              id="follow-slug"
              placeholder="jared-falk"
              value={slug}
              onChange={(e) => setSlug(e.target.value)}
            />
            <Label htmlFor="follow-brand">Brand (optional)</Label>
            <Input
              id="follow-brand"
              placeholder="drumeo"
              value={brand}
              onChange={(e) => setBrand(e.target.value)}
            />
          </TabsContent>
        </Tabs>

        <div className="flex flex-col gap-2">
          <Label htmlFor="follow-quality">Quality</Label>
          <Select value={quality} onValueChange={setQuality}>
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

        {preview && (
          <div className="flex flex-col gap-1 rounded-md border bg-muted/40 p-3">
            <span className="font-medium">{preview.title}</span>
            <span className="text-sm text-muted-foreground">
              {preview.lesson_count} lessons
            </span>
          </div>
        )}

        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => previewMut.mutate()}
            disabled={!canPreview || previewMut.isPending}
          >
            Preview
          </Button>
          <Button
            onClick={() => addMut.mutate()}
            disabled={!preview || addMut.isPending}
          >
            Add
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
