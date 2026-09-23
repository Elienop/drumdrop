-- What a lesson owns inside shared plex-tv season folders: a JSON array of the
-- entries the library move placed there for it (every version file, sidecar and
-- subfolder), each relative to the library folder, "<show>/Season NN/<name>",
-- so the record stays true when the library moves or is spelled another way. A
-- delete and a re-download act on exactly these, so two lessons whose names
-- look alike can never take each other's files.
-- Nullable: NULL means "no record" (a default-layout lesson, or a plex-tv lesson
-- moved before this column existed, which falls back to name matching); '[]'
-- means a record that holds nothing.
ALTER TABLE lessons ADD COLUMN library_entries TEXT;

-- Until when a delete holds the lesson's files (a lease): while it is in the
-- future, a delete is removing them, and no job may be enqueued or retried for
-- the lesson, so no download can record files the delete is removing. NULL (or
-- a time already past) means no delete holds it. The delete renews the lease
-- while it runs and clears it when it ends; one that died mid-way (a crash)
-- lets it lapse on its own, so no startup sweep is needed, and no process can
-- clear a mark another process's live delete holds.
ALTER TABLE lessons ADD COLUMN deleting_until DATETIME;

-- What a stopper wanted for each job it removed while a worker may still hold
-- it (running, or canceled after it started, its worker not yet finished):
--   keep    - a follow removed without its files: nothing is removed;
--   discard - a lesson skipped: what that download wrote goes, except what any
--             lesson row records (the skipped lesson's own earlier files stay);
--   delete  - the lesson's files are deleted: what that download wrote goes,
--             except what ANOTHER lesson row records.
-- The worker reads its row when its next write finds the job gone, and the row
-- is removed then; a row nobody reads is removed after 7 days (see
-- removeActiveJobsTx). A row answers only for its own job AND lesson. Job ids
-- come from AUTOINCREMENT and are never reused; a migration that ever rebuilds
-- the jobs table must empty this one in the same step.
CREATE TABLE abandoned_jobs (
    job_id         INTEGER PRIMARY KEY,
    railcontent_id INTEGER NOT NULL,
    intent         TEXT NOT NULL CHECK(intent IN ('keep', 'discard', 'delete')),
    recorded_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
