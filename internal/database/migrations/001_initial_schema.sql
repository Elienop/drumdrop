-- 001_initial_schema.sql — drumdrop persistence + follows.
--
-- Three tables: follows (one row per followed node or instructor), lessons
-- (one row per lesson; railcontent_id is the natural dedup key), and jobs
-- (the download queue / history the scheduler consumes in a later phase).
--
-- Plain CREATE TABLE / CREATE INDEX (no IF NOT EXISTS): the migration runner
-- guarantees once-only application per version.

-- follows: one row per followed node or instructor.
CREATE TABLE follows (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    kind            TEXT NOT NULL CHECK(kind IN ('node','instructor')),
    railcontent_id  INTEGER,            -- set when kind='node'
    slug            TEXT,               -- set when kind='instructor'
    title           TEXT NOT NULL DEFAULT '',
    brand           TEXT NOT NULL DEFAULT 'drumeo',
    quality         TEXT NOT NULL DEFAULT 'best',
    added_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_synced_at  DATETIME
);
CREATE UNIQUE INDEX idx_follows_node       ON follows(railcontent_id) WHERE kind='node';
CREATE UNIQUE INDEX idx_follows_instructor ON follows(slug)           WHERE kind='instructor';

-- lessons: one row per lesson; railcontent_id is the natural dedup key.
CREATE TABLE lessons (
    railcontent_id        INTEGER PRIMARY KEY,
    title                 TEXT NOT NULL DEFAULT '',
    parent_railcontent_id INTEGER,
    brand                 TEXT NOT NULL DEFAULT 'drumeo',
    status                TEXT NOT NULL DEFAULT 'pending'
                          CHECK(status IN ('pending','downloading','downloaded','failed','skipped')),
    quality               TEXT,
    output_dir            TEXT,
    video_path            TEXT,
    bytes                 INTEGER,
    error                 TEXT,
    first_seen_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    downloaded_at         DATETIME,
    updated_at            DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_lessons_status ON lessons(status);
CREATE INDEX idx_lessons_parent ON lessons(parent_railcontent_id);

-- jobs: download queue / history (scheduler consumes these next phase).
CREATE TABLE jobs (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    follow_id       INTEGER REFERENCES follows(id) ON DELETE SET NULL,
    railcontent_id  INTEGER NOT NULL,
    status          TEXT NOT NULL DEFAULT 'queued'
                    CHECK(status IN ('queued','running','done','failed','canceled')),
    attempts        INTEGER NOT NULL DEFAULT 0,
    error           TEXT,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at      DATETIME,
    finished_at     DATETIME
);
CREATE INDEX idx_jobs_status ON jobs(status);
