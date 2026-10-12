-- 0010：让"边判边回报"可重复。
--
-- 判机每完成一个用例就写一行，网络重试或重复回报必须落在同一行上而不是攒出两条，
-- 所以 (submission_id, ordinal) 从普通索引升成唯一索引，写入侧才能用 UPSERT。

DROP INDEX IF EXISTS idx_runs_submission;
CREATE UNIQUE INDEX idx_runs_submission ON runs(submission_id, ordinal);
