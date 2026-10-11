-- 0006: 关注关系。主键服务"我关注了谁"，第二个索引服务"谁关注了我"。
-- 时间列叫 followed_at：叫 created_at 会和 users.created_at 在 JOIN 里撞成歧义列。
-- CHECK 挡住自己关注自己；级联删除让注销/清理账号时不留悬挂边。

CREATE TABLE follows (
    follower_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    followee_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    followed_at INTEGER NOT NULL,
    PRIMARY KEY (follower_id, followee_id),
    CHECK (follower_id <> followee_id)
);
CREATE INDEX idx_follows_followee ON follows(followee_id, follower_id);
