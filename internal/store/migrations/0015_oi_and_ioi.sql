-- 0015: 把 OI 与 IOI 分开。
-- 0013 的 'oi' 其实写的是 IOI 的算法（每题取最高分、同分比用时）。真正的 OI（CSP-J/S、NOIP、NOI）
-- 是**最后一次提交生效、同分同名次、比赛中不出榜**，所以mode 要多一个取值 'ioi'。
-- SQLite 不能改已有列的 CHECK，只能按官方那套重建：建新表、搬数据、丢旧表、改名、再建索引。
-- 整个过程在 store.migrate 的一个事务里，中途失败会整体回滚。

CREATE TABLE contests_new (
    id            INTEGER PRIMARY KEY,
    cid           TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    title         TEXT    NOT NULL,
    statement     TEXT    NOT NULL DEFAULT '',
    start_at      INTEGER NOT NULL,
    end_at        INTEGER NOT NULL CHECK (end_at > start_at),
    mode          TEXT    NOT NULL DEFAULT 'oi' CHECK (mode IN ('oi','ioi','acm')),
    join_code     TEXT    NOT NULL DEFAULT '',
    standing_open INTEGER NOT NULL DEFAULT 0,
    visible       INTEGER NOT NULL DEFAULT 1,
    team_size     INTEGER NOT NULL DEFAULT 1 CHECK (team_size BETWEEN 1 AND 5),
    penalty_ms    INTEGER NOT NULL DEFAULT 1200000,
    owner_id      INTEGER REFERENCES users(id),
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);
CREATE TABLE contest_problems_new (
    contest_id  INTEGER NOT NULL REFERENCES contests_new(id) ON DELETE CASCADE,
    problem_id  INTEGER NOT NULL REFERENCES problems(id) ON DELETE CASCADE,
    ordinal     INTEGER NOT NULL,
    points      INTEGER NOT NULL DEFAULT 0 CHECK (points >= 0),
    label       TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (contest_id, problem_id)
);
CREATE TABLE contest_teams_new (
    id         INTEGER PRIMARY KEY,
    contest_id INTEGER NOT NULL REFERENCES contests_new(id) ON DELETE CASCADE,
    name       TEXT    NOT NULL COLLATE NOCASE,
    created_at INTEGER NOT NULL,
    UNIQUE (contest_id, name)
);
CREATE TABLE contest_members_new (
    contest_id INTEGER NOT NULL REFERENCES contests_new(id) ON DELETE CASCADE,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    team_id    INTEGER REFERENCES contest_teams_new(id) ON DELETE SET NULL,
    role       TEXT    NOT NULL DEFAULT 'member' CHECK (role IN ('owner','member')),
    joined_at  INTEGER NOT NULL,
    PRIMARY KEY (contest_id, user_id)
);

INSERT INTO contests_new          SELECT * FROM contests;
INSERT INTO contest_problems_new  SELECT * FROM contest_problems;
INSERT INTO contest_teams_new     SELECT * FROM contest_teams;
INSERT INTO contest_members_new   SELECT * FROM contest_members;

-- 先丢子表再丢父表：反过来会顺着 ON DELETE CASCADE 把刚搬好的数据删掉。
DROP TABLE contest_members;
DROP TABLE contest_teams;
DROP TABLE contest_problems;
DROP TABLE contests;

-- 父表先改名的：SQLite 会把它在其他表外键里的名字一起改掉。
ALTER TABLE contests_new RENAME TO contests;
ALTER TABLE contest_teams_new RENAME TO contest_teams;
ALTER TABLE contest_problems_new RENAME TO contest_problems;
ALTER TABLE contest_members_new RENAME TO contest_members;

CREATE INDEX idx_contests_start ON contests(start_at DESC, id DESC);
CREATE UNIQUE INDEX uq_contest_problem_ordinal ON contest_problems(contest_id, ordinal);
CREATE INDEX idx_members_contest ON contest_members(contest_id, team_id);
CREATE INDEX idx_members_user ON contest_members(user_id, contest_id);
