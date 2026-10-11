-- 0005: the feed (动态). Text-only posts, Markdown rendered like statements.

CREATE TABLE posts (
    id         INTEGER PRIMARY KEY,
    author_id  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    body       TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    -- Hidden posts stay in the table so a moderator's decision is reversible
    -- and the author can still see what was taken down.
    visible    INTEGER NOT NULL DEFAULT 1,
    hidden_by  INTEGER REFERENCES users(id) ON DELETE SET NULL,
    hidden_at  INTEGER
);
CREATE INDEX idx_posts_recent ON posts(visible, id DESC);
CREATE INDEX idx_posts_author ON posts(author_id, id DESC);
