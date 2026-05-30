import { BrowserRouter } from "react-router-dom"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { Toaster } from "@/components/ui/sonner"
import { SSEProvider } from "@/lib/sse"
import { Sidebar } from "@/components/app-shell/Sidebar"
import { TopBar } from "@/components/app-shell/TopBar"
import { AppRoutes } from "@/routes"
import { TokenGate } from "@/components/TokenGate"

const qc = new QueryClient({ defaultOptions: { queries: { staleTime: 5_000, retry: 1 } } })

export default function App() {
  return (
    <QueryClientProvider client={qc}>
      <SSEProvider>
        <BrowserRouter>
          <div className="flex h-screen overflow-hidden">
            <Sidebar />
            <div className="flex flex-1 flex-col overflow-hidden">
              <TopBar />
              <main className="flex-1 overflow-auto p-6">
                <AppRoutes />
              </main>
            </div>
          </div>
          <TokenGate />
          <Toaster richColors theme="dark" />
        </BrowserRouter>
      </SSEProvider>
    </QueryClientProvider>
  )
}
