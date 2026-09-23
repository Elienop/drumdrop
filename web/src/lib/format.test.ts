import { brandName, formatBytes, formatRelativeTime, formatDuration } from "./format"

describe("formatBytes", () => {
  it("handles null and zero", () => {
    expect(formatBytes(null)).toBe("—")
    expect(formatBytes(0)).toBe("0 B")
  })
  it("scales units", () => {
    expect(formatBytes(1024)).toBe("1.0 KB")
    expect(formatBytes(1536)).toBe("1.5 KB")
    expect(formatBytes(1048576)).toBe("1.0 MB")
    expect(formatBytes(5_368_709_120)).toBe("5.0 GB")
  })
})

describe("formatRelativeTime", () => {
  it("returns em dash for null", () => {
    expect(formatRelativeTime(null)).toBe("—")
  })
  it("renders 'just now' for very recent", () => {
    const now = new Date()
    expect(formatRelativeTime(now.toISOString(), now)).toBe("just now")
  })
  it("renders minutes/hours ago", () => {
    const now = new Date("2026-05-30T12:00:00Z")
    expect(formatRelativeTime("2026-05-30T11:58:00Z", now)).toBe("2m ago")
    expect(formatRelativeTime("2026-05-30T09:00:00Z", now)).toBe("3h ago")
  })
})

describe("formatDuration", () => {
  it("formats seconds and minutes", () => {
    expect(formatDuration(45)).toBe("45s")
    expect(formatDuration(125)).toBe("2m 5s")
  })
})

describe("brandName", () => {
  it("names Musora's brands as Musora does", () => {
    expect(brandName("drumeo")).toBe("Drumeo")
    expect(brandName("pianote")).toBe("Pianote")
    expect(brandName("guitareo")).toBe("Guitareo")
    expect(brandName("singeo")).toBe("Singeo")
  })
  it("shows any other brand as sent, playbass included (its casing is unconfirmed)", () => {
    expect(brandName("playbass")).toBe("playbass")
    expect(brandName("")).toBe("")
    // Only the lookup's own entries count: `names[brand] ?? brand` would
    // show Object's own function for these.
    expect(brandName("constructor")).toBe("constructor")
    expect(brandName("toString")).toBe("toString")
  })
})
