import { render, screen } from "@testing-library/react"
import { MemoryRouter } from "react-router-dom"
import { Sidebar } from "./Sidebar"

it("marks the current route's nav link active", () => {
  render(
    <MemoryRouter initialEntries={["/lessons"]}>
      <Sidebar />
    </MemoryRouter>,
  )
  const lessons = screen.getByRole("link", { name: /lessons/i })
  expect(lessons.className).toContain("bg-primary")
  const dashboard = screen.getByRole("link", { name: /dashboard/i })
  expect(dashboard.className).not.toContain("bg-primary")
})
