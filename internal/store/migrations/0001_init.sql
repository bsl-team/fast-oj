-- 0001: core judging schema
-- All timestamps are Unix milliseconds.

CREATE TABLE users (
    id         INTEGER PRIMARY KEY,
    handle     TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    passhash   TEXT    NOT NULL DEFAULT '',
    role       TEXT    NOT NULL DEFAULT 'user' CHECK (role IN ('user','admin')),
    created_at INTEGER NOT NULL
);

CREATE TABLE tokens (
    token      TEXT    PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    label      TEXT    NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);
CREATE INDEX idx_tokens_user ON tokens(user_id);

CREATE TABLE problems (
    id            INTEGER PRIMARY KEY,
    pid           TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    title         TEXT    NOT NULL,
    statement     TEXT    NOT NULL DEFAULT '',
    time_limit_ms INTEGER NOT NULL DEFAULT 1000 CHECK (time_limit_ms > 0),
    mem_limit_kb  INTEGER NOT NULL DEFAULT 262144 CHECK (mem_limit_kb > 0),
    languages     TEXT    NOT NULL DEFAULT '',
    visible       INTEGER NOT NULL DEFAULT 1,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);

CREATE TABLE testcases (
    id         INTEGER PRIMARY KEY,
    problem_id INTEGER NOT NULL REFERENCES problems(id) ON DELETE CASCADE,
    ordinal    INTEGER NOT NULL,
    name       TEXT    NOT NULL,
    is_sample  INTEGER NOT NULL DEFAULT 0,
    score      INTEGER NOT NULL DEFAULT 1 CHECK (score >= 0),
    input_rel  TEXT    NOT NULL,
    output_rel TEXT    NOT NULL,
    UNIQUE (problem_id, name)
);
CREATE INDEX idx_testcases_problem ON testcases(problem_id, ordinal);

CREATE TABLE judges (
    id           INTEGER PRIMARY KEY,
    name         TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    languages    TEXT    NOT NULL DEFAULT '',
    note         TEXT    NOT NULL DEFAULT '',
    last_seen_at INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE submissions (
    id          INTEGER PRIMARY KEY,
    user_id     INTEGER NOT NULL REFERENCES users(id),
    problem_id  INTEGER NOT NULL REFERENCES problems(id),
    language    TEXT    NOT NULL,
    source      TEXT    NOT NULL,
    status      TEXT    NOT NULL DEFAULT 'queued'
                CHECK (status IN ('queued','compiling','running','done','abandoned')),
    verdict     TEXT    NOT NULL DEFAULT '',
    time_ms     INTEGER NOT NULL DEFAULT -1,
    mem_kb      INTEGER NOT NULL DEFAULT -1,
    score       INTEGER NOT NULL DEFAULT 0,
    total_score INTEGER NOT NULL DEFAULT 0,
    failed_case TEXT    NOT NULL DEFAULT '',
    detail      TEXT    NOT NULL DEFAULT '',
    judge_id    INTEGER REFERENCES judges(id),
    created_at  INTEGER NOT NULL,
    started_at  INTEGER,
    judged_at   INTEGER
);
CREATE INDEX idx_submissions_user ON submissions(user_id, id DESC);
CREATE INDEX idx_submissions_problem ON submissions(problem_id, id DESC);
-- The judge poll only ever scans this partial index.
CREATE INDEX idx_submissions_pending ON submissions(id) WHERE status = 'queued';

CREATE TABLE runs (
    id            INTEGER PRIMARY KEY,
    submission_id INTEGER NOT NULL REFERENCES submissions(id) ON DELETE CASCADE,
    ordinal       INTEGER NOT NULL,
    case_name     TEXT    NOT NULL,
    verdict       TEXT    NOT NULL,
    time_ms       INTEGER NOT NULL DEFAULT -1,
    mem_kb        INTEGER NOT NULL DEFAULT -1,
    is_sample     INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_runs_submission ON runs(submission_id, ordinal);
