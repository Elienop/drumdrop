import { defineConfig } from "vitest/config"
import react from "@vitejs/plugin-react"
import tailwindcss from "@tailwindcss/vite"
import path from "node:path"

const API = "http://127.0.0.1:8080" // matches `drumdrop serve` default listen

// SonarQube's scanner runs from the repo root, so every path in a report it
// reads must be relative to it (`web/src/...`), not to web/.
const repoRoot = path.resolve(__dirname, "..")

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: { alias: { "@": path.resolve(__dirname, "src") } },
  server: {
    proxy: {
      "/api": { target: API, changeOrigin: true },
      "/healthz": { target: API, changeOrigin: true },
      "/readyz": { target: API, changeOrigin: true },
    },
  },
  build: { outDir: "dist", emptyOutDir: true },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test/setup.ts"],
    css: false,
    passWithNoTests: true,
    // Every run also writes SonarQube's Generic Test Execution XML (the test
    // count, pass/fail and duration). Setting `reporters` replaces vitest's
    // defaults, so the first entries re-create them as vitest 2.1 picks them:
    // `default`, plus `github-actions` in CI for PR annotations.
    reporters: [
      "default",
      ...(process.env.GITHUB_ACTIONS === "true" ? ["github-actions"] : []),
      [
        "vitest-sonar-reporter",
        {
          outputFile: "coverage/sonar-report.xml",
          silent: true,
          // The reporter hands over cwd-relative paths; make them repo-root
          // relative whatever the cwd.
          onWritePath: (p: string) => path.relative(repoRoot, path.resolve(p)),
        },
      ],
    ],
    // `vitest run --coverage` (`make coverage` at the repo root) writes
    // coverage/lcov.info for SonarQube. Every application file counts, tested
    // or not; vitest's default excludes drop the test files themselves.
    coverage: {
      provider: "v8",
      include: ["src/**/*.{ts,tsx}"],
      // `lcovonly`, not `lcov`, which also writes an HTML report.
      reporter: ["text-summary", ["lcovonly", { projectRoot: repoRoot }]],
    },
  },
})
