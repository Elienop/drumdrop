import { useEffect, useState } from "react"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { setToken, setAuthRequiredHandler } from "@/lib/auth"

// TokenGate opens whenever the api client hits a 401 — the client calls
// clearToken(), which fires the auth-required handler this component registers.
// On save it stores the token and re-bootstraps (default onSaved reloads) so the
// data fetches AND the SSE EventSource re-establish with the new credential.
export function TokenGate(
  { onSaved = () => window.location.reload() }: { onSaved?: () => void } = {},
) {
  const [open, setOpen] = useState(false)
  const [value, setValue] = useState("")
  useEffect(() => {
    setAuthRequiredHandler(() => setOpen(true))
    return () => setAuthRequiredHandler(null)
  }, [])
  // save runs on either path — Enter (form submit) or clicking Save — so the
  // two stay in lockstep. trim() also guards an Enter on whitespace-only input.
  const save = () => {
    const token = value.trim()
    if (!token) return
    setToken(token)
    setOpen(false)
    onSaved()
  }
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>API token required</DialogTitle>
          <DialogDescription>This drumdrop server is protected by a token. Paste it to continue; it's stored only in this browser.</DialogDescription>
        </DialogHeader>
        <form className="flex flex-col gap-4" onSubmit={(e) => { e.preventDefault(); save() }}>
          <div className="flex flex-col gap-2">
            <Label htmlFor="token">Access token</Label>
            <Input id="token" type="password" autoFocus value={value} onChange={(e) => setValue(e.target.value)} />
          </div>
          <Button type="submit" disabled={value.trim() === ""}>Save</Button>
        </form>
      </DialogContent>
    </Dialog>
  )
}
