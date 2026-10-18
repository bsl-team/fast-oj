-- 0023: 判机自报能力 + 判机实际用的工具链。
--
-- 要修的是这种谎：提交页写着「C++14 · O2 关」「本题由 SPJ 判定」，而判那次的是一台
-- 旧判机——旧二进制不认识工单里的 options / checker / interactor，会照它自己那套硬编码
-- 编、按逐字节比较判，然后正常回报成功。协议里没有版本协商，所以服务器无从知道它判错了调。
--
-- 两张表各补一角：
--   judges.capabilities —— 这台判机自报它会什么（逗号串，词表固定：options / checker /
--     interactor）。老判机不会送这一列，于是它是空串，服务器就不会把需要新能力的工单派给它；
--     派不出去的那条会排队等一台判得对的机器，而不是被一台判不了的机器判掉。
--   submissions.toolchain —— 判机回报它**实际**用的那一份口径（"C++14 · O2 关"、
--     "python3.14"）。页面显示的是这一列而不是提交时的选择；空串写「未回报」，
--     因为"没人说过"与"就是这么判的"是两件事。
ALTER TABLE judges ADD COLUMN capabilities TEXT NOT NULL DEFAULT '';
ALTER TABLE submissions ADD COLUMN toolchain TEXT NOT NULL DEFAULT '';
