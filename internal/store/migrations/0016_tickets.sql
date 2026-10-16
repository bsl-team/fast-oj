-- 0016: 工单系统。普通用户提交，管理员处理。
--
-- 为什么不复用 topics：帖子是公开的，工单不是。判题申诉、举报、账号问题这些内容
-- 只该让作者和处理它的人看到，所以另起一张表，权限单独一个键。
--
-- 状态变化写成 ticket_replies 里的一行（event 非空），而不是只改一个字段：
-- "谁在什么时候把它从待处理转到处理中"本身就是这单的一部分，页面上一眼能看完整个经过。

CREATE TABLE tickets (
    id            INTEGER PRIMARY KEY,
    kind          TEXT    NOT NULL CHECK (kind IN ('appeal','bug','report','account','other')),
    title         TEXT    NOT NULL,
    body          TEXT    NOT NULL DEFAULT '',
    author_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- 可以挂一道题或一次提交（判题申诉最常用的就是"这道题这个提交"）。
    -- 题目或提交被删时工单要留着：申诉过什么这件事本身是历史。
    problem_id    INTEGER REFERENCES problems(id)    ON DELETE SET NULL,
    submission_id INTEGER REFERENCES submissions(id) ON DELETE SET NULL,
    status        TEXT    NOT NULL DEFAULT 'open' CHECK (status IN ('open','doing','done','closed')),
    -- 谁在办这单。转交时改这个字段，同时留一行 event。
    handler_id    INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);

CREATE INDEX idx_tickets_author ON tickets(author_id, id DESC);
-- 管理员的默认视图是"还没办完的"，这两条索引就是为它建的。
CREATE INDEX idx_tickets_status ON tickets(status, id DESC);
CREATE INDEX idx_tickets_open   ON tickets(id DESC) WHERE status IN ('open','doing');

CREATE TABLE ticket_replies (
    id         INTEGER PRIMARY KEY,
    ticket_id  INTEGER NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
    author_id  INTEGER NOT NULL REFERENCES users(id)   ON DELETE CASCADE,
    body       TEXT    NOT NULL DEFAULT '',
    -- event 非空时这是一条系统流水（改状态/转交），body 留空；普通回复 event 为空。
    event      TEXT    NOT NULL DEFAULT '' CHECK (event IN ('','status','assign')),
    created_at INTEGER NOT NULL,
    -- 流水行必须有 event 文本，人写的回复必须有正文：别留两种都空的行。
    CHECK (body <> '' OR event <> '')
);

CREATE INDEX idx_ticket_replies ON ticket_replies(ticket_id, id);

-- 处理工单是一种能力，不是身份：只给 admin 打底，别人按需在权限控制台加。
INSERT INTO permissions (key, label, staff)
    VALUES ('ticket.handle', '处理工单（查看全部、回复、改状态、关单）', 1);
INSERT INTO role_permissions (role, key) VALUES ('admin', 'ticket.handle');
