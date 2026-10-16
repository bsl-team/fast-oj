# fast-oj

超级无敌简洁的 OJ，UI 极少。**只有网页**：人是唯一的使用者，出题、用例、判题、私信、后台全在浏览器里点。

- 单个可执行文件，零外部服务依赖（SQLite 内嵌，纯 Go 驱动）
- 判机与服务器分离，判机只需要一个出站 HTTP 连接（那是判机的内部协议，网页上没有入口）
- 题面、动态、个人简介、首页语统一走 Markdown + KaTeX 数学公式
- 首页内容（站名、首页语、友情链接、页脚、页脚 API 提示、注册开关）在网页后台改，不用重启
- 页面每次请求的处理时间在 0.2–3 ms 之间（200 题 / 500 提交规模，见[性能](#性能)）

---

## 30 秒跑起来

### Ubuntu / Debian：一条命令

```bash
./run.sh
```

四件事：确认 `go` 和 `g++`（缺了就告诉你 apt 装什么）、编译到 `dist/`、按 `127.0.0.1:8080` 起服务器并顺手带起一台判机、Ctrl-C 时两个一起收掉（不留孤儿判机空轮询）。两个令牌没给就随机生成并打印出来，想固定下来就 `export FASTOJ_ADMIN_TOKEN FASTOJ_JUDGE_TOKEN`——脚本里写死一对默认值迟早跟着仓库传到网上。

```bash
FASTOJ_ADDR=0.0.0.0:8080 ./run.sh   # 同网段都能访问（判机仍然走 127.0.0.1 连它）
JUDGE=0 ./run.sh                    # 只起服务器，判机在另一台机器上
./run.sh -no-register -v            # 其余参数原样传给服务器
FASTOJ_DATA=/srv/fast-oj/data ./run.sh
```

端口上已经有一个 fast-oj 在跑时它会拒绝启动并提示换地址，不会留下一个"看起来起来了但其实没起来"的服务器。

### Windows，或想手动分两步

```powershell
powershell -File build.ps1        # 产出 dist\fast-oj.exe dist\fast-judge.exe
```

```bash
# 1. 服务端（两个令牌必填，留空会拒绝启动）
./dist/fast-oj.exe -addr 127.0.0.1:8080 -data data -admin-token ADM -judge-token JDG

# 2. 判机端（需要 g++ 在 PATH 上，可以和服务器同机）
./dist/fast-judge.exe -server http://127.0.0.1:8080 -token JDG -name box1 -v
```

浏览器打开 <http://127.0.0.1:8080>。**全新库的第一步**：打开 <http://127.0.0.1:8080/admin/claim>，把 `-admin-token` 的值填进去，给 `admin` 账号设一个网页密码，之后就是普通登录（这个页面在旧库上也能用，属于恢复手段，会先警告你要覆盖已有密码）。命令行只留一件事——探活：

```bash
curl -s localhost:8080/healthz          # ok
```

运行概览（版本、数据目录、WAL、各项计数、页面清单）现在是网页 `/about`，判机在线情况是网页 `/judges`。

现成的演示脚本：

没有 curl 演示脚本了：以前那些脚本走的正是被撤掉的 API。想试完整流程就在浏览器里注册两个账号 → 导航栏「出题」建题 → 用例页加数据 → 提交代码。

---

## 没有对外 API

以前同一个 URL 会按 `Accept` 与 `?format=` 给浏览器 HTML、给 curl 纯文本、给脚本 JSON，还有「用户 API 令牌」让脚本代替人提交。这套全部撤掉了：

| 撤掉 | 换成 |
|------|------|
| `?format=json` / `?format=text` / `Accept` 协商 | 每个页面只出 HTML |
| 用户 API 令牌（`/settings` 签发、`/account`、`/whoami`） | 浏览器会话，`tokens` 表已删 |
| `POST /submissions`（脚本提交代码） | 题面页的提交表单 |
| `POST` / `PUT` / `DELETE /problems`（JSON 建题改题） | 网页出题：`/problems/new`、`/problems/{pid}/edit` |
| `POST` / `DELETE /problems/{pid}/cases`（JSON + zip 清单） | 网页用例管理：逐条增删改，zip 只做批量导入 |
| `GET` / `POST /users`、`POST /users/{handle}/tokens` | 注册页 + `/admin/perms` 权限控制台 |
| `?wait=秒` 长轮询 | 提交详情页判题中每 2 秒自动刷新 |

保留的两处不是给人用的：`/healthz`（一行 `ok`，给 Docker / 反代探活）和 `/judge/*`（判机协议，只有判机令牌能访问，网页里没有任何入口）。少了一套视图就少一条代码路径：页面和权限判定从此只有一条，浏览器不会比谁多看到一点东西。

网页版页面：首页 `/`（站名 + 首页语 + 统计 + 最新题目 + 动态 + 打卡）、题库 `/problems`、题面 `/problems/{pid}`、出题 `/problems/new`、改题 `/problems/{pid}/edit`、用例 `/problems/{pid}/cases`、动态 `/feed`、提交列表 `/submissions`、提交详情 `/submissions/{id}`、排行榜 `/rank`、个人主页 `/u/{handle}`、关注与粉丝 `/u/{handle}/following` `/followers`、私信 `/messages`、登录注册 `/login` `/register`、设置 `/settings`、站点设置 `/admin/site`、权限控制台 `/admin/perms`、判机 `/judges`、运行概览 `/about`。

---

## 页面与操作一览

没有对外 API：下面每一个路径都只出 HTML，浏览器会话是唯一的人用凭据。

| 方法 | 路径 | 权限 | 说明 |
|------|------|------|------|
| `GET` | `/healthz` | 公开 | 探活，会真连一次数据库 |
| `GET` | `/about` | 公开 | 运行概览页：版本、数据目录、WAL、各项计数、页面清单 |
| `GET` | `/` | 公开 | 首页：站名 + 首页语 + 统计 + 最新题目 + 动态 + 打卡 |
| `GET` | `/problems` | 公开 | 列表，`?q=` `?limit=` `?after=` |
| `GET` | `/problems/{key}` | 公开 | 题面 + 样例（`key` 可为编号或数字 id） |
| `GET` `/POST` | `/problems/new` `/problems` | `problem.create` | 网页出题：编号、标题、题面（可预览）、时限、内存、语言、可见性 |
| `GET` `/POST` | `/problems/{key}/edit` `/problems/{key}` | 该题 owner 或 `problem.edit.any` | 网页改题；改可见性另需 `problem.publish` |
| `POST` | `/problems/{key}/delete` | 该题 owner 或 `problem.edit.any` | 删题（连带用例文件） |
| `GET` | `/problems/{key}/cases` | owner 或 `testcase.manage` | 用例管理页：列表、大小、加一条、zip 导入、清空 |
| `POST` | `/problems/{key}/cases` | 同上 | 加一条用例（名称、分值、样例、输入、输出） |
| `POST` | `/problems/{key}/cases/upload` | 同上 | 批量导入 zip（`名字.in` + `名字.out`，同名覆盖） |
| `GET` `/POST` | `/problems/{key}/cases/{id}/edit` | 同上 | 改单条用例；超过 256KB 的文件只改元信息，内容走 zip |
| `POST` | `/problems/{key}/cases/{id}/delete` | 同上 | 删一条用例（连带它的文件） |
| `POST` | `/problems/{key}/cases/wipe` | 同上 | 清空用例 |
| `GET` | `/problems/{key}/cases/{name}/{in\|out}` | 样例公开，判题需 admin/判机 | 取单个用例正文 |
| `POST` | `/problems/{key}/submit` | 登录用户 | 网页表单提交 |
| `GET` | `/submissions` | 公开 | 列表，`?user=`（或 `me`）`&problem=&limit=&after=` |
| `GET` | `/submissions/{id}` | 公开 | 结果。判机每完成一个用例就回报一行，未判完时页面每 2 秒自动刷新，逐用例行一条条冒出来 |
| `GET` | `/contests` | 公开 | 比赛列表：状态（未开始/进行中/已结束）、时间、赛制、题数、人数、是否封榜；`?q=` `?limit=` `?after=` |
| `GET` | `/contests/{key}` | 公开 | 比赛主页：时间、规则、题目（成员与主办方看得到）、名单、加入/退出 |
| `GET` `/POST` | `/contests/new` `/contests` | `contest.create` | 网页建赛：编号（留空自动 `C0001`）、标题、起止时间、赛制、加入码、每队人数、罚时、封榜、可见性 |
| `GET` `/POST` | `/contests/{key}/edit` `/contests/{key}` | 该赛 owner 或 `contest.manage.any` | 改赛与挂题：题目下拉、比赛内分值、题号标签、上下调序、移下、删除比赛 |
| `POST` | `/contests/{key}/problems` | 同上 | 挂题（未公开的题目只有能编辑它的人能挂）；同一题再提交一次就是改它的分值 |
| `POST` | `/contests/{key}/problems/{problemKey}/remove\|move` | 同上 | 移下 / 调顺序（移下的题历史成绩照算） |
| `POST` | `/contests/{key}/join` | 登录用户 | 加入；私有赛校验加入码，已结束不再收人 |
| `POST` | `/contests/{key}/leave` | 成员 | 退出（既有提交仍是比赛内记录） |
| `POST` | `/contests/{key}/teams` | 成员 | `op=create\|join\|leave`：建队并进队 / 加入同名队 / 退队 |
| `GET` | `/contests/{key}/standings` | 见[比赛](#比赛限时--独立榜--加入码--队伍oiioiacm-三种赛制) | 该比赛独立榜，不影响 `/rank`；OI 比赛结束前除主办方外看不到 |
| `GET` | `/contests/{key}/p/{题号}` | 成员或主办方 | 比赛内题号入口，进去的提交自动归属该比赛 |
| `GET` | `/rank` | 公开 | 排行榜 |
| `GET` | `/topics` | 公开 | 社区贴列表（统一管理页），`?q=` `?after=` `?limit=`；登录后每行带上「你能删」的删除按钮 |
| `GET` `/POST` | `/topics/new` `/topics` | 登录用户 | 发帖：类型（求助 / 社区）、标题、正文（可预览）、题号 |
| `GET` | `/topics/{id}` | 公开 | 帖子正文与评论；未登录只能看 |
| `POST` | `/topics/{id}/comment` | 登录用户 | 发表评论（每人每小时 60 条） |
| `POST` | `/topics/{id}/delete` | 本人或 `topic.moderate` | 删帖（评论一起没） |
| `POST` | `/topics/comments/{id}/delete` | 本人或 `topic.moderate` | 删一条评论 |
| `GET` | `/tickets` | 登录用户 | 工单列表：`?scope=mine\|queue\|all` `?status=` `?q=` `?after=`（后两种要 `ticket.handle`） |
| `GET` `/POST` | `/tickets/new` `/tickets` | 登录用户 | 提交工单（申诉 / 故障建议 / 举报 / 账号 / 其他），可挂题目或提交 |
| `GET` | `/tickets/{id}` | 作者或 `ticket.handle` | 工单详情与流水；**陌生人拿到 404**，不确认存在 |
| `POST` | `/tickets/{id}/reply` | 作者或处理人 | 追问 / 回复（每人每小时 30 条） |
| `POST` | `/tickets/{id}/status` | 处理人任意；作者只能关自己那张 | 改状态并留一条流水 |
| `POST` | `/tickets/{id}/delete` | `ticket.handle` | 删工单（回复一起没） |
| `GET` | `/problems/{key}/topics` | 公开 | 这道题的求助贴；从这里进发帖表单会锁主题目 |
| `GET` | `/feed` | 公开 | 动态时间线，`?user=` `?after=` `?limit=` |
| `POST` | `/feed` | 登录用户 | 发一条动态（每人每小时 20 条） |
| `POST` | `/feed/{id}/delete` | 本人或 `feed.moderate` | 删除 |
| `POST` | `/feed/{id}/hide` | `feed.moderate` | 隐藏 / 恢复（不删数据） |
| `GET` | `/u/{handle}` | 公开 | 个人主页 |
| `GET` | `/u/{handle}/following` | 公开 | 关注名单 |
| `GET` | `/u/{handle}/followers` | 公开 | 粉丝名单 |
| `POST` | `/u/{handle}/follow` | 登录用户 | 关注；`/unfollow` 取消 |
| `GET` | `/messages` | 登录用户 | 我的私信会话列表（未读数在最后一条那列） |
| `GET` | `/messages/{handle}` | 登录用户 | 一对一对话，打开即标记已读 |
| `POST` | `/messages/{handle}` | 登录用户 | 发一条私信（受对方收信策略限制） |
| `POST` | `/settings/privacy` | 登录用户 | 设置谁可以给我发私信 |
| `POST` | `/checkin` | 登录用户 | 每日打卡，返回连续天数 |
| `GET` `/POST` | `/login` `/logout` | — | 邮箱或用户名 + 密码登录，session cookie |
| `GET` `/POST` | `/register` | 公开（可关） | 自助注册，邮箱暂不验证 |
| `GET` `/POST` | `/settings` `/settings/*` | 登录用户 | 资料、密码、私信策略 |
| `POST` | `/judge/claim` | 判机令牌 | 心跳 + 领取一条（空队列返回 404 `queue empty`） |
| `GET` | `/judge/cases/{id}/{in\|out}` | 判机令牌 | 取用例正文（含判题用例） |
| `POST` | `/judge/submissions/{id}/status` | 判机令牌 | 进度 + 逐用例回报（`compiling` → `running`） |
| `POST` | `/judge/submissions/{id}/result` | 判机令牌 | 终局判定与逐用例结果 |
| `GET` | `/judges` | `judge.view` | 判机框清单 |
| `GET` `/POST` | `/admin/site` | `site.settings` | 站名、首页语、友情链接、页脚、API 提示、注册开关 |
| `GET` | `/admin/perms` | `permission.manage` | 账号列表与权限控制台 |
| `GET` | `/admin/perms/u/{handle}` | `permission.manage` | 单个账号的权限矩阵 |
| `POST` | `/admin/perms/u/{handle}` | `permission.manage` | 授予 / 收回 / 恢复默认（不能改自己） |
| `POST` | `/admin/users/{handle}/role` | `user.manage` | 改角色 |
| `POST` | `/admin/users/{handle}/status` | `user.manage` | 封禁 / 解封（封禁踢掉会话） |

出题在浏览器里走：导航栏「出题」→ 填编号、标题、题面（可以「预览」看渲染效果，不保存）→ 保存后直接落到用例页，
逐条加输入输出或用 zip 批量导入。表格里最后四行是判机协议：只有判机令牌能用，网页上没有任何入口，
`/healthz` 与它们是两个非 HTML 的端点。

---

## 题面写作（Markdown + KaTeX）

题面按 Markdown 渲染，支持标题、表格、围栏代码块、列表、引用、链接。

数学公式：

- 行内用 `$a + b$`
- 行间用 `$$ \frac{a+b}{2} $$`，**并且要让这一段只放公式**：`$$…$$` 夹在句子里会被读成两个相邻的行内 `$…$`，结果是公式渲染了、两侧各留下一个裸 `$`。行间公式请单独占一行（前后空行最稳）。

渲染器把两者输出成 `<span class="math inline">\(…\)</span>` / `<span class="math display">\[…\]</span>`，页面里的 `math.js` 只对这两个节点调用 KaTeX，所以代码块里的下划线、`$` 不会被误当成公式。KaTeX 走 CDN（jsdelivr，0.16.11），但**只有页面上真的出现公式时才会去取**（模板里没有 CDN 标签，`static/math.js` 发现 `span.math` 才注入）；**CDN 不可达时公式退回显示原始 TeX**，题面照样能读。需要完全离线就把 KaTeX 与字体嵌进二进制，见[性能](#浏览器侧的账f12-限速-3g)。

> 注意：请写 `$…$`，不要写 `\(…\)` —— 后者会被当成普通文本并吞掉反斜杠。

安全边界（题面也是不可信输入）：

- 裸 HTML 一律丢弃（`SkipHTML`），`<script>`、`<img onerror=…>` 进不去
- `javascript:` / `data:` / `vbscript:` / `file:` / `blob:` / `about:` 链接被拒（`Safelink` + 白名单谓词）
- 外链自动加 `target="_blank" rel="nofollow"`

同一条流水线也用在动态正文、个人简介、首页语与页脚上 —— 这些同样是用户/操作员输入的不可信文本，规则一模一样。

---

## 首页、站点设置与动态

`/admin/site`（需要 `site.settings` 权限）改的是数据库里的站点配置，不是启动参数：

| 项 | 说明 |
|----|------|
| 站点标题 | 标签页、导航栏与首页大标题 |
| 首页语 | 首页大标题下面那段，支持 Markdown + 公式 |
| 友情链接 | 每行一条 `标题 \| https://…`，最多 20 条，只允许 http/https/mailto |
| 页脚 | 可选，全站显示 |
| 页脚 API 提示 | 默认那句 `curl -s localhost:8080/problems`，支持 Markdown；清空则整行不显示（部署端口不是 8080 时改这里） |
| 允许自助注册 | 与 `-no-register` 取与；关掉后 `/register` 的页面与 POST 都是 403 |

保存是一次事务，页面缓存 2 秒后自动过期，改完刷新即见。被拒的保存会把已填内容原样留在表单里（长度按字符数算，与浏览器 `maxlength` 一致），不会让操作员整页重打。

动态（`/feed`）是纯文本版的朋友圈：

- 登录即可发，正文最长 4000 字符，一小时最多 20 条（按用户计数，复用登录节流的那张表）
- 只能删自己的；`feed.moderate` 可以删别人的，也可以只隐藏（`visible` 翻转，不删数据）
- 隐藏的条目对公众消失，作者在自己的主页仍能看到标着「已隐藏」的那条，审核者的时间线保留它否则无法撤销
- 时间线按 `id` 倒序游标翻页，`?after=` 取上一页最后一条；只有当页装满才会给出「更早的动态」

---

## 关注与私信

关注是一张 `(follower, followee)` 的边，个人页上 关注/粉丝 两个数字点进去各自的名单；
互相关注时多一个「互相关注」标记。关注是幂等的（重复点不报错），取消第二次会如实说
"你本来就没有关注"。

私信是一对一的纯文本（不走 Markdown，也不渲染 HTML），会话没有独立的表：一条
`(a,b)` 的对话就是两个方向的消息集合。谁能给你发，由**收信人**在
`/settings → 谁可以给我发私信` 里决定：

| 策略 | 谁能发进来 |
|------|-----------|
| `everyone`（默认） | 所有人 |
| `mutual` | 只接受互相关注的人 —— 即"拒绝陌生私信" |
| `following` | 只接受我关注过的人 |
| `off` | 完全关闭私信 |

几点口径要说清：

- 策略只挡**新发进来**的消息，已经存在的对话仍然看得见；关闭私信不影响你自己往外发
- 被拒的发送会当场说明原因（页面提示"对方的隐私设置不接受这条私信"），不会静默丢弃
- 每人每 2 秒 1 条，与动态、登录共用同一张节流表（窗口 2 秒、窗口内 1 条）；正文最长 300 字。被拒的尝试不算消耗配额，所以"发太快"之后等两秒就能发出去
- 导航栏的未读角标只在 cookie 会话上统计（API/bearer 调用不付这次查询），走
  `WHERE read_at IS NULL` 的部分索引；打开对话就是已读回执
- 还没有做：拉黑单个用户、举报、删除消息、群发与图片

---

## 每日打卡

首页上「打卡」按钮，一人一天一次，重复点不重复计。打卡后提示**您已连续打卡 x 天**。

漏卡要罚：隔了 n 天没打开，下次打卡时连续天数扣 `3^n`，扣到不足 1 就归 1（当天这一次
总是算数的）。

| 之前连续 | 漏了几天 | 这次打完 |
|---------:|---------:|---------:|
| 30 | 0 | 31 |
| 30 | 1 | 28 |
| 30 | 2 | 22 |
| 30 | 3 | 4 |
| 30 | 4 天及以上 | 1 |

个人主页显示「连续打卡 x 天（累计 y 天）」。日期按**服务器本地时区**算，所以判机换时区
会让"今天"的边界移动；累计天数按打卡记录的行数，不看连续。

---

## 用例包约定

zip 里按 `<名字>.in` + `<名字>.out` 成对给出，目录层级会被压平（只取文件名）。

- 名字以 `sample` 开头 → 判为**样例**，展示在题面上且不计分
- 其余为**判题用例**，默认每条 1 分，分值在用例管理页逐条改
- 重复上传按名字替换，**序号保持稳定**，判机顺序不会漂移
- 只有 `.in` 没有 `.out` → 整个包拒收并列出缺哪些
- **组名（捆绑）**：同名即同组，组内任何一条没过（或判机没跑完这一组）整组 0 分；组的分值 = 组内各条之和。分数一律由服务器按逐条判定重算，判机谎报 AC 会被降级
- **判题详情级别**（出题表单里选，只管别人看到的，源码与总分始终归提交者自己）：`none` 只给总判定与得分 → `case` 给逐条判定 → `diff` 再加实测/期望与失败用例（默认）→ `full` 再给判机编号；出题人与持 `submission.view.any` 的人始终看到全部
- 单文件上限 `-max-case`（默认 16 MiB），整包上限 `-max-upload`（默认 64 MiB）

zip 只是批量入口：用例页「批量导入 zip」选一个包即可，同名覆盖、新名排在后面。

```bash
zip -j cases.zip 1.in 1.out sample1.in sample1.out
```

日常加一两条不用打包：用例页「加一条」里直接填输入输出。

`Content-Type: application/zip` 不能省：`--data-binary` 默认发 `text/plain`，端点按 zip 读体就会一条用例都收不到，而提交只会回报「本题还没有测试数据」。

---

## 比赛（限时 + 独立榜 + 加入码 + 队伍，OI/IOI/ACM 三种赛制）

比赛只负责「谁在什么时间对哪些题交了什么」。**每题得分仍然由 M13 那套逐用例计分给出**（组内全过才给分），
比赛一层不另起算分逻辑，所以一场比赛的榜和平时练习的得分不会互相打脸。

### 状态与时间

状态**不落库**，永远按当前时间算：`未开始 / 进行中 / 已结束`。没有定时任务，也就没有「到点没改状态」这种事故。
落库的开关只有两个：`榜单公示`（封榜与否）与`出现在列表`（比赛本身是否公开）。

时间是**本站服务器时区**：表单里填 `2026-10-14T20:00` 就按服务器本地时间解释，页面也按同一时区显示。
全站不提供用户时区，所以「主办方填的时间」和「选手看到的时间」是同一个数。

### 计分公式

设某题在题目里的满分为 `T`（判题用例分值之和），比赛里给该题设定的分为 `P`（填 0 表示沿用 `T`）：

```
比赛内该题满分 max = P（P>0 时）或 T
某次提交挣到的分   award = round(raw × P / T)   （P>0 且 T>0）
                      raw                        （否则，raw 就是逐用例得分之和）
```

> 所以「比赛设定分」是**等比缩放**，不是凭空加分：只过了半数分值的人，在比赛榜上也只拿到一半。

三种赛制，别混：

**OI 赛制**（CSP-J/S、NOIP、NOI 用的就是这个，也是默认值）

- **同一题取最后一次提交**——不是最高分。做出来又改坏了，就算改坏的那次
- 总分从高到低；**同分就是同名次**，不比用时，也不比提交次数
- **比赛进行期间不出榜**：除主办方外任何人（包括自己）都看不到榜单，页面说明原因
- 榜上**没有用时列，格子里也不显示尝试次数**：只有每题得分与总分
- 配套用法：比赛期间把题目的「判题详情级别」设成 `none`，才是完整的闭卷

**IOI 赛制**

- 每题取**最高分**那次提交
- 总分降序；**同分再比总用时**（用时 = 各题那次最高分的 `判定时刻 − 开始时刻` 之和，没拿分的题按最后一次尝试计时）
- 格子里给灰字用时，尝试多于一次时一并标出

**ACM 赛制**

- 一题只认**首次 AC**：该题计 1 解题、满分 `max`
- 罚时 `= Σ(首次 AC 时刻 − 开始) + (该题首次 AC 之前的非 AC 判定次数) × penalty_ms`
- `CE` 不罚；`IE`（判机故障）**既不算尝试也不罚**；WA/TLE/MLE/RE 算
- 没 AC 的题只显示尝试次数，不给时间
- 名次键：`解题数 降 → 罚时 升`

**三种共同**：只有 `status=done` 的提交入榜（判机中、排队中、判机超时的都不在榜上）；
名次并列就同名次，下一个名次照常跳过（1 2 2 4）；比赛榜**不影响**全站 `/rank`。

### 队伍

`每队人数 > 1` 时启用队伍赛：

- 加入比赛时没指定队伍，就自动进一支以你自己 handle 命名的队，保证没人掉在榜外
- `op=create / join / leave`：建队并进队、按队名加入、退队
- 榜上一个队伍是**一个竞争者**：每题取队内最优，尝试次数按队求和（所以罚时也是队级）
- 把每队人数**改小不会拆开已有的超编队伍**，只是不再允许新的人加入

### 加入码与封榜

- 加入码留空 = 公开赛，任何人免码加入；填了码 = 私有赛，加入时必须对上码（服务端常数时间比较）
- 加入码**只出现在主办方的编辑表单里**：比赛列表根本不查这一列，主页、榜单、URL、日志里都没有
- 比赛结束后不再收人（防止赛后补进榜单改写比赛结果）；未开始可以先报名
- **封榜**：`榜单公示` 关着时，别人看不到彼此——成员只看到**自己那一行**（名次仍是全场真名次），
  非成员只看到一句说明；主办方看得到完整榜单，并标注「封榜中」
- **结束不会自动放榜**，要在编辑页手动勾「榜单公示」。这是刻意的：忘了关设置就是泄题，多一次点击不会

### 权限

| 操作 | 需要的权限 |
|------|-----------|
| 建比赛 | `contest.create` |
| 改/删自己的比赛、挂题、看加入码 | 该比赛 owner |
| 改/删他人的比赛 | `contest.manage.any` |
| 出题、用题目 | `problem.create` 等原有权限，比赛不额外放行 |

比赛进行中**不会**因为「在比赛里」就多看到别人的源码或逐用例明细：判题详情级别照题目自己的设置走。
比赛内的题目列表只对成员与主办方开放；未公开的题目要挂进比赛，挂的人必须能编辑那道题，
否则公开一场比赛就等于替别人公开了题目。

---

## 帖子（求助贴 / 社区贴 + 评论）

两类帖子，一张表：

| 类型 | 挂题目？ | 从哪进 | 谁在管 |
|------|---------|--------|--------|
| **求助贴** | 必须挂一道题 | 题目页的「求助贴」→ `/problems/{pid}/topics` | 按题目聚合，别人搜题就能看到 |
| **社区贴** | 不允许挂题 | 导航栏「帖子」→ `/topics` | 一个页面统一管理 |

- 「必须 / 不允许」写在表的 `CHECK` 里，不是靠每个调用方记得查——所以不会出现半条挂空的求助贴
- **删除题目会连带删掉它的求助贴与评论**（外键级联），删号同理
- 正文与评论都走题面同一套 Markdown + `$KaTeX$` 渲染管线，转义策略全站只有一份
- **必须登录才能发帖与评论，未登录只能浏览**：写操作一律先回登录页并带上原地址
- 节流：发帖每人每小时 10 帖、评论每人每小时 60 条，与动态分开算，灌水贴刷不掉别人的讨论
- 删除权限：自己写的随时删；删别人的要 `topic.moderate`
- 首页展出**最近 10 条**帖子（两类混合），一行两个方块卡片，窄屏自动退成一列

---

## 工单（用户提单，管理员处理）

与帖子的根本区别是**私密**：`/topics` 谁都能读，`/tickets` 只有作者与持 `ticket.handle` 的人读得到。
申诉里会写"我认为标准答案错了"，举报里会写别人的用户名，这些都不该出现在公开页上。
所以陌生路径返回 **404 而不是 403**——工单的存在本身就是信息。

| 类型 | 用来做什么 |
|------|-----------|
| 申诉 | 判题觉得判错了、数据弱、想请人复核某次提交 |
| 故障建议 | 站点毛病、题目毛病、功能建议 |
| 举报 | 举报某个用户或某次提交 |
| 账号 | 邮箱、封禁、注册之类账号上的事 |
| 其他 | 以上都不是 |

- 可以挂一道题或一次提交（**只能挂自己的提交**，别人的提交号一律报"找不到"）
- 状态 `待处理 → 处理中 → 已解决 / 已关闭`。处理人改状态时顺手记名为经办人；退回待处理会清空经办
- 作者没有 `ticket.handle` 也能**关自己那张**（"我自己解决了"是会发生的事），其余状态改不动
- 改状态写成流水里的一行，和人的回复同一条流：一页看完整个经过
- 导航栏「工单」对登录用户出现，**未处理数只给处理人算**（普通用户看不到队列有多大）
- 速率：每人每小时 5 张工单、30 条回复
- 通知：没有邮件也没有推送，处理人靠导航栏那个角标。要做提醒算下一期

---

## 身份与权限

| 凭证 | 怎么来 | 能干什么 |
|------|--------|----------|
| `-admin-token` | 启动参数 | 运维引导凭据：驱动判机队列、以及在 `/admin/claim` 给第一个账号设密码。不是日常登录方式 |
| `-judge-token` | 启动参数 | 领取任务、取用例正文、回报结果 |
| 邮箱 + 密码 | `/register` 自助注册 | 登录成普通用户（session cookie） |

登录与会话：

- 注册要邮箱 + 用户名 + 密码（≥10 位，bcrypt cost 默认 11）；**邮箱暂不发送验证邮件**，注册即可用
- 登录可用邮箱或用户名；失败提示统一为「账号或密码不正确」，不区分哪一步错，避免枚举账号
- 同一来源 IP 在 15 分钟内失败 10 次返回 429；计数在库里，重启不会把锁定清零
- session cookie `fastoj_session`：`HttpOnly` + `SameSite=Lax` + `Path=/`，30 天；上 TLS 时加 `-secure-cookies`
- 每次登录新建 session；改密码吊销该用户全部 session
- **CSRF**：非 GET 请求统一过闸门，只有「凭 cookie 认证」的请求被要求带 `csrf` 字段或 `X-CSRF-Token` 头，与 session 里的 token 常量时间比较；`Authorization: Bearer` 的 API 调用不带 cookie、天然免疫，不做要求
- `/login?next=` 只认站内路径，`//evil`、`/\evil`、绝对 URL 一律回落到 `/`
- 引导令牌与判机令牌都做常量时间比较；除此之外系统里没有第二种凭据
- 隐藏题对无权限者一律 404 而不是 403，避免被枚举
- 提交的判定结果公开；源码、逐用例明细、判机差异信息只有本人或管理员可见
- 关闭注册：`-no-register` 与后台 `/admin/site` 的「允许自助注册」取与，任一方关掉后 `/register` 即 403
- 个人简介（`/settings`）与动态正文走题面同一条 Markdown 安全管线，见[首页、站点设置与动态](#首页站点设置与动态)

### 细粒度权限

不是"普通用户 / 管理员"两档了事：每个能力是一项可以单独授予的权限，网页控制台 `/admin/perms` 逐项开关。

| 权限键 | 含义 |
|--------|------|
| `problem.create` | 创建题目 |
| `problem.edit.any` | 编辑他人创建的题目 |
| `problem.publish` | 公开 / 隐藏题目（作者本人也不能隐藏自己的题，需要这一项） |
| `testcase.manage` | 管理他人题目的用例 |
| `contest.create` | 创建比赛 |
| `contest.manage.any` | 管理他人创建的比赛 |
| `user.manage` | 改角色、封禁 / 解封 |
| `permission.manage` | 授予与回收上面这些权限 |
| `judge.view` | 查看判机框与队列 |
| `feed.moderate` | 删除他人动态 |
| `topic.moderate` | 删除他人帖子与评论 |
| `ticket.handle` | 处理工单：看全部、回复、改状态、删单 |
| `site.settings` | 修改站点设置与首页 |
| `submission.view.any` | 查看他人源码与逐用例明细（作弊调查用） |

规则：

- 题目记录 `owner_id`。**创建者天然能改自己的题**，不必持有 `problem.edit.any`；`testcase.manage` 是给"只维护测试数据"的人的独立通道
- 生效权限 = 角色默认 ⊕ 个人覆盖（个人覆盖双向生效，可以只从管理员手里收回某一项）
- **控制台拒绝修改你自己**：改自己的权限/角色/状态一律 400，避免把自己锁在门外
- 封禁（`status='banned'`）立即吊销该账号全部会话；解封不需要重设密码
- 改角色后能力集在下一个请求就重算，不需要对方重新登录
- 拒绝时回答里会点名缺哪一项（例如 `缺少权限：problem.publish`），方便对接脚本

建题、传用例、看判题数据现在都可以只给到"能出题但不能隐藏题"这种粒度；`-admin-token` 对应的 admin 账号默认持有全部项。

---

## 判机协议与队列

判机端只出站连接服务器，四个动作：

```
POST /judge/claim                       → 心跳注册 + 领取（404 = 队列空）
GET  /judge/cases/{id}/{in|out}         → 逐个取用例
POST /judge/submissions/{id}/status     → compiling → running
POST /judge/submissions/{id}/result     → 判定 + 逐用例行
```

服务器侧的规则：

- **领取是单条语句** `UPDATE … WHERE id=(SELECT … WHERE status='queued' LIMIT 1) RETURNING`，两台判机抢同一份提交时后者必然 0 行受影响；4 框并发抢 24 条实测零重复
- 状态迁移是 CAS：`UPDATE … WHERE id=? AND status=?`，`judge.Transition` 列出合法迁移并与 schema 的 `CHECK` 对齐
- 判机中途死掉 → 按 `started_at` 租约回收成 `abandoned` + `IE`；`RequeueAbandoned` 只允许把 abandoned 放回队尾，已成定局的提交不能被重新排队
- **分数不信任判机的算术**：`tallyResult` 按库里的用例权重重算；谎报 AC 但逐用例行里有非 AC → 降级成那个判定；消失的用例行也不能证明 AC；认不出的 verdict 字符串一律记 `IE`（判机故障不该算提交者的错）
- 判机拿不到结果时也必须回报 `IE`，不能让提交卡在 `compiling` 等租约

---

## 判机隔离（边界必须说清）

当前支持语言：**C / C++**（`g++ -x c++ -std=c++17 -O2 -w -static`）。命令一律是 argv 数组，绝不拼 shell 字符串。

Windows 本地判机（`runner_windows.go` + `runner.go`）：

- 内存上限用 Job Object 落实：`JOB_OBJECT_LIMIT_PROCESS_MEMORY` 管单进程，`JOB_OBJECT_LIMIT_JOB_MEMORY` 管整棵树（上限的 4 倍），实测 32 MB 上限下 `malloc(512MB)` 直接返回 `NULL`；无上限时如实报出峰值 526 MB
- **进程数上限** `JOB_OBJECT_LIMIT_ACTIVE_PROCESS`（提交默认 4，编译 16）：没有它，一个 `fork` 循环可以躲在单进程内存上限下面把机器的进程表和 CPU 吃光
- CPU 时间取进程退出状态里的 FILETIME，内存取 Job 的 `PeakProcessMemoryUsed`
- 超时用 wall-clock 兜底（`max(2×时限, 2s)`）并整树击杀 —— 只限 CPU 的话，一个 `sleep` 型程序能把判机槽位永久占死
- **环境变量一律不继承**：判题进程只看到 `PATH`/`SystemRoot`/`WINDIR` 与指向临时工作目录的 `TEMP`/`TMP`/`HOME`/`USERPROFILE`。fast-judge 自己的令牌、数据库路径、服务单元导出的任何秘密，`getenv` 都读不到
- 启动时把边界打印一次，避免有人在日常使用的机器上把判机当成沙箱
- **Windows 上内存超限通常表现为 `RE`（分配被拒）而不是 `MLE`**，Linux sandbox 才给得出明确的 MLE

**Windows 上仍未隔离：文件系统、网络、以及"能不能弹窗口到你的桌面"。** 判题程序就是以 fast-judge 那个用户的身份在跑，能读写该用户能读写的一切，`system("shutdown -s -t 3600")` 这类调用会真的被系统接受 —— Job Object 的 UI 限制在 Vista 之后就被 Windows 作废了，`shutdown.exe` 也不是靠 `ExitWindowsEx` 的句柄限制能拦的。Linux 上没有这个问题：判题沙盒已经把网络与文件系统这两道关上了（见下）。

| 你要的强度 | 做法 |
|-----------|------|
| 本机自测、只跑自己的题 | 现在这样就够 |
| 接受陌生人提交 | Linux 上开沙盒（默认就开），并给它一个专用账号或容器；**别在登录用的账号上跑 fast-judge**（Windows 上判机没有这层保护） |
| 生产 | 判机放 Linux 容器 + 开沙盒（`docker-compose.judge.yml` 已经把需要的 cap 加好了）；要连内核攻击面一起收，再叠 seccomp（尚未做，见[已知限制](#已知限制)） |

### 判题沙盒（`internal/jail`：一次运行一个牢房）

Linux 上每次判题运行都被关进一组命名空间，靠的是**判机 exec 自己**：Go 没法在 fork 与
exec 之间插东西，所以 `setrlimit`、`mount`、`chroot` 这些必须由子进程在 exec 提交程序
之前自己做完。牢房里：

| 面 | 手段 | 实测结果 |
|----|------|---------|
| 网络 | `CLONE_NEWNET` | 只剩一条 DOWN 的 lo；`connect(8.8.8.8:53)` 返回 `ENETUNREACH`。**阿里云元数据 `100.100.100.200` 一并废掉** |
| 目录 | `CLONE_NEWNS` + 把系统目录只读 bind + `chroot` 到这次运行的工作目录 | `/etc/passwd`、`/var`、别人的源码都不存在；`fopen("/etc/xxx","w")` 失败 |
| 进程 | `CLONE_NEWIPC`/`CLONE_NEWUTS` + `RLIMIT_NPROC` | fork 炸弹被内核掐住（实测 400 次 fork 在 8 的额度下被判 TLE 并整组杀掉） |
| 时间 | 判机墙上时钟 + 整组击杀，另有 `RLIMIT_CPU` 宽两秒兜底 | 死循环仍是 `TLE`，且给出实测值 |
| 内存 | 判机实测 `rusage` + `/proc` 轮询熔断，`RLIMIT_AS` 只留 512 MB 余量 | 吃 300 MB 而限 64 MB 仍是 **`MLE`**，不会被抢先打成 `RE` |
| 身份 | root 判机降到 `-run-as` 的 uid；非 root 判机走非特权 user namespace，最后落到一个**未映射**的 uid | 宿主看到的是 nobody，判机的库、令牌、别人的提交都读不到 |

`-sandbox=auto`（默认）能开就开，开不起来就在启动日志与 `/judges` 上写明"未隔离"，不影响判题；
`-sandbox=on` 是硬要求，建不出牢房就每条判题报 `IE` 并说明原因——**不会悄悄按不隔离的路子重跑**。
牢房建不起来时判机报 `IE` 而不是 `RE`：内核不给权限，不该让学生去查自己的代码。

两个实测踩过的坑，改这块代码前先看：

- **不能开 `CLONE_NEWPID`**。Go 是 fork 之后再 `unshare()` 的，而 `unshare(CLONE_NEWPID)`
  只把调用者的**子进程**放进新 pid 命名空间，被 exec 的程序自己还在原来那个；留着它
  拿不到好处，还会让被 exec 起来的 Go 程序（判机自己）在起线程时 `EINVAL`。
- **`RLIMIT_AS` 不能卡到判定线上**。卡紧之后超内存的申请直接 `ENOMEM` 崩掉，判定从
  `MLE` 变成 `RE`，等于用安全性换掉了判题口径。

判机容器要多给三个能力：`SYS_ADMIN` + `NET_ADMIN`（开 mount/network 命名空间）、
`SYS_PTRACE`（读降权子进程的 `/proc/<pid>/statm`，内存熔断靠它）。Docker **默认 seccomp
profile 不需要放开**，实测照跑。

### 判机容器化（`Dockerfile.judge` + `docker-compose.judge.yml`）

把提交代码关进容器，是现在最省事的一层真隔离：Docker Desktop 的容器跑在轻量 VM 里，
判题程序连你登录用的账号都不是同一个。

```bash
# 1. 服务器照旧在宿主上跑
./dist/fast-oj.exe -addr 127.0.0.1:18080 -data data -admin-token ADM -judge-token JDG

# 2. 交叉编译 + 构建镜像 + 起容器（只需出站连接，没暴露任何端口）
FASTOJ_JUDGE_TOKEN=JDG bash scripts/judge.docker.sh          # 本地已有镜像时直接复用
REBUILD=1 FASTOJ_JUDGE_TOKEN=JDG bash scripts/judge.docker.sh  # 装了新工具链时强制重建
```

`docker build` 每次都要向 Docker Hub 取 `FROM` 的元数据，网络不通就整镜像失败；脚本
因此带了一条离线回退：把新编译的二进制 `docker cp` 进临时容器再 `docker commit`
（commit 时显式写回 `ENTRYPOINT`，否则会把临时容器的 `sleep` 提交进去）。等价于只重跑
那一层 COPY，工具链没变时完全够用。

容器里跑的约束（下面这套已在 Docker Desktop 29.7.2 / linux 引擎上实测）：

| 手段 | 挡住什么 |
|------|----------|
| `cap_drop ALL` + `cap_add SETUID,SETGID,KILL,SYS_ADMIN,NET_ADMIN,SYS_PTRACE`，`no-new-privileges` | 只保留"把提交降权""杀掉它""开牢房的那三个能力"，其余一律没有 |
| `-run-as 65534:65534`（判机 root 起步，提交降到 nobody） | 提交读不到判机进程的环境（`/proc/1/environ`）与判机令牌，跨 uid 之后内核直接拒绝 |
| `read_only` 根文件系统 + `/work` tmpfs（512 MiB，**必须带 `exec`**） | 改不了镜像里的任何东西；写盘有额度。Docker 给 `--tmpfs` 默认挂 `noexec`，而编译产物就在 `/work`，漏写 `exec` 时判题全部 `IE` |
| `mem_limit` / `memswap_limit`（不给 swap） | 超内存被 cgroup 拦下，实测 512 MiB 的提交报出 524940 KB 并判 `MLE` |
| `pids_limit 256` + `--ulimit nproc=128` | fork 炸弹 |
| `--ulimit fsize=64MiB`、`core=0`、`cpu=120` | 写满磁盘、留下 core 文件、CPU 兜底 |
| 判机侧 `Setpgid` + 整组击杀 | 超时的进程树不会变成孤儿继续吃 CPU |

判机以前不发 rlimit 给子进程：Go 在 Linux 上没有"spawn 时带 rlimit"的通道
（`SysProcAttr.Rlimits` 只有 BSD 有），而绕道 shell 的 `ulimit` 会让判定结果依赖镜像里
装的是 dash 还是 bash。现在这一条由**牢房**解决：jailer 在 `exec` 提交程序之前自己
`setrlimit`，所以内核层面有硬顶，而判定仍然来自判机的**测量**（CPU 取 `wait4` 的
rusage，内存取 `ru_maxrss`），Linux 容器的 `MLE` 因此比 Windows 的 `RE` 更准确。
cgroup 仍然是最外面那层（`mem_limit`/`pids_limit`），牢房是它里面一层。

判定口径（`judge.Run`）：

- **实测 CPU 时间超过本题时限就是 `TLE`**，即使程序自己正常退出了。原来只有 wall-clock
  到点才给 TLE，一个跑掉 1038ms 却 `return 0` 的程序会被判成 AC。
- 每个测试点允许多跑 **50 ms** 才被砍（`wall = 时限 + 50ms`，最小 250 ms 兜住进程创建）。
  这 50 ms 不是为了放宽时限，是为了让程序**跑完到能被测量**：1000ms 的题，
  1050 / 1038 / 1001ms 都算超时，各自报出自己的数字。
- 被中途杀掉的运行只能给出**下界**，网页与文本视图给它们加 `>` 前缀：
  `>1050ms`、`>289.00M`；自然跑完的运行仍然显示精确值。
- 内存不再等容器 OOM：判机每 20 ms 轮询一次峰值，超过**本题上限 + 16 MB** 立刻整组击杀
  并判 `MLE`（Linux 读 `/proc/<pid>/statm`，Windows 读 Job 的峰值）。实测 256 MB 上限的题，
  一个申请 512 MB 的程序在 289 MB 处被砍，报 `>289.00M`。

两个坑是实测踩出来的，都进了代码与测试：

- **`cap_add` 里少了 `KILL`**：降权之后判机（uid 0）给 uid 65534 的子进程发 SIGKILL 会
  `EPERM`。表现是超时的程序杀不掉、判机卡在输出管道上、整条队列停摆（要等容器
  `--ulimit cpu` 收尸）。现在 `Kill` 会把错误带出来，并且最多再等 5 秒就带着
  `TLE` 与原因返回，绝不无限占着判机槽位；回归用例是 `TestDroppedSubmissionStillTimesOut`
  （需要 root 判机，用 `FASTOJ_TEST_RUN_AS_UID=65534` 打开）。
- **`internal: true` 网络不能用**：它把 `host.docker.internal` 一起挡了
  （`dial tcp 192.168.65.254:18080: network is unreachable`），判机领不到工单。所以
  compose 里默认不开，**提交程序的出口网络目前没有被限制**——要单独掐掉，得给每次运行
  开一个无网络的命名空间（容器内 root + `CAP_NET_ADMIN` + `unshare -n`，再降权），
  这条归到外部 sandbox 那一期。挡住的关键面仍然是：宿主的文件系统、进程与关机权限。

输出：每路最多 1 MiB，超出即按 `WA` 判（已经打印出来的内容不可能再对上答案）。

---

## 数据与备份

```
data/
├── fast-oj.db        SQLite（WAL 模式，迁移内嵌在二进制里，启动即自动建库）
├── fast-oj.db-wal
├── fast-oj.db-shm
└── cases/<题目id>/<序号>.in|.out    用例正文，库里只存相对路径
```

停掉服务后整目录拷贝就是备份；不想停服务用 `sqlite3 data/fast-oj.db ".backup 备份路径"`。用例正文是散文件，一起拷。

---

## 参数

服务端 `fast-oj`（同名环境变量在括号里）：

| 参数 | 默认 | 说明 |
|------|------|------|
| `-addr` | `127.0.0.1:8080` | 监听地址（`FASTOJ_ADDR`） |
| `-data` | `./data` | 数据目录（`FASTOJ_DATA`） |
| `-db` | `<data>/fast-oj.db` | 单独指定库文件（`FASTOJ_DB`） |
| `-admin-token` | 必填 | 管理令牌（`FASTOJ_ADMIN_TOKEN`） |
| `-judge-token` | 必填 | 判机令牌（`FASTOJ_JUDGE_TOKEN`） |
| `-max-source` | 262144 | 源码/JSON 请求体上限 |
| `-max-upload` | 64 MiB | 用例包上限 |
| `-max-case` | 16 MiB | 单个用例文件上限 |
| `-bcrypt-cost` | 11 | 口令哈希强度（10 偏弱，14 登录会明显卡） |
| `-secure-cookies` | 关 | 给 session cookie 加 `Secure`，上 TLS 后打开 |
| `-no-register` | 关 | 关闭自助注册（与 `/admin/site` 的开关取与） |
| `-v` | 关 | 记录每个请求（默认只记失败与 >50ms 的请求，以及登录失败） |

判机端 `fast-judge`：

| 参数 | 默认 | 说明 |
|------|------|------|
| `-server` | `http://127.0.0.1:8080` | 服务器地址（`FASTOJ_SERVER`） |
| `-token` | 必填 | 判机令牌（`FASTOJ_JUDGE_TOKEN`） |
| `-name` | 主机名 | 判机框名字（`FASTOJ_JUDGE_NAME`） |
| `-dir` | 用户缓存目录 | 工作目录（`FASTOJ_JUDGE_DIR`），每次判题用 `sid` 子目录，跑完即删 |
| `-poll` | 500ms | 空队列轮询间隔 |
| `-backoff` | 3s | 出错后的退避 |
| `-tools` | 全部可用 | 只申报指定语言 |
| `-run-as uid:gid` | 空 | root 判机把提交降到这个 uid（`FASTOJ_JUDGE_RUN_AS`）；Windows 上直接拒绝启动。开了沙盒时它是牢房外的第二道身份 |
| `-sandbox` | `auto` | 判题沙盒：`auto` 能开就开、开不了就在 `/judges` 标注；`on` 建不出牢房就不判（报 `IE`）；`off` 明确不隔离（`FASTOJ_JUDGE_SANDBOX`） |
| `-once` | — | 最多判一条就退出 |

---

## 开发

```bash
go test ./...            # 含真起 g++ 判题的端到端用例
go vet ./...
gofmt -l .
powershell -File build.ps1 -Test
```

- 模块代理：国内直连 `proxy.golang.org` 会超时，已按 `go env -w GOPROXY=https://goproxy.cn,direct` 配置
- 依赖只有三个：`modernc.org/sqlite`（纯 Go，无 CGO）、`gomarkdown/markdown`、`golang.org/x/sys`
- 交叉编译：`build.ps1` 默认 `GOARCH=amd64`；`-Arch 386` 出 32 位判机框版本
- 三个脚本各管一台机器：`run.sh`（Ubuntu/Debian 一键：查依赖 + 编译 + 服务器带判机）、`build.ps1`（Windows 编译）、`scripts/judge.docker.sh`（判机装进容器）
- 包结构：`internal/api`（HTTP + 页面）、`internal/store`（SQLite + 迁移 + 队列）、`internal/judge`（状态机 + 执行层）、`internal/judgeagent`（判机端）、`internal/protocol`（两端共享的线上类型）、`internal/casefs`（用例落盘）、`internal/config`

---

## 性能

`go test -run XXX -bench . ./internal/api/` 与 `TestReadPathP99Budget`，数据规模 200 道题 / 每道 5 个用例 / 500 条提交，Windows 回环，i7-10700KF：

| 路径 | 每次请求 | p50 | p99 |
|------|----------|-----|-----|
| `/healthz` | 0.16 ms | <1 ms | 1.0 ms |
| `/problems?limit=50` | 0.86 ms | 1.0 ms | 2.2 ms |
| `/problems/{pid}`（含 Markdown 渲染） | 0.55 ms | 1.0 ms | 1.5 ms |
| `/submissions?limit=50` | 0.62 ms | 1.0 ms | 1.7 ms |
| `/rank` | 1.15 ms | 1.0 ms | 2.0 ms |

端到端 p50/p99 被 Windows 约 1 ms 的计时粒度顶住，所以看起来都 ≥1 ms；真正的工作量在"每次请求"那一列。

支撑这些数字的取舍：

- 列表一律游标分页（`WHERE id > ?`），不用 `OFFSET`，深页和第一页同价
- 列表查询不取 `source` / `detail` 大字段，只有单条且有权查看时才二次取回
- 待判队列走 `status='queued'` 部分索引
- 一次查询取回整页的用例数/通过数，不做 N+1
- WAL + 预编译语句 + 无 ORM 运行时
- 访问日志默认只记失败与 >50 ms 的请求，日志不吃掉延迟

已知会先劣化的地方：`/rank` 用相关子查询按用户聚合，用户数上千后需要改成物化计数列。

### 浏览器侧的账（F12 限速 3G）

后端 1 ms 级别不代表页面快。Chrome 的 Fast 3G 是 1.6 Mbps（≈200 KB/s）+ 150 ms RTT，
在这个档位下首字节 14 ms、整页 P99 却要 2.3 s——差的那部分是**下载量**：

| 项目 | 改前 | 改后 |
|------|------|------|
| 页面 HTML | 3–11 KB 原样发送 | gzip 后 1.5–2 KB（`/submissions` 10.8 KB → 1.55 KB） |
| `style.css` + `math.js` | 5 KB + 0.9 KB，`max-age=300` | gzip + `?v=<内容哈希>` + `immutable` 一年缓存 |
| KaTeX（CSS 23 KB + JS 275 KB + 字体 100–180 KB） | **每一页都从 CDN 拉** | 只有页面上真的存在公式节点时才拉 |

所以现在的规则是：`/problems`、`/submissions`、`/rank`、登录注册这些没有公式的页面，
一次外部请求都不发（实测网络面板只剩本站两个资源）；只有题面、写了公式的页脚/首页语
那类页面才付 KaTeX 的钱。加载逻辑在 `static/math.js`：模板里不留任何 CDN 标签，由脚本
发现 `span.math` 之后再注入 CSS 与 JS，CDN 不可达时原始 TeX 仍然可读。

还要再快可以做两件事：把 KaTeX 与其字体一并嵌进二进制（离线可用，省掉到第三方
的 DNS + TLS 握手，代价是二进制大约再胖 1.5 MB），以及把关键 CSS 内联进 HTML
（省一个 RTT）。

---

## 已知限制与后续

| 事项 | 现状 |
|------|------|
| 语言 | 只有 C/C++；Python 判机的时间/内存测量另说 |
| 判机隔离 | Linux 上每次运行都有命名空间牢房（无网络、看不到宿主目录、内核 rlimit 兜 CPU/内存/进程数），root 与非 root 两种部署都能开；容器再套一层。仍**没有**：seccomp-bpf 系统调用白名单（牢房里仍能 `execve`/`ptrace`，只是无网、无宿主文件、无权限）、pid 命名空间（Go 的 `unshare` 语义拿不到，见[判题沙盒](#判题沙盒-internaljail一次运行一个牢房)）、Windows 上的任何文件/网络隔离（平台没有这套机制，`-sandbox=on` 会直接拒绝启动） |
| 账号 | 注册可关；邮箱验证与「忘记密码」重置链接还没做，忘了密码要管理员改库 |
| 工单 | 已做（五类、四状态、私密、流水、处理人角标）。仍**没有**：新工单的邮件/推送提醒、按人分派与 SLA、附件上传（正文能写 Markdown，贴图得靠外链） |
| 比赛 | 已做（限时、加入码私有、队伍、OI/IOI/ACM 三种赛制独立榜、封榜）。OI 比赛中不出榜。仍**没有**：虚拟排名（参赛者不插进别人的榜）、比赛中改时限或换数据后对历史提交的重算、队长踢人/转让队长的完整 UI、比赛内单独的时限配置（用题目自己的时限）；时间全站按服务器时区，没有用户时区 |
| 升级 0015 | 老比赛里 `mode='oi'` 的**算法口径变了**（0013 那版算的其实是 IOI 的最高分）。数据不迁移：表结构只是多认一个 `'ioi'`，历史 `oi` 比赛从这次起按「最后一次提交、同分同名次、赛中不出榜」算，主办方要复核老比赛的赛制选择 |
| 比赛封榜 | 结束**不自动放榜**，要主办方手动勾「榜单公示」——忘了关是泄题，多一次点击不会 |
| 统计 | 排行榜只有「按去重 AC 题数」，无难度、标签、通过率排序 |
| 防作弊 | 无代码查重；登录/注册/发动态有节流，提交与建题还没有速率限制 |
| 附件与图片 | 题面不能贴图（外链可以），无附件上传 |
| 静态资源 | 已按内容哈希加 `immutable` 一年缓存；模板与样式仍内嵌在二进制里，改样式要重新编译并重启 |
