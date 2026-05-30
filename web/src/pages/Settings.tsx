import * as React from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { api, ApiHttpError } from "@/lib/api"
import { qk } from "@/lib/queryKeys"
import { clearToken, getToken, setToken } from "@/lib/auth"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"

// Settings has three cards: the Musora account connection (session pill + a
// local connect form), the API token (stored in localStorage, shared with the
// 401 modal), and an About card showing the running server version.
export function Settings() {
  return (
    <div className="flex flex-col gap-6">
      <h1 className="text-2xl font-bold">Settings</h1>
      <MusoraCard />
      <TokenCard />
      <AboutCard />
    </div>
  )
}

function MusoraCard() {
  const qc = useQueryClient()
  const session = useQuery({ queryKey: qk.session, queryFn: api.getSession })
  const [email, setEmail] = React.useState("")
  const [password, setPassword] = React.useState("")

  const connect = useMutation({
    mutationFn: () => api.login({ email, password }),
    onSuccess: () => {
      toast.success("Connected")
      setPassword("")
      qc.invalidateQueries({ queryKey: qk.session })
    },
    onError: (err) => {
      // 401 → bad Musora credentials; 400 → malformed. Both surface a generic
      // "login failed"; ApiHttpError carries a clean server message we append.
      const detail = err instanceof ApiHttpError && err.message ? `: ${err.message}` : ""
      toast.error(`login failed${detail}`)
    },
  })

  const connected = session.data?.connected === true

  return (
    <Card>
      <CardHeader>
        <div className="flex items-center justify-between gap-4">
          <div className="flex flex-col gap-1.5">
            <CardTitle>Musora connection</CardTitle>
            <CardDescription>
              Posted to your local drumdrop server; credentials never leave this machine.
            </CardDescription>
          </div>
          {connected ? (
            <Badge className="bg-emerald-600 text-white">Connected</Badge>
          ) : (
            <Badge variant="secondary">Disconnected</Badge>
          )}
        </div>
      </CardHeader>
      <CardContent>
        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault()
            connect.mutate()
          }}
        >
          <div className="flex flex-col gap-2">
            <Label htmlFor="settings-email">Email</Label>
            <Input
              id="settings-email"
              type="email"
              autoComplete="username"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
            />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="settings-password">Password</Label>
            <Input
              id="settings-password"
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </div>
          <div>
            <Button
              type="submit"
              disabled={connect.isPending || email.trim() === "" || password === ""}
            >
              Connect
            </Button>
          </div>
        </form>
      </CardContent>
    </Card>
  )
}

function TokenCard() {
  const [value, setValue] = React.useState("")
  const [stored, setStored] = React.useState(() => getToken() !== null)

  const save = () => {
    if (value.trim() === "") return
    setToken(value.trim())
    setStored(true)
    setValue("")
    toast.success("Token saved")
  }

  const clear = () => {
    clearToken()
    setStored(false)
    setValue("")
    toast.message("Token cleared")
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>API token</CardTitle>
        <CardDescription>
          {stored
            ? "A token is stored. Paste a new value to replace it."
            : "No token stored. Paste the server's bearer token to authenticate."}{" "}
          This is the same token the re-auth modal uses.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-2">
            <Label htmlFor="settings-token">Token</Label>
            <Input
              id="settings-token"
              type="password"
              placeholder="paste token"
              value={value}
              onChange={(e) => setValue(e.target.value)}
            />
          </div>
          <div className="flex gap-2">
            <Button onClick={save} disabled={value.trim() === ""}>
              Save
            </Button>
            <Button variant="outline" onClick={clear} disabled={!stored}>
              Clear
            </Button>
          </div>
        </div>
      </CardContent>
    </Card>
  )
}

function AboutCard() {
  const health = useQuery({ queryKey: qk.health, queryFn: api.health })

  return (
    <Card>
      <CardHeader>
        <CardTitle>About</CardTitle>
        <CardDescription>The drumdrop server you're connected to.</CardDescription>
      </CardHeader>
      <CardContent>
        <div className="flex items-center justify-between gap-4 text-sm">
          <span className="text-muted-foreground">Version</span>
          <span className="font-medium">{health.data?.version ?? "—"}</span>
        </div>
      </CardContent>
    </Card>
  )
}
