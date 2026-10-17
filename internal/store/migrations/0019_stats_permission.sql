-- 0019: 站点统计的入口权限。
--
-- 统计页要回答的是"这个站现在怎么样"：多少人、多少提交、判题分布、请求量。这些数字
-- 对外没有意义（而且暴露判题失败率会给爬虫提示），所以单独一个管理侧键，不跟
-- site.settings 混在一起：能改首页文案的人不必看到运营数据。

INSERT INTO permissions (key, label, staff) VALUES ('stats.view', '查看站点统计', 1);
INSERT INTO role_permissions (role, key) VALUES ('admin', 'stats.view');
