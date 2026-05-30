import "@testing-library/jest-dom/vitest"

// Node 26 exposes an experimental global `localStorage` that is `undefined`
// (no `--localstorage-file`), shadowing jsdom's implementation. Install a
// minimal in-memory Storage so `localStorage`/`window.localStorage` work.
if (typeof globalThis.localStorage === "undefined" || globalThis.localStorage === null) {
  class MemoryStorage implements Storage {
    private store = new Map<string, string>()
    get length(): number {
      return this.store.size
    }
    clear(): void {
      this.store.clear()
    }
    getItem(key: string): string | null {
      return this.store.has(key) ? this.store.get(key)! : null
    }
    key(index: number): string | null {
      return Array.from(this.store.keys())[index] ?? null
    }
    removeItem(key: string): void {
      this.store.delete(key)
    }
    setItem(key: string, value: string): void {
      this.store.set(key, String(value))
    }
  }
  const storage = new MemoryStorage()
  Object.defineProperty(globalThis, "localStorage", {
    value: storage,
    configurable: true,
    writable: true,
  })
  if (typeof window !== "undefined") {
    Object.defineProperty(window, "localStorage", {
      value: storage,
      configurable: true,
      writable: true,
    })
  }
}
