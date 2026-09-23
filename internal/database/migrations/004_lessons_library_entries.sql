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

-- 1 while a delete is removing the lesson's files (BeginLessonDelete /
-- BeginFollowDelete until the delete ends): no job may be enqueued or retried
-- for it meanwhile, so no download can record files the delete is removing.
-- The daemon clears any left set at startup (a process that died mid-delete).
ALTER TABLE lessons ADD COLUMN deleting INTEGER NOT NULL DEFAULT 0;

-- What a delete wanted for each job it removed while a worker may still hold
-- it (running, or canceled but not yet finished): discard = 1 when the delete
-- removes the lesson's files, so what that download wrote must go too; 0 when
-- it keeps them (a follow removed without its files), so nothing is removed.
-- The worker reads it when its next write finds the job gone. Job ids are
-- never reused (AUTOINCREMENT), so a row can never answer for another job.
CREATE TABLE abandoned_jobs (
    job_id  INTEGER PRIMARY KEY,
    discard INTEGER NOT NULL CHECK(discard IN (0, 1))
);
