-- 0014: 帖子。两类：求助贴（必须挂在一道题上）与社区贴（不挂题，由 /topics 统一管理）。
-- 与「动态」分开存：动态是一条短句的时间线，帖子有标题、正文与评论，两者的读法不一样。
CREATE TABLE topics (
    id         INTEGER PRIMARY KEY,
    kind       TEXT    NOT NULL CHECK (kind IN ('help','community')),
    title      TEXT    NOT NULL,
    body       TEXT    NOT NULL DEFAULT '',
    author_id  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    problem_id INTEGER REFERENCES problems(id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    -- 求助贴必须挂题，社区贴必须不挂题：这条不变量交给数据库，而不是每个调用方各自记得查。
    CHECK ((kind = 'help') = (problem_id IS NOT NULL))
);
CREATE INDEX idx_topics_recent ON topics(id DESC);
CREATE INDEX idx_topics_author ON topics(author_id, id DESC);
CREATE INDEX idx_topics_problem ON topics(problem_id, id DESC);
CREATE INDEX idx_topics_kind ON topics(kind, id DESC);

-- 评论。删帖连带删评论，删号连带删评论：两张表都走 CASCADE，不留孤儿。
CREATE TABLE topic_comments (
    id         INTEGER PRIMARY KEY,
    topic_id   INTEGER NOT NULL REFERENCES topics(id) ON DELETE CASCADE,
    author_id  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    body       TEXT    NOT NULL,
    created_at INTEGER NOT NULL
);
CREATE INDEX idx_topic_comments_topic ON topic_comments(topic_id, id);

-- 删除他人帖子与评论是一项单独的能力：admin 层级默认有，普通人只能删自己的。
INSERT INTO permissions (key, label, staff) VALUES
    ('topic.moderate', '删除他人帖子与评论', 1);
INSERT INTO role_permissions (role, key) VALUES ('admin', 'topic.moderate');
