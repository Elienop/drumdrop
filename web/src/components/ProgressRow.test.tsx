import { render, screen } from "@testing-library/react"
import { ProgressRow } from "./ProgressRow"

it("shows only the downloaded size when the total is unknown (0)", () => {
  render(
    <ProgressRow
      title="Lesson A"
      pct={10}
      bytes={120 * 1024 * 1024}
      totalBytes={0}
      speed="512KiB/s"
    />,
  )
  expect(screen.getByText("120.0 MB")).toBeInTheDocument() // no "/ 0 B"
  expect(screen.queryByText(/0 B/)).toBeNull()
  expect(screen.getByText("512KiB/s")).toBeInTheDocument()
})

it("shows downloaded / total when the total is known", () => {
  render(
    <ProgressRow
      title="Lesson B"
      pct={50}
      bytes={500 * 1024 * 1024}
      totalBytes={1024 * 1024 * 1024}
    />,
  )
  expect(screen.getByText("500.0 MB / 1.0 GB")).toBeInTheDocument()
})
