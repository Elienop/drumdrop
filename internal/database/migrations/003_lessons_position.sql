-- Per-follow lesson sequence, used for the "NN - " download folder prefix.
-- Nullable: a position-less upsert (or a pre-existing row) sorts last/unnumbered.
ALTER TABLE lessons ADD COLUMN position INTEGER;
