-- 0013: 比赛。
-- 计分不另起一套：每场比赛只记录「谁在什么时间对哪些题交了什么」，每题得分继续读 runs.score。
CREATE TABLE contests (
    id            INTEGER PRIMARY KEY,
    cid           TEXT    NOT NULL UNIQUE COLLATE NOCASE,   -- 人类可读编号，URL 用它：/contests/C0001
    title         TEXT    NOT NULL,
    statement     TEXT    NOT NULL DEFAULT '',              -- Markdown + KaTeX，与题面同一渲染管线
    start_at      INTEGER NOT NULL,
    end_at        INTEGER NOT NULL CHECK (end_at > start_at),
    mode          TEXT    NOT NULL DEFAULT 'oi' CHECK (mode IN ('oi','acm')),
    join_code     TEXT    NOT NULL DEFAULT '',              -- 空 = 公开赛，任何人免码加入
    standing_open INTEGER NOT NULL DEFAULT 0,               -- 0 封榜（只给自己看）/ 1 公示
    visible       INTEGER NOT NULL DEFAULT 1,               -- 是否出现在比赛列表
    team_size     INTEGER NOT NULL DEFAULT 1 CHECK (team_size BETWEEN 1 AND 5),
    penalty_ms    INTEGER NOT NULL DEFAULT 1200000,         -- ACM 罚时：每次未过尝试 20 分钟
    owner_id      INTEGER REFERENCES users(id),
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);
CREATE INDEX idx_contests_start ON contests(start_at DESC, id DESC);

-- 挂题：分值可以覆盖，0 表示沿用题目自身满分。
CREATE TABLE contest_problems (
    contest_id  INTEGER NOT NULL REFERENCES contests(id) ON DELETE CASCADE,
    problem_id  INTEGER NOT NULL REFERENCES problems(id) ON DELETE CASCADE,
    ordinal     INTEGER NOT NULL,                           -- 比赛内顺序（A/B/C…）
    points      INTEGER NOT NULL DEFAULT 0 CHECK (points >= 0),
    label       TEXT    NOT NULL DEFAULT '',                -- 「A」「B」；空则按 ordinal 生成
    PRIMARY KEY (contest_id, problem_id)
);
CREATE UNIQUE INDEX uq_contest_problem_ordinal ON contest_problems(contest_id, ordinal);

CREATE TABLE contest_teams (
    id         INTEGER PRIMARY KEY,
    contest_id INTEGER NOT NULL REFERENCES contests(id) ON DELETE CASCADE,
    name       TEXT    NOT NULL COLLATE NOCASE,
    created_at INTEGER NOT NULL,
    UNIQUE (contest_id, name)
);

-- team_id 用 NULL 而不是 0 表示「单打」：外键对 0 不会放行，读取一律 IFNULL(team_id, 0)，
-- 删队时由 ON DELETE SET NULL 自动把成员退回单打。
CREATE TABLE contest_members (
    contest_id INTEGER NOT NULL REFERENCES contests(id) ON DELETE CASCADE,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    team_id    INTEGER REFERENCES contest_teams(id) ON DELETE SET NULL,
    role       TEXT    NOT NULL DEFAULT 'member' CHECK (role IN ('owner','member')),
    joined_at  INTEGER NOT NULL,
    PRIMARY KEY (contest_id, user_id)
);
CREATE INDEX idx_members_contest ON contest_members(contest_id, team_id);
CREATE INDEX idx_members_user ON contest_members(user_id, contest_id);

-- 提交归属比赛；0 是平时练习。归属在创建时定下，之后退队、移题都不追溯。
ALTER TABLE submissions ADD COLUMN contest_id INTEGER NOT NULL DEFAULT 0;
CREATE INDEX idx_submissions_contest ON submissions(contest_id, user_id, problem_id, id);
