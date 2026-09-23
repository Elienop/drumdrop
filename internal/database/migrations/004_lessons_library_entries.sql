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
