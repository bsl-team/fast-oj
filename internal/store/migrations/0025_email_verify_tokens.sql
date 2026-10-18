-- 0025: 邮箱验证链接的令牌（M25f）。
--
-- 表里存的是令牌的 SHA-256（十六进制），不是令牌本身：链接只在邮件里出现一次，
-- 而库被人读到不该等于拿到一叠还能用的验证链接。
--
-- used_at 为空 = 还没用掉。验证成功时把它写下而不是删行，于是"同一个链接再点一次"
-- 有确定的答案（作废），页面上也能说清是重复点击而不是链接写错了。
-- 重发 = 先把该用户名下没用掉的旧令牌删掉再插一条新的，所以旧邮件里的那条链接
-- 之后必然失效：一个人手里同时躺着两条能验证的链接，等于验证过的地址可以事后换掉。
--
-- email 一并记下：验证时要求它和 users.email 还一致，这样万一以后允许改邮箱，
-- 寄往旧地址的那条链接不能替新地址作证。
CREATE TABLE email_verify_tokens (
    token_hash TEXT    PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    email      TEXT    NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    used_at    INTEGER
);

CREATE INDEX idx_email_verify_tokens_user ON email_verify_tokens(user_id);
CREATE INDEX idx_email_verify_tokens_expiry ON email_verify_tokens(expires_at);
