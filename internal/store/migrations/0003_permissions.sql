-- 0003: capability-based permissions and problem ownership.
--
-- A role grants a baseline set; a per-user row then adds or removes a single
-- capability without promoting anyone to a different role. "谁可以出题" is one
-- row here, not a new account tier.

CREATE TABLE permissions (
    key      TEXT    PRIMARY KEY,
    label    TEXT    NOT NULL,
    staff    INTEGER NOT NULL DEFAULT 0  -- 1 = 管理侧权限，授予即等于工作人员
);

INSERT INTO permissions (key, label, staff) VALUES
    ('problem.create',        '创建题目',                 1),
    ('problem.edit.any',      '编辑他人题目',             1),
    ('problem.publish',       '公开或隐藏题目',           1),
    ('testcase.manage',       '上传与删除判题用例',       1),
    ('contest.create',        '创建比赛',                 1),
    ('contest.manage.any',    '管理他人比赛',             1),
    ('user.manage',           '管理用户（改名、封禁）',   1),
    ('permission.manage',     '授予与回收权限',           1),
    ('judge.view',            '查看判机队列与判机框',     1),
    ('feed.moderate',         '删除他人动态',             1),
    ('site.settings',         '修改站点设置与首页',       1),
    ('submission.view.any',   '查看他人源码与逐用例明细', 1);

-- Baseline grants per account tier. role is a plain label matching users.role,
-- so it carries no foreign key.
CREATE TABLE role_permissions (
    role TEXT NOT NULL,
    key  TEXT NOT NULL REFERENCES permissions(key) ON DELETE CASCADE,
    PRIMARY KEY (role, key)
);

CREATE TABLE user_permissions (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    key     TEXT    NOT NULL REFERENCES permissions(key) ON DELETE CASCADE,
    granted INTEGER NOT NULL DEFAULT 1,
    PRIMARY KEY (user_id, key)
);

-- Baseline grants: ordinary users get none of the staff capabilities; the admin
-- tier holds every one of them, which is also what the bootstrap token maps to.
INSERT INTO role_permissions (role, key)
SELECT 'admin', key FROM permissions;

-- Every problem remembers who made it, so "can edit" can mean "owns it" without
-- handing out a global edit right.
ALTER TABLE problems ADD COLUMN owner_id INTEGER REFERENCES users(id);
CREATE INDEX idx_problems_owner ON problems(owner_id);
