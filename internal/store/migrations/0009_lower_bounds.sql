-- 0009：被中途杀掉的运行只能给出下界。
--
-- 一个在 1050ms 被砍掉的程序，真实用时是"至少 1050ms"；内存熔断同理。与其把它
-- 当成实测值显示，不如带上这个标记，网页与文本视图据此加前缀 ">"。

ALTER TABLE runs ADD COLUMN time_min INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runs ADD COLUMN mem_min  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE submissions ADD COLUMN time_min INTEGER NOT NULL DEFAULT 0;
ALTER TABLE submissions ADD COLUMN mem_min  INTEGER NOT NULL DEFAULT 0;
