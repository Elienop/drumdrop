// Browser APIs that jsdom (or Node under it) lacks, stubbed so the components
// can mount in tests. setup.ts imports this module before every test file.
// It lives apart from setup.ts because vitest leaves its setupFiles out of
// coverage: here the stubs show as run, and they are, by every test.
//
// No imports, on purpose: vitest 2's v8 coverage reports line 1 of a module
// that imports anything as never run, a false miss on every such file.

// Radix Select (and other Radix popper components) call PointerEvent capture
// and scrollIntoView APIs that jsdom does not implement; stub them so the
// components can mount and open in tests.
if (typeof Element !== "undefined") {
  if (!Element.prototype.hasPointerCapture) {
    Element.prototype.hasPointerCapture = () => false
  }
  if (!Element.prototype.setPointerCapture) {
    Element.prototype.setPointerCapture = () => {}
  }
  if (!Element.prototype.releasePointerCapture) {
    Element.prototype.releasePointerCapture = () => {}
  }
  if (!Element.prototype.scrollIntoView) {
    Element.prototype.scrollIntoView = () => {}
  }
}

// An open Radix popper (a Tooltip's arrow, a Checkbox inside a form) measures
// itself with ResizeObserver, which jsdom does not have. jsdom lays nothing
// out, so there is never a size change to report.
globalThis.ResizeObserver ??= class implements ResizeObserver {
  observe(): void {
    // Nothing to watch without layout.
  }
  unobserve(): void {
    // Nothing is watched.
  }
  disconnect(): void {
    // Nothing is watched.
  }
}

// Node 26 exposes an experimental global `localStorage` that is `undefined`
// (no `--localstorage-file`), shadowing jsdom's implementation. Install a
// minimal in-memory Storage so `localStorage`/`window.localStorage` work.
// (`== null`: undefined or null, as the typeof check before it read.)
if (globalThis.localStorage == null) {
  class MemoryStorage implements Storage {
    private readonly store = new Map<string, string>()
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
