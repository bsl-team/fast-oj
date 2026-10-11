# fast-oj

超级无敌简洁的 OJ，UI 极少。同一个 URL 对浏览器给 HTML、对 `curl` 给纯文本、对程序给 JSON —— 权限判定只有一条代码路径，网页不会比 API 多泄露任何东西。

- 单个可执行文件，零外部服务依赖（SQLite 内嵌，纯 Go 驱动）
- 判机与服务器分离，判机只需要一个出站 HTTP 连接
- 题面、动态、个人简介、首页语统一走 Markdown + KaTeX 数学公式
- 首页内容（站名、首页语、友情链接、页脚、注册开关）在网页后台改，不用重启
- 读接口每次请求的处理时间在 0.2–1.2 ms 之间（200 题 / 500 提交规模，见[性能](#性能)）

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

浏览器打开 <http://127.0.0.1:8080>，命令行：

```bash
curl -s localhost:8080/healthz          # ok
curl -s localhost:8080/about            # 版本、数据目录、WAL、各项计数
curl -s localhost:8080/problems         # 纯文本题库列表
```

现成的演示脚本：

| 脚本 | 作用 |
|------|------|
| `bash scripts/smoke.sh` | 两个 exe + curl 走完整流程：建题 → 传用例 → 发令牌 → 提交 → 看 AC/WA |
| `bash scripts/seed_web.sh` | 起一套带中文题面 + KaTeX 公式的示例库并在 18080 留着，供浏览器查看 |

---

## 三种视图

| 请求 | 结果 |
|------|------|
| 浏览器（`Accept: text/html`） | HTML 页面 |
| `curl`（默认） | `text/plain` |
| `?format=json` 或 `Accept: application/json` | JSON |
| `?format=text` | 强制纯文本 |

网页版页面：首页 `/`（站名 + 首页语 + 统计 + 最新题目 + 动态；curl 与 `?format=json` 拿到的仍是题库列表）、题库 `/problems`、题面 `/problems/{pid}`、动态 `/feed`、提交列表 `/submissions`、提交详情 `/submissions/{id}`、排行榜 `/rank`、个人主页 `/u/{handle}`、登录注册 `/login` `/register`、设置 `/settings`、站点设置 `/admin/site`、权限控制台 `/admin/perms`、令牌绑定 `/account`。

---

## 接口一览

| 方法 | 路径 | 权限 | 说明 |
|------|------|------|------|
| `GET` | `/healthz` | 公开 | 探活，会真连一次数据库 |
| `GET` | `/about` | 公开 | 运行摘要与全部路由 |
| `GET` | `/` | 公开 | 浏览器给首页，curl/JSON 给题库列表 |
| `GET` | `/problems` | 公开 | 列表，`?q=` `?limit=` `?after=` |
| `GET` | `/problems/{key}` | 公开 | 题面 + 样例（`key` 可为编号或数字 id） |
| `POST` | `/problems` | `problem.create` | 建题（记录 owner） |
| `PUT` | `/problems/{key}` | 该题 owner 或 `problem.edit.any` | 改题（只改发来的字段） |
| `DELETE` | `/problems/{key}` | 该题 owner 或 `problem.edit.any` | 删题（连带用例文件） |
| `POST` | `/problems/{key}/cases` | owner 或 `testcase.manage` | 上传用例 zip |
| `GET` | `/problems/{key}/cases` | owner 或 `testcase.manage` | 用例清单与大小 |
| `DELETE` | `/problems/{key}/cases` | owner 或 `testcase.manage` | 清空用例 |
| `GET` | `/problems/{key}/cases/{name}/{in\|out}` | 样例公开，判题需 admin/判机 | 取单个用例正文 |
| `POST` | `/problems/{key}/submit` | 登录用户 | 网页表单提交 |
| `POST` | `/submissions` | 用户令牌 | 提交代码 |
| `GET` | `/submissions` | 公开 | 列表，`?user=`（或 `me`）`&problem=&limit=&after=` |
| `GET` | `/submissions/{id}` | 公开 | 结果；`?wait=秒` 长轮询到出结论（上限 5 分钟） |
| `GET` | `/rank` | 公开 | 排行榜 |
| `GET` | `/feed` | 公开 | 动态时间线，`?user=` `?after=` `?limit=` |
| `POST` | `/feed` | 登录用户 | 发一条动态（每人每小时 20 条） |
| `POST` | `/feed/{id}/delete` | 本人或 `feed.moderate` | 删除 |
| `POST` | `/feed/{id}/hide` | `feed.moderate` | 隐藏 / 恢复（不删数据） |
| `GET` | `/u/{handle}` | 公开 | 个人主页 |
| `GET` | `/whoami` | 令牌 | 当前身份 |
| `GET` `/POST` | `/login` `/logout` | — | 邮箱或用户名 + 密码登录，session cookie |
| `GET` `/POST` | `/register` | 公开（可关） | 自助注册，邮箱暂不验证 |
| `GET` `/POST` | `/settings` `/settings/*` | 登录用户 | 资料、密码、API 令牌签发与吊销 |
| `POST` | `/users` | `user.manage` | 建/取用户 |
| `GET` | `/users` | `user.manage` | 用户列表 |
| `POST` | `/users/{handle}/tokens` | `user.manage` | 代用户签发 API 令牌 |
| `POST` | `/judge/claim` | 判机 | 心跳 + 领取一条（空队列返回 404 `queue empty`） |
| `GET` | `/judge/cases/{id}/{in\|out}` | 判机 | 取用例正文（含判题用例） |
| `POST` | `/judge/submissions/{id}/status` | 判机 | 进度（`compiling` → `running`） |
| `POST` | `/judge/submissions/{id}/result` | 判机 | 回报判定与逐用例结果 |
| `GET` | `/judges` | `judge.view` | 判机框清单 |
| `GET` `/POST` | `/admin/site` | `site.settings` | 站名、首页语、友情链接、页脚、注册开关 |
| `GET` | `/admin/perms` | `permission.manage` | 账号列表与权限控制台 |
| `GET` | `/admin/perms/u/{handle}` | `permission.manage` | 单个账号的权限矩阵 |
| `POST` | `/admin/perms/u/{handle}` | `permission.manage` | 授予 / 收回 / 恢复默认（不能改自己） |
| `POST` | `/admin/users/{handle}/role` | `user.manage` | 改角色 |
| `POST` | `/admin/users/{handle}/status` | `user.manage` | 封禁 / 解封（封禁踢掉会话） |

建题示例：

```bash
curl -s -X POST -H "Authorization: Bearer ADM" -H "Content-Type: application/json" \
  -d '{"pid":"P1001","title":"A+B","statement":"输入 $a,b$，输出 $a+b$。",
       "time_limit_ms":1000,"mem_limit_kb":262144,"languages":["c","cpp"]}' \
  localhost:8080/problems
```

---

## 题面写作（Markdown + KaTeX）

题面按 Markdown 渲染，支持标题、表格、围栏代码块、列表、引用、链接。

数学公式：

- 行内用 `$a + b$`
- 行间用 `$$ \frac{a+b}{2} $$`，**并且要让这一段只放公式**：`$$…$$` 夹在句子里会被读成两个相邻的行内 `$…$`，结果是公式渲染了、两侧各留下一个裸 `$`。行间公式请单独占一行（前后空行最稳）。

渲染器把两者输出成 `<span class="math inline">\(…\)</span>` / `<span class="math display">\[…\]</span>`，页面里的 `math.js` 只对这两个节点调用 KaTeX，所以代码块里的下划线、`$` 不会被误当成公式。KaTeX 走 CDN（jsdelivr，0.16.11）；**CDN 不可达时公式退回显示原始 TeX**，题面照样能读，需要完全离线就把 KaTeX 放本地并改 `internal/api/templates/layout.html` 的两行链接。

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
| 允许自助注册 | 与 `-no-register` 取与；关掉后 `/register` 的页面与 POST 都是 403 |

保存是一次事务，页面缓存 2 秒后自动过期，改完刷新即见。被拒的保存会把已填内容原样留在表单里（长度按字符数算，与浏览器 `maxlength` 一致），不会让操作员整页重打。

动态（`/feed`）是纯文本版的朋友圈：

- 登录即可发，正文最长 4000 字符，一小时最多 20 条（按用户计数，复用登录节流的那张表）
- 只能删自己的；`feed.moderate` 可以删别人的，也可以只隐藏（`visible` 翻转，不删数据）
- 隐藏的条目对公众消失，作者在自己的主页仍能看到标着「已隐藏」的那条，审核者的时间线保留它否则无法撤销
- 时间线按 `id` 倒序游标翻页，`?after=` 取上一页最后一条；只有当页装满才会给出「更早的动态」

---

## 用例包约定

zip 里按 `<名字>.in` + `<名字>.out` 成对给出，目录层级会被压平（只取文件名）。

- 名字以 `sample` 开头 → 判为**样例**，展示在题面上且不计分
- 其余为**判题用例**，默认每题 1 分（可用 `PUT` 改分值）
- 重复上传按名字替换，**序号保持稳定**，判机顺序不会漂移
- 只有 `.in` 没有 `.out` → 整个包拒收并列出缺哪些
- 单文件上限 `-max-case`（默认 16 MiB），整包上限 `-max-upload`（默认 64 MiB）

```bash
zip -j cases.zip 1.in 1.out sample1.in sample1.out
curl -s -X POST -H "Authorization: Bearer ADM" -H "Content-Type: application/zip" \
  --data-binary @cases.zip localhost:8080/problems/P1001/cases
```

`Content-Type: application/zip` 不能省：`--data-binary` 默认发 `text/plain`，端点按 zip 读体就会一条用例都收不到，而提交只会回报「本题还没有测试数据」。

---

## 身份与权限

| 凭证 | 怎么来 | 能干什么 |
|------|--------|----------|
| `-admin-token` | 启动参数 | 建题、看/改隐藏题、建用户发令牌、看判题用例、驱动判机队列 |
| `-judge-token` | 启动参数 | 领取任务、取用例正文、回报结果 |
| 邮箱 + 密码 | `/register` 自助注册 | 登录成普通用户（session cookie） |
| 用户 API 令牌 | 登录后在 `/settings` 自助签发，或 admin 代签 | 给 curl / 脚本用，与网页登录互不影响 |

登录与会话：

- 注册要邮箱 + 用户名 + 密码（≥10 位，bcrypt cost 默认 11）；**邮箱暂不发送验证邮件**，注册即可用
- 登录可用邮箱或用户名；失败提示统一为「账号或密码不正确」，不区分哪一步错，避免枚举账号
- 同一来源 IP 在 15 分钟内失败 10 次返回 429；计数在库里，重启不会把锁定清零
- session cookie `fastoj_session`：`HttpOnly` + `SameSite=Lax` + `Path=/`，30 天；上 TLS 时加 `-secure-cookies`
- 每次登录新建 session；改密码吊销该用户全部 session（令牌不受牵连，吊销是 `/settings` 上的独立动作）
- **CSRF**：非 GET 请求统一过闸门，只有「凭 cookie 认证」的请求被要求带 `csrf` 字段或 `X-CSRF-Token` 头，与 session 里的 token 常量时间比较；`Authorization: Bearer` 的 API 调用不带 cookie、天然免疫，不做要求
- `/login?next=` 只认站内路径，`//evil`、`/\evil`、绝对 URL 一律回落到 `/`
- 引导令牌常量时间比较；用户令牌只存 SHA-256 摘要，明文仅在签发那一次出现
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

Windows 本地判机（`runner_windows.go`）：

- 内存上限用 Job Object（`JOB_OBJECT_LIMIT_PROCESS_MEMORY`）落实，实测 32 MB 上限下 `malloc(512MB)` 直接返回 `NULL`；无上限时如实报出峰值 526 MB
- CPU 时间取进程退出状态里的 FILETIME，内存取 Job 的 `PeakProcessMemoryUsed`
- 超时用 wall-clock 兜底（`max(2×时限, 2s)`）并整树击杀 —— 只限 CPU 的话，一个 `sleep` 型程序能把判机槽位永久占死
- **Windows 上内存超限通常表现为 `RE`（分配被拒）而不是 `MLE`**，Linux sandbox 才给得出明确的 MLE
- **不隔离文件系统与网络**：只适合跑可信题、本机自测

生产部署请把判机放 Linux 并接外部 sandbox（HOJ/exejail 那一类）。`judge.Guard` 接口已经留好，`runner_other.go` 是当前占位实现（只有超时击杀）。

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

---

## 已知限制与后续

| 事项 | 现状 |
|------|------|
| 语言 | 只有 C/C++；Python 判机的时间/内存测量另说 |
| 判机隔离 | Windows 只限内存/CPU；Linux 外部 sandbox 待接 |
| 账号 | 注册可关；邮箱验证与「忘记密码」重置链接还没做，忘了密码要管理员改库 |
| 建题 | 只能走 API（网页管理界面未做） |
| 竞赛/比赛 | 未做 |
| 统计 | 排行榜只有「按去重 AC 题数」，无难度、标签、通过率排序 |
| 防作弊 | 无代码查重；登录/注册/发动态有节流，提交与建题还没有速率限制 |
| 附件与图片 | 题面不能贴图（外链可以），无附件上传 |
| 静态资源 | 无指纹/长缓存策略，改样式需重启（模板内嵌在二进制里） |
