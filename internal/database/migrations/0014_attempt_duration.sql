-- 0014_attempt_duration: store time-taken on a test attempt.
-- Client reports actual seconds spent; nullable for in-progress / legacy rows.

ALTER TABLE test_attempts
  ADD COLUMN IF NOT EXISTS duration_seconds INT
  CHECK (duration_seconds IS NULL OR duration_seconds >= 0);
