-- 0008: 每日打卡。
--
-- 连续天数不另开一列存：每次打卡把当时的 streak 记进这一天的行，读最近一行就是
-- 当前状态，改规则时历史行不会被写坏。day 用服务器本地日期（YYYY-MM-DD），
-- 所以"今天"跟着判机所在时区走。

CREATE TABLE checkins (
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    day        TEXT    NOT NULL,
    streak     INTEGER NOT NULL CHECK (streak > 0),
    created_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, day)
);
CREATE INDEX idx_checkins_recent ON checkins(user_id, day DESC);
