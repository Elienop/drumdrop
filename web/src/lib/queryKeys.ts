export const qk = {
  summary: ["summary"] as const,
  follows: ["follows"] as const,
  follow: (id: number) => ["follows", id] as const,
  followLessons: (id: number) => ["follows", id, "lessons"] as const,
  lessons: (params?: Record<string, unknown>) => ["lessons", params ?? {}] as const,
  lesson: (id: number) => ["lessons", id] as const,
  jobs: (params?: Record<string, unknown>) => ["jobs", params ?? {}] as const,
  job: (id: number) => ["jobs", id] as const,
  session: ["session"] as const,
  health: ["health"] as const,
}
