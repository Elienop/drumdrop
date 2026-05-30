-- 002_lessons_follow_id.sql — link each lesson to the follow it was discovered
-- under.
--
-- follow_id is nullable (a lesson may predate this column, or its follow may be
-- removed) and ON DELETE SET NULL so deleting a follow detaches its lessons
-- rather than cascading them away. The Planner records the first follow that
-- discovers a lesson; UpsertLesson's ON CONFLICT deliberately does NOT touch
-- follow_id, so this is first-follow-wins.
--
-- Plain ALTER/CREATE INDEX (no IF NOT EXISTS): the migration runner guarantees
-- once-only application per version.
ALTER TABLE lessons ADD COLUMN follow_id INTEGER REFERENCES follows(id) ON DELETE SET NULL;
CREATE INDEX idx_lessons_follow_id ON lessons(follow_id);
