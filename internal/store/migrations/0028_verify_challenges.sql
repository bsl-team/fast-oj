-- 0028: 人机验证等待（观感是假的，等待是真的）。
--
-- 这张表只干一件事：把"这个来源地址要等到某个时刻之后才被放行"这句话记在服务端。
-- 页面上那个转圈和复选框都不算数——浏览器说自己等了 5 秒，服务端没有任何理由相信；
-- 真正让脚本多花成本的是这里的 ready_at：令牌由渲染表单那次请求发出来，绑死来源地址
-- 和用途（注册还是登录），时间没到就核销不掉。脚本要绕过，只能同样等那么久。
--
-- 为什么是一次性 DELETE 而不是加一个 used 标记：核销这个动作要同时满足"没过期、来源
-- 对、用途对、时间到了、以前没用过"，把这五条写在一条 DELETE 的 WHERE 里，靠
-- RowsAffected 判断，就不存在"先查再改"之间的竞争窗口——同一个令牌并发提交两次，
-- 只有一次能删掉那一行。加了 used 标记反而要处理"标记写了但请求失败"的中间态。
--
-- 为什么同 kind+ip 只留一行（写之前先删旧的）：这张表是匿名请求能写东西的少数几张表
-- 之一，刷新一次注册页就多一行。按来源地址收成一个待核销窗口，表的大小就被
-- "同时在看表单的地址数"限制住，而不是被"刷页的次数"限制住。
CREATE TABLE verify_challenges (
    -- 随机令牌，出现在表单的隐藏字段里；它不是凭据，只代表"这个地址领过一个等待"。
    token      TEXT    PRIMARY KEY,
    -- 'register' 或 'login'：用途绑死，拿登录页领的令牌去交注册表单不算数。
    kind       TEXT    NOT NULL,
    -- 领令牌的来源地址（RemoteAddr，不信 X-Forwarded-For，理由同 0020）。
    ip         TEXT    NOT NULL,
    -- 服务端认为"可以放行"的时刻，毫秒。
    ready_at   INTEGER NOT NULL,
    -- 过期时刻：领了不用就作废，否则攒下来的旧令牌等于给未来的提交预留额度。
    expires_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL
);

-- 核销走主键；按来源地址收旧行走这条，清过期行走 expires_at 那条（过期清理挂在提交那一路
-- 而不是渲染那一路：渲染谁都能触发，提交才是被限流和核销管住的那一步）。
CREATE INDEX idx_verify_challenges_ip ON verify_challenges(kind, ip, expires_at);
CREATE INDEX idx_verify_challenges_expiry ON verify_challenges(expires_at);
