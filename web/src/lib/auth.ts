export const TOKEN_KEY = "drumdrop_api_token"

type Listener = (token: string | null) => void
const listeners = new Set<Listener>()

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
}

export function subscribe(l: Listener): () => void {
  listeners.add(l)
  return () => listeners.delete(l)
}
