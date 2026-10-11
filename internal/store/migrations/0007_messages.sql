-- 0007: 私信与会收信策略。
--
-- 会话没有独立的表：一条 (a,b) 的对话就是 sender/recipient 两个方向的消息集合，
-- 所以两个方向的索引都要有，否则 OR 查询只能全表扫。
-- read_at 为 NULL 表示未读，未读角标走部分索引，读过的消息不会让索引继续长大。

ALTER TABLE users ADD COLUMN dm_policy TEXT NOT NULL DEFAULT 'everyone'
    CHECK (dm_policy IN ('everyone', 'mutual', 'following', 'off'));

CREATE TABLE dms (
    id           INTEGER PRIMARY KEY,
    sender_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    recipient_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    body         TEXT    NOT NULL,
    created_at   INTEGER NOT NULL,
    read_at      INTEGER,
    CHECK (sender_id <> recipient_id)
);
CREATE INDEX idx_dms_thread_a ON dms(sender_id, recipient_id, id);
CREATE INDEX idx_dms_thread_b ON dms(recipient_id, sender_id, id);
CREATE INDEX idx_dms_unread ON dms(recipient_id, id) WHERE read_at IS NULL;
