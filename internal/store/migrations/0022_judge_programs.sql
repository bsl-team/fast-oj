-- 0022: 题目自带的判机程序（SPJ 判题器 / 交互题的交互器）。
--
-- 为什么不往 problems 上塞两列：这两类东西是**代码**，体积跟题面不是一个量级，而且以后
-- 还会加别的种类（通信题的环境程序、打分制判题器）。放一张表里按 (题目, 种类) 一行，
-- 加种类不动表结构，读题目详情也不会顺手拖进几十 KB 源码。
--
-- 这些源码由判机编译并执行，等于"出题人写的代码在别人机器上跑"。所以它跟学生提交一样
-- 走沙盒、一样降权，不因为作者是管理员就免隔离；判机没开沙盒时这类题目不收（见 README）。
CREATE TABLE judge_programs (
    problem_id INTEGER NOT NULL REFERENCES problems(id) ON DELETE CASCADE,
    kind       TEXT    NOT NULL CHECK (kind IN ('checker','interactor')),
    language   TEXT    NOT NULL DEFAULT 'cpp',
    source     TEXT    NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (problem_id, kind)
);

-- 只有能出题的人能写判题器：这是把"在判机上执行代码"的权限发出去，不该跟着
-- problem.edit 顺手流给每个被委托改题面的人。
INSERT INTO permissions (key, label, staff) VALUES ('judge_program.edit', '给题目写 SPJ 判题器与交互器', 1);
INSERT INTO role_permissions (role, key) VALUES ('admin', 'judge_program.edit');
