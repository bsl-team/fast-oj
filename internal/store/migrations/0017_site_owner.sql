-- 0017: 超级管理员（站主）。
--
-- 站主的定义是"谁第一个坐上这个位子"，而且这个位子不可转让、不可被别人改动。
-- users.role 上带着 CHECK (role IN ('user','admin'))，加第三个值要重建整张被十几张表
-- 外键引用的 users，代价远大于收益；所以站主另存一张只允许一行的表。
-- id 上的 CHECK 就是"永远只有一行"这句约束本身。

CREATE TABLE site_owner (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    claimed_at INTEGER NOT NULL
);

-- 已经跑过一段时间的站点：把站主交给最早的那个管理员账号。
-- 不预置的话，一个老站升级完，下一个来注册的路人就直接拿到了最高权限。
INSERT INTO site_owner (id, user_id, claimed_at)
SELECT 1, id, created_at FROM users WHERE role='admin' ORDER BY id LIMIT 1
ON CONFLICT(id) DO NOTHING;
