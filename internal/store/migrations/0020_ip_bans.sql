-- 0020: IP 封禁。
--
-- 封禁的是"来源地址"，不是账号：一个正在刷站的机器可以没有账号，一个被偷号的人
-- 也未必值得把他整个账号抹掉。命中的请求一律 403，整站拦死（含静态资源与判机回调），
-- 所以这个页面同时会显示每个地址的流量，让管理员先看再封。
--
-- ip 存的是文本，允许单地址（203.0.113.9）或网段（203.0.113.0/24）：学校机房一个出口
-- 后面挂着几十台机器，能整段封掉才是可用的粒度。匹配在 Go 里用 netip 做，所以这里
-- 不加 CHECK——写法是否正确由入口校验负责，老数据不会因为规则收紧而卡住。

CREATE TABLE ip_bans (
    ip         TEXT    PRIMARY KEY,
    note       TEXT    NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    created_by INTEGER REFERENCES users(id) ON DELETE SET NULL
);

-- 封禁与看流量是同一件事的两面：能封人的人必须能看见是谁在打。
-- 这一项也是管理侧权限，普通管理员不给就看不到别人从哪来。
INSERT INTO permissions (key, label, staff) VALUES ('ip.manage', '封禁 IP 与查看 IP 流量', 1);
INSERT INTO role_permissions (role, key) VALUES ('admin', 'ip.manage');
