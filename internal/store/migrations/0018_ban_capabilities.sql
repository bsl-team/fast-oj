-- 0018: 三项可以单独禁止的能力。
--
-- 有人刷屏动态、灌水发帖、或者拿 AI 在比赛里一条条试，需要的处置是"别让他再做这件
-- 事"，而不是封掉整个账号——历史提交、成绩、私信都还得留着。
--
-- 这三条不是管理侧权限（staff=0）：默认人人可用，禁止靠 user_permissions 里的一条
-- "收回"，在权限控制台点一下就能做到，恢复则点"恢复默认"。

INSERT INTO permissions (key, label, staff) VALUES
    ('feed.post',    '发动态',                0),
    ('topic.post',   '发帖与求助',            0),
    ('contest.join', '参加比赛与赛内提交',    0);

-- 两个层级都打底放开。没有这两行，普通用户读不到任何权限（'user' 层级本来就是空的），
-- 一上来就等于全站禁止。
INSERT INTO role_permissions (role, key)
SELECT r.role, p.key
  FROM (SELECT 'user' AS role UNION ALL SELECT 'admin') r
  JOIN permissions p ON p.key IN ('feed.post', 'topic.post', 'contest.join');
