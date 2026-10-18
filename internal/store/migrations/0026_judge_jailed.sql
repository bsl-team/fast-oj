-- 0026: 判机的沙盒状态从日志搬进表里。
--
-- "这台判机有没有把提交关进牢房"是这套系统最重要的一个安全属性，但它今天只出现在
-- 启动日志的一行和 /judges 那句自由文本备注里——没人盯日志就等于没人知道。判机每次
-- 心跳都会重报（沙盒开不起来时那个值会翻过去），于是 /judges 可以在没有任何一台机器
-- 有牢房的时候当面警告。
--
-- 两列而不是一个 bool：`jailed_known=0` 表示这台机器还没说过（升级后没心跳过的老二进制），
-- 与 `jailed=0, jailed_known=1`（它说了开不起来）是两件事。页面必须分开写——
-- "没人说过"与"说过没有"不一样，混起来就是把没确认的事当成确认过的事。
ALTER TABLE judges ADD COLUMN jailed INTEGER NOT NULL DEFAULT 0;
ALTER TABLE judges ADD COLUMN jailed_known INTEGER NOT NULL DEFAULT 0;
