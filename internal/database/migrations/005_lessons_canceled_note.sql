-- A download that ended without recording anything used to leave the bare word
-- 'canceled' as the lesson's error, which the web UI shows under the lesson as
-- if it were a sentence. It now leaves a sentence (stoppedNote in
-- downloads.go); this gives the rows an older version left the same one. Only
-- a skipped lesson ever got that word from DrumDrop. A Skip reason someone
-- typed as exactly 'canceled' can't be told apart from it, and gets it too.
UPDATE lessons
   SET error = 'The download stopped before it finished. Download again to get this lesson.'
 WHERE status = 'skipped' AND error = 'canceled';
