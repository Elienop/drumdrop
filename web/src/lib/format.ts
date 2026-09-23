export function formatBytes(n: number | null | undefined): string {
  if (n == null) return "—"
  if (n === 0) return "0 B"
  const units = ["B", "KB", "MB", "GB", "TB"]
  const i = Math.min(Math.floor(Math.log(n) / Math.log(1024)), units.length - 1)
  const v = n / Math.pow(1024, i)
  return `${i === 0 ? v : v.toFixed(1)} ${units[i]}`
}

export function formatRelativeTime(iso: string | null | undefined, now: Date = new Date()): string {
  if (!iso) return "—"
  const then = new Date(iso).getTime()
  const secs = Math.round((now.getTime() - then) / 1000)
  if (secs < 10) return "just now"
  if (secs < 60) return `${secs}s ago`
  const mins = Math.floor(secs / 60)
  if (mins < 60) return `${mins}m ago`
  const hrs = Math.floor(mins / 60)
  if (hrs < 24) return `${hrs}h ago`
  const days = Math.floor(hrs / 24)
  return `${days}d ago`
}

export function formatDuration(seconds: number): string {
  if (seconds < 60) return `${seconds}s`
  const m = Math.floor(seconds / 60)
  const s = seconds % 60
  return `${m}m ${s}s`
}

// Musora's own names for its brands. The server sends a brand as the
// lower-case value the Brand field takes ("pianote"); every place the UI shows
// one reads it through brandName, so the Add follow preview and the Follows
// and Lessons tables all say "Pianote". A brand not listed here (playbass,
// whose casing is unconfirmed) shows as the server sent it, not as a guessed
// capitalisation.
const BRAND_NAMES: Readonly<Record<string, string>> = {
  drumeo: "Drumeo",
  pianote: "Pianote",
  guitareo: "Guitareo",
  singeo: "Singeo",
}

export function brandName(brand: string): string {
  return Object.hasOwn(BRAND_NAMES, brand) ? BRAND_NAMES[brand] : brand
}
