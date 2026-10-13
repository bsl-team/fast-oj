# fast-oj

超级无敌简洁的 OJ，UI 极少。**只有网页**：人是唯一的使用者，出题、用例、判题、私信、后台全在浏览器里点。

- 单个可执行文件，零外部服务依赖（SQLite 内嵌，纯 Go 驱动）
- 判机与服务器分离，判机只需要一个出站 HTTP 连接（那是判机的内部协议，网页上没有入口）
- 题面、动态、个人简介、首页语统一走 Markdown + KaTeX 数学公式
- 首页内容（站名、首页语、友情链接、页脚、页脚 API 提示、注册开关）在网页后台改，不用重启
- 页面每次请求的处理时间在 0.2–3 ms 之间（200 题 / 500 提交规模，见[性能](#性能)）

---

## 30 秒跑起来

```powershell
powershell -File build.ps1        # 产出 dist\fast-oj.exe dist\fast-judge.exe
```

```bash
# 1. 服务端（两个令牌必填，留空会拒绝启动）
./dist/fast-oj.exe -addr 127.0.0.1:8080 -data data -admin-token ADM -judge-token JDG

# 2. 判机端（需要 g++ 在 PATH 上，可以和服务器同机）
./dist/fast-judge.exe -server http://127.0.0.1:8080 -token JDG -name box1 -v
```

浏览器打开 <http://127.0.0.1:8080>。命令行只留一件事——探活：

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
| `GET` | `/rank` | 公开 | 排行榜 |
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
- 单文件上限 `-max-case`（默认 16 MiB），整包上限 `-max-upload`（默认 64 MiB）

zip 只是批量入口：用例页「批量导入 zip」选一个包即可，同名覆盖、新名排在后面。

```bash
zip -j cases.zip 1.in 1.out sample1.in sample1.out
```

日常加一两条不用打包：用例页「加一条」里直接填输入输出。

`Content-Type: application/zip` 不能省：`--data-binary` 默认发 `text/plain`，端点按 zip 读体就会一条用例都收不到，而提交只会回报「本题还没有测试数据」。

---

## 身份与权限

| 凭证 | 怎么来 | 能干什么 |
|------|--------|----------|
| `-admin-token` | 启动参数 | 运维引导凭据：给第一个账号发权限、手动驱动判机队列。不是人用的登录方式 |
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

**仍未隔离：文件系统、网络、以及"能不能弹窗口到你的桌面"。** 判题程序就是以 fast-judge 那个用户的身份在跑，能读写该用户能读写的一切，`system("shutdown -s -t 3600")` 这类调用会真的被系统接受 —— Job Object 的 UI 限制在 Vista 之后就被 Windows 作废了，`shutdown.exe` 也不是靠 `ExitWindowsEx` 的句柄限制能拦的。所以：

| 你要的强度 | 做法 |
|-----------|------|
| 本机自测、只跑自己的题 | 现在这样就够 |
| 接受陌生人提交 | **别在登录用的账号上跑 fast-judge**。给它一个专用本地账号（判机账号对 `data\` 无权限，就读不到题库、源码与用户表），或直接上虚拟机/容器 |
| 生产 | 判机放 Linux + 外部 sandbox（HOJ/exejail 那一类）。`judge.Guard` 接口已经留好，`runner_other.go` 是当前占位实现（只有超时击杀） |

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
| `cap_drop ALL` + `cap_add SETUID,SETGID,KILL`，`no-new-privileges` | 只保留"把提交降权"和"杀掉它"这两种能力，其余一律没有 |
| `-run-as 65534:65534`（判机 root 起步，提交降到 nobody） | 提交读不到判机进程的环境（`/proc/1/environ`）与判机令牌，跨 uid 之后内核直接拒绝 |
| `read_only` 根文件系统 + `/work` tmpfs（512 MiB，**必须带 `exec`**） | 改不了镜像里的任何东西；写盘有额度。Docker 给 `--tmpfs` 默认挂 `noexec`，而编译产物就在 `/work`，漏写 `exec` 时判题全部 `IE` |
| `mem_limit` / `memswap_limit`（不给 swap） | 超内存被 cgroup 拦下，实测 512 MiB 的提交报出 524940 KB 并判 `MLE` |
| `pids_limit 256` + `--ulimit nproc=128` | fork 炸弹 |
| `--ulimit fsize=64MiB`、`core=0`、`cpu=120` | 写满磁盘、留下 core 文件、CPU 兜底 |
| 判机侧 `Setpgid` + 整组击杀 | 超时的进程树不会变成孤儿继续吃 CPU |

判机自己不发 rlimit 给子进程：Go 在 Linux 上没有"spawn 时带 rlimit"的通道
（`SysProcAttr.Rlimits` 只有 BSD 有），而绕道 shell 的 `ulimit` 会让判定结果依赖镜像里
装的是 dash 还是 bash。硬上限由 cgroup 承担，判机负责**测量**（CPU 取 `wait4` 的
rusage，内存取 `ru_maxrss`）并据此出判定，所以 Linux 容器的 `MLE` 比 Windows 的
`RE` 更准确。

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
- 包结构：`internal/api`（HTTP + 三种视图）、`internal/store`（SQLite + 迁移 + 队列）、`internal/judge`（状态机 + 执行层）、`internal/judgeagent`（判机端）、`internal/protocol`（两端共享的线上类型）、`internal/casefs`（用例落盘）、`internal/config`

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
| 判机隔离 | 容器化判机已就位（`Dockerfile.judge` + `docker-compose.judge.yml`）；不带容器直接跑在 Windows 上时只限内存/CPU/进程数，**文件系统、网络、桌面不隔离**，所以别在日常登录的账号上跑判机 |
| 账号 | 注册可关；邮箱验证与「忘记密码」重置链接还没做，忘了密码要管理员改库 |
| 建题 | 只能走 API（网页管理界面未做） |
| 竞赛/比赛 | 未做 |
| 统计 | 排行榜只有「按去重 AC 题数」，无难度、标签、通过率排序 |
| 防作弊 | 无代码查重；登录/注册/发动态有节流，提交与建题还没有速率限制 |
| 附件与图片 | 题面不能贴图（外链可以），无附件上传 |
| 静态资源 | 已按内容哈希加 `immutable` 一年缓存；模板与样式仍内嵌在二进制里，改样式要重新编译并重启 |
