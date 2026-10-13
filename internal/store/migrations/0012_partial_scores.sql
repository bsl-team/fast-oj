-- M13：部分分与用例捆绑。
-- 组内全过才给分，而组的分值就是组内各条分值之和，所以只需要一个组名，不必再建一张表。
ALTER TABLE testcases ADD COLUMN group_name TEXT NOT NULL DEFAULT '';
-- 每条用例实际拿到多少分由服务器按组规则算，页面要能区分「这条过了」和「这组给了分」。
ALTER TABLE runs ADD COLUMN score INTEGER NOT NULL DEFAULT 0;
-- 判题详情透露级别：none 只给总判定，case 给逐条判定，diff 再加实测/期望，full 给判机侧细节。
ALTER TABLE problems ADD COLUMN detail_level TEXT NOT NULL DEFAULT 'diff'
    CHECK (detail_level IN ('none','case','diff','full'));
