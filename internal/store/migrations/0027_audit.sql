-- 0027: 特权操作审计。
--
-- 日志早就有，但那是打到 stderr 的一行：进程一重启就没了，而"谁在什么时候给谁开了
-- 什么权限、封了谁的号、删了哪道题"恰恰是要在事后几个月还能问出来的东西。判机令牌泄漏
-- 时能翻出来的也只有这张表，不是那台机器上还剩多少日志。
--
-- 为什么只追加、永不更新也永不删除：这一行的全部价值在于"当时是这样"。改了它，就等于
-- 事后编辑历史——而最有动机改它的人正是被记下来的那批人（能改权限的人也能删审计，只要
-- 允许 UPDATE/DELETE 存在）。所以这张表只有 INSERT 与 SELECT 两条路：
-- 代码里没有任何改它或删它的函数，页面也只有"看"。
--
-- 为什么动手的人要把当时的用户名另存一份文本，而不是只留 actor_id 外键：账号可以注销，
-- 注销后用户名会释放给下一个人注册。只留 id 的话，那一条审计要么变成"封禁用户"，要么
-- 更糟——变成几个月后拿同名新账号的人的罪名。actor 存的是当时那个人的名字，actor_id
-- 只是给人点的跳转链接，它允许指向一个已经不是同一个人的账号，也允许是 0（判机令牌、
-- 匿名请求这些没有账号的场合）。
--
-- detail/target/ip 都是攻击者或管理员能控制的文本，写入侧一律截断到固定上限（见
-- internal/store/audit.go）：一张只追加的表如果每行能被塞进几 MB，审计本身就变成了
-- 把站点写满的手段。
CREATE TABLE audit (
    id       INTEGER PRIMARY KEY,
    -- 毫秒时间戳，与全站别的表同一口径。
    at       INTEGER NOT NULL,
    -- 动手的人的账号 id；没有账号的凭据（判机令牌、管理员令牌、匿名）为 0。
    actor_id INTEGER NOT NULL DEFAULT 0,
    -- 动手时那个人的用户名文本，理由见上面的注释块。
    actor    TEXT    NOT NULL,
    -- 动作键，例如 perm.grant / ip.ban / problem.delete：代码里的常量，不是给人读的文本。
    action   TEXT    NOT NULL,
    -- 被操作的东西：用户名、题目号、比赛号、工单号、IP 段……
    target   TEXT    NOT NULL DEFAULT '',
    detail   TEXT    NOT NULL DEFAULT '',
    -- 来源地址（RemoteAddr，不信 X-Forwarded-For，理由同 0020）。
    ip       TEXT    NOT NULL DEFAULT ''
);

-- 两个默认读法：按时间倒着翻最近的，和按动作筛（"这个站删过哪些题"）。
CREATE INDEX idx_audit_at ON audit(at DESC);
CREATE INDEX idx_audit_action ON audit(action, id DESC);

-- 看审计不需要新权限键：能看安全态势的人（ip.manage：封人并且看见谁在打）本来就够格看
-- 这些，多一个键只是多一处会漏配的地方。
