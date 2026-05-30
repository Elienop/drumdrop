export const qk = {
  summary: ["summary"] as const,
  follows: ["follows"] as const,
  lessons: (params?: Record<string, unknown>) => ["lessons", params ?? {}] as const,
  jobs: (params?: Record<string, unknown>) => ["jobs", params ?? {}] as const,
  session: ["session"] as const,
  health: ["health"] as const,
}
