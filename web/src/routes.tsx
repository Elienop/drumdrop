import { Navigate, Route, Routes } from "react-router-dom"
import { Dashboard } from "@/pages/Dashboard"
import { Follows } from "@/pages/Follows"
import { Lessons } from "@/pages/Lessons"
import { Queue } from "@/pages/Queue"
import { Settings } from "@/pages/Settings"

export function AppRoutes() {
  return (
    <Routes>
      <Route path="/" element={<Dashboard />} />
      <Route path="/follows" element={<Follows />} />
      <Route path="/lessons" element={<Lessons />} />
      <Route path="/queue" element={<Queue />} />
      <Route path="/settings" element={<Settings />} />
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  )
}
