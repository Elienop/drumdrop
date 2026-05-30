import { render, screen } from "@testing-library/react"
import { StatusBadge } from "./StatusBadge"

it("renders each status with its label", () => {
  for (const s of ["downloaded", "downloading", "pending", "failed", "skipped"] as const) {
    const { unmount } = render(<StatusBadge status={s} />)
    expect(screen.getByText(s)).toBeInTheDocument()
    unmount()
  }
})
