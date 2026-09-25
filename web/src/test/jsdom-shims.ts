// Browser APIs that jsdom (or Node under it) lacks, stubbed so the components
// can mount in tests. setup.ts imports this module before every test file.
// It lives apart from setup.ts because vitest leaves its setupFiles out of
// coverage: here the stubs show as run, and they are, by every test.
//
// No imports, on purpose: vitest 2's v8 coverage reports line 1 of a module
// that imports anything as never run, a false miss on every such file.

// Every stub installs unconditionally, never "only if missing": tests then run
// against the same stand-ins on every Node and jsdom version, and a guard
// whose outcome depends on the machine would leave its other side unrun.
// vitest's jsdom environment makes `window` the global object, so installing
// on globalThis installs on window too.

// Radix Select (and other Radix popper components) call PointerEvent capture
// and scrollIntoView APIs that jsdom does not implement; stub them so the
// components can mount and open in tests.
Element.prototype.hasPointerCapture = () => false
Element.prototype.setPointerCapture = () => {}
Element.prototype.releasePointerCapture = () => {}
Element.prototype.scrollIntoView = () => {}

// An open Radix popper (a Tooltip's arrow, a Checkbox inside a form) measures
// itself with ResizeObserver, which jsdom does not have. jsdom lays nothing
// out, so there is never a size change to report.
globalThis.ResizeObserver = class implements ResizeObserver {
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

// An in-memory Storage for localStorage. Node 26 defines its own global
// `localStorage`, `undefined` without `--localstorage-file`, and it shadows
// jsdom's; older Nodes leave jsdom's in place. This one replaces either.
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
Object.defineProperty(globalThis, "localStorage", {
  value: new MemoryStorage(),
  configurable: true,
  writable: true,
})
