export const TOKEN_KEY = "drumdrop_api_token"

type Listener = (token: string | null) => void
const listeners = new Set<Listener>()

let onAuthRequired: (() => void) | null = null
export function setAuthRequiredHandler(fn: (() => void) | null) {
  onAuthRequired = fn
}

export function getToken(): string | null {
  return localStorage.getItem(TOKEN_KEY)
}

export function setToken(token: string): void {
  localStorage.setItem(TOKEN_KEY, token)
  listeners.forEach((l) => l(token))
}

// clearToken removes the stored token and notifies subscribers. By default it
// also fires the auth-required handler (the 401 path needs the gate to open);
// pass { silent: true } for a deliberate Settings "Clear" that should not
// re-open the gate.
export function clearToken(opts?: { silent?: boolean }): void {
  localStorage.removeItem(TOKEN_KEY)
  listeners.forEach((l) => l(null))
  if (!opts?.silent) onAuthRequired?.()
}

export function subscribe(l: Listener): () => void {
  listeners.add(l)
  return () => listeners.delete(l)
}
