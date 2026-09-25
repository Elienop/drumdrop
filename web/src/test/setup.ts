// vitest's setup file: it runs before every test file. Only imports, in this
// order: vitest leaves setup files out of coverage, so anything that runs
// lives in a module imported here, where coverage sees it run.
import "@testing-library/jest-dom/vitest"
// Browser APIs jsdom lacks, stubbed before any component module loads.
import "./jsdom-shims"
// The shared MSW server, started and stopped around each test file.
import "./msw"
