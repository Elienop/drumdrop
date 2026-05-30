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

export function clearToken(): void {
  localStorage.removeItem(TOKEN_KEY)
  listeners.forEach((l) => l(null))
  onAuthRequired?.()
}

export function subscribe(l: Listener): () => void {
  listeners.add(l)
  return () => listeners.delete(l)
}
