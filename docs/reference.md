# 参考手册

部署步骤见 [README](../README.md)。

## 角色

| 角色 | 登录系统 | 可见/可操作范围 |
|---|---|---|
| `super` 超级管理员 | admin | 全部，日志明文显示终端用户（用户名、令牌、IP）。唯一能管理管理员、渠道模板和系统设置；上 key 同样按模板锁定 |
| `admin` 普通管理员 | admin | 自己创建的门户用户（子用户）；自己上的 key + 子用户的渠道，及对应的统计；日志是全部（打码同门户）；审计日志 = 自己 + 子用户。上 key 时字段按模板锁定，可把渠道归属给子用户；可设置子用户的日志可见范围和结账记录 |
| 门户用户 | portal | 自己的渠道；按日志可见范围的日志（终端用户打码）；自己渠道的统计（含已删除渠道的历史）；自己的结账记录（只读）；自己的审计日志 |

越权一律 404（不暴露存在性）；普通管理员访问 super 专属接口返回 403。bootstrap 创建的首个管理员是 `super`，之后新建的管理员都是 `admin`。不能禁用或删除自己，至少保留一个启用的 `super`；名下有用户或渠道的管理员不能删除。

**渠道模板**是按渠道类型自动套用的参数：所有角色（含 super）都只能选有启用模板的类型。一种类型可以有多个启用模板（模板名全局唯一）：类型下只有一个时不用选，有多个时创建渠道必须传 `template_id`，否则 400；传了就必须是该类型的启用模板，否则 400。分组、优先级、权重、API 地址与额外参数（`extra`）以模板为准，服务端忽略客户端传来的这些字段；状态固定为启用。模型从模板的模型里选（`models` 逗号分隔，至少一个，缺省为全部，模板外的返回 400，顺序按模板）；模板的测试模型不在所选里时改用所选的第一个。模板还要勾选号池（`pool_ids`，至少一个，须是已有号池且不重复，否则 400）：用它提交的渠道推送到其中每个「接收新渠道」的号池，都不接收时创建返回 400。见「[号池](#号池)」。

**渠道创建后只能编辑名称和模型，任何人都不能改密钥和地址，任何接口都不返回密钥。** 此外只能启停、测试、刷新余额、删除，以及管理多 key 渠道的单个 key（见下文「多 key 渠道」）：

- **编辑**（`PUT /api/channels/{id}`，`{name, models}`，成功 204）：范围内且未删除的渠道。发给 new-api 的 body 只有 `id`、`name`、`models`，新模型不含渠道当前的测试模型时再加 `test_model`（所选的第一个，否则 new-api 的定时检查会测已去掉的模型，可能自动禁用渠道），请求里的其他字段不转发。模型须属于该渠道自己的模板（`portal_channels.template_id`）；该模板已停用或没有记录时，类型下只有一个启用模板就用它，否则改模型返回 400，只能改名。不传 `models` 或集合不变时不校验模板（模板收窄或停用后仍可改名）。new-api 拒绝时返回 502。审计 `channel.update` 记录改前改后。
- **删除是软删除**：渠道仍在 new-api 时先置为手动禁用（status=2），再在自有库打删除标记；不调用 new-api 的删除接口。用户侧不再显示，但日志与统计保留便于对账；管理员可在「已删除」视图恢复（恢复后保持禁用，需手动启用）。
- **清理映射**（`DELETE /api/channels/{id}?purge=1`，仅管理员）：只允许已在 new-api 面板删除的孤儿渠道，只删除本地记录。
- 门户用户名下有任何渠道（含已删除）时不能删除该用户，可改为禁用。
- 一个渠道推送到几个号池，就在每个号池各有一个 new-api 渠道（投放）。编辑、启停、删除对所有投放生效：全部号池都接受时照原样返回；只有部分接受时 202 `{pending_pools:[号池 id]}`，其余由后台推送任务稍后补齐；没有一个号池接受、也没有可以补齐的号池时，按原错误返回，本地不改。多 key 渠道的单个 key 启停同样发到每个投放，`done` 是所有号池都完成的编号，没完成的号池由推送任务稍后对齐：以最早创建、所在号池可用且自己没落后的投放为准；作为基准的投放 new-api 拒绝回报 key 状态（如已在 new-api 面板删除）时改以下一个投放为准，轮到落后的投放自己时保持现状；基准号池的 PAT 被拒（401/403）或 new-api 出错时不换基准，稍后重试。

## 目录

```
cmd/portal, cmd/admin     进程入口（embed 各自前端 dist）
internal/config           环境变量加载，缺失即报错
internal/httpx            JSON、错误、安全头、SPA、优雅退出
internal/auth             签名 cookie 会话、API 令牌校验、bcrypt、登录限速
internal/audit            操作审计记录、登录/登出端点
internal/secret           主密钥（APP_MASTER_KEY）派生子密钥，AES-256-GCM 加密存库的值
internal/security         两步验证：谷歌验证码、通用密钥、备份码、登录第二步、再验证、登录限流和图形验证码
internal/connection       号池注册表：从自有库读出各号池并打开（单个号池失败只降级它），保存前校验、错误归类、配置变化后退出重启
internal/logstore         MongoDB 里同步来的日志：同步任务、冲突检测、按 LogSource 读取
internal/store            自有 PG：用户、管理员、号池、模板、渠道与投放、同步状态与冲突、审计、系统设置、结账（schema.sql 启动时自动迁移）
internal/newapi           new-api API 客户端 + 只读 SQL（PG/MySQL）
internal/keyprobe         唯一读取 key 的包：经号池的 key 库连接读多 key 渠道的 key，直连 OpenRouter 测试和查余额
internal/channels         两个进程共用的渠道接口（创建、列表、编辑、测试、启停、余额、软删除、范围控制、结账汇总、跨号池合并日志）、推送任务和门户的程序接口
internal/brand            两个进程共用的公开品牌接口（系统名称、logo）
internal/portal, admin    两个进程各自的 HTTP API
web/ui                    共享前端代码库（基于 new-api 前端，AGPL-3.0），APP=portal|admin 两次构建
web/portal, web/admin     各自的 embed.go 与构建产物 dist/
```

## 统计口径

统计口径：消费 type=2，错误 type=5（按失败尝试计），退款 type=6；净额 = 消费 − 退款；时间范围 `[start,end)`，最长 92 天；金额按 new-api `/api/status` 的 `quota_per_unit` 换算。

- 多个号池的结果先按各号池的 `quota_per_unit` 换算到主号池（id 最小的号池）的单位，再相加；页面和接口里的 `quota` 都是主号池单位。某个号池的单位未知时跳过它并在 `degraded` 里说明。
- 日志模式为 sync 的号池只有最近 30 天的日志。查询起点更早时只返回这 30 天，响应带 `retention`。`/api/logs/stat` 的 RPM、TPM、并发只算直读号池（同步有分钟级延迟），额度包含全部号池。

- new-api 渠道测试写入的日志（`token_name = 'Model test'`）只在数据看板（`/api/stats*`）排除；日志列表、`/api/logs/stat` 和导出都包含，和 new-api 通用日志一致。
- `/api/logs/stat` 同 new-api 的 `SumUsedQuota`：用量是筛选范围内 type=2 的额度，RPM/TPM 只算最近 60 秒。`concurrency`（并发）是本系统加的：同样这些行里，同一秒同时处理的请求数峰值。new-api 在请求结束时才写日志，也不记录在途请求数，所以这是最近 60 秒的结果，看不到还在处理中的请求。
- 看板的缓存读写取 `logs.other` 的 `cache_tokens`、`cache_write_tokens`，`other` 为空或不是 JSON 时按 0。
- 性能健康从 `logs` 算，没有用 new-api 的内存采样：成功率 = 消费 /（消费 + 错误），延迟精度是 `use_time` 的秒，吞吐量只算耗时大于 0 的消费行。

## 配置

| 变量 | 进程 | 说明 |
|---|---|---|
| `APP_DB_DSN` | 两者 | 必需，自有 PostgreSQL，`postgres://` 或 `postgresql://` |
| `APP_MASTER_KEY` | 两者 | 必需，≥ 32 字节，两个进程相同（`openssl rand -hex 32`）。HKDF 派生三把子密钥，分别加密谷歌验证码密钥、号池连接和供应商提交的渠道 key；密文绑定字段名（验证码还绑定账号，号池连接绑定号池 id，渠道 key 绑定渠道编号），不能互换。不支持轮换 |
| `PORTAL_PUBLIC_ORIGIN` | portal | 必需，门户的访问地址，如 `https://portal.example.com`：小写，只有协议、主机和可选端口；须为 https，本地开发可用 `http://localhost:5181`；不能是 IP。通用密钥的 RP ID 取其主机名，不从请求头推导 |
| `ADMIN_PUBLIC_ORIGIN` | admin | 必需，后台的访问地址，规则同上 |
| `NEWAPI_BASE_URL`、`NEWAPI_PAT`、`NEWAPI_DB_DSN`、`NEWAPI_LOG_DB_DSN`、`NEWAPI_KEY_DB_DSN` | 两者 | 只用于一次性导入：自有库从未有过号池时读取，校验通过后加密写入为「号池 1」（日志模式 direct）并记审计 `settings.newapi_import`（只记字段名），校验失败启动报错。有过号池后忽略（删光号池也不会再导入），只打一行提示。之后的修改在后台「号池」，见下文 |
| `MONGO_URI` | 两者 | 可选，`mongodb://` 或 `mongodb+srv://`，同步来的日志存在这里。没有它时，开了日志同步的号池让 admin 启动失败，日志模式为 sync 的号池让两个进程都启动失败；后台也不允许打开同步或选 sync。连不上不影响启动，只有同步号池的日志暂时缺失，每 30 秒重连。按 `deploy/compose.yaml` 部署时已指向 `mongo` 服务 |
| `MONGO_DB` | 两者 | 默认 `newapisub`，库名，所有号池共用集合 `logs` |
| `COOKIE_SECURE` | 两者 | 必需，`true`（HTTPS）或 `false`（本地 HTTP） |
| `TRUSTED_PROXIES` | 两者 | 可选，逗号分隔的 IP 或 CIDR，默认空（不信任任何代理，审计 IP 取 `RemoteAddr`）；格式非法启动报错。部署在反向代理后必须设置，否则审计 IP 全是代理地址。按 `deploy/compose.yaml` 部署时已设为 `entry` 所在网段 |
| `PORTAL_LISTEN_ADDR` | portal | 默认 `:8081` |
| `PORTAL_SESSION_SECRET` | portal | 必需，≥ 32 字节随机串 |
| `ADMIN_LISTEN_ADDR` | admin | 默认 `:8082` |
| `ADMIN_SESSION_SECRET` | admin | 必需，≥ 32 字节，**必须与 portal 的不同** |
| `ADMIN_BOOTSTRAP_USERNAME` / `ADMIN_BOOTSTRAP_PASSWORD` | admin | 可选，两个同时设置或都不设。`admin_users` 为空时用它们创建首个管理员（角色 `super`）；都不设则随机生成，只在首次启动成功时打印到日志一次，之后忽略 |

生成密钥：`PORTAL_SESSION_SECRET`、`ADMIN_SESSION_SECRET` 用 `openssl rand -base64 48`，`APP_MASTER_KEY` 用 `openssl rand -hex 32`。`APP_DB_PASSWORD`（`deploy/.env`）会拼进 DSN，必须用 `openssl rand -hex 32`：base64 里的 `/` 会让 DSN 解析失败，容器起不来。

## 运行

依赖：Go 1.25+（CI 和发布镜像用 1.26.10：1.25 最新版本仍有未修复的标准库漏洞）、bun（上游声明 1.4.4，本机 1.3.5 可用；依赖与 `web/ui/bun.lock` 原样来自上游，安装一律 `--frozen-lockfile`）。前端 typecheck 通过 `bunx -p typescript@5.9.3` 临时拉取 tsc（上游依赖中无 typescript）。

```sh
make web          # bun 安装并构建两个前端：web/ui -> web/portal/dist、web/admin/dist（Go 编译 embed 依赖它们）
make build        # 产出 bin/portal、bin/admin

# 本地开发：后端 + rsbuild dev（各开一个终端；dev 只代理 /api 到对应后端）
make dev-admin    # :8082
make web-dev-admin   # http://localhost:5182
make dev-portal   # :8081
make web-dev-portal  # http://localhost:5181

make check        # gofmt + go vet + 前端 typecheck（不含测试）
go test ./internal/...   # 单元测试，仅开发机手工执行

# 需要真实数据库的测试，未设置变量时跳过，同样仅开发机手工执行：
TEST_PG_DSN='postgres://...' go test -timeout 30m ./internal/channels   # 经两个进程的 HTTP 路由跑范围、锁定字段、软删除等；每个测试建临时 schema，全包约 12 分钟，超过 go test 默认的 10 分钟
TEST_NEWAPI_DSNS='postgres://...,user:pw@tcp(host:3306)/db' go test ./internal/newapi   # 在 new-api 自己迁移出的 PG/MySQL 库上跑渠道与日志查询，会写入并删除测试行，只能指向临时库
TEST_PG_DSN='postgres://...' TEST_MONGO_URI='mongodb://...' go test ./internal/logstore   # 同步、冲突和 Mongo 读取（与 SQL 读取逐项比对）；每个测试用随机库名，用完删除
```

自有库 schema 在每次启动时由 `schema.sql` 自动迁移（`CREATE ... IF NOT EXISTS`、`ADD COLUMN IF NOT EXISTS`、`ON CONFLICT DO NOTHING` 的种子），新加的表、列和设置项会补到旧库上。

## 注意

- 后台用户页的「快速添加」按前缀和数量批量建用户（`abc001` 起，已占用的编号跳过），密码由浏览器随机生成，只在结果页显示一次，关闭后只能重置。
- **新渠道的同步延迟**：new-api 的启停和余额接口读内存缓存，新建渠道要等它下一次同步（`SYNC_FREQUENCY`，默认 60 秒）后才能操作。在此之前启停、删除、刷新余额返回 409 并提示稍后再试，不会把没生效的操作报告为成功。测试连接不受影响。
- 渠道名称原样写入 new-api；归属写在 `remark`：门户用户的渠道为 `portal:u{用户id}:{32 位随机 hex}`，管理员自己上的 key 为 `admin:a{管理员id}:{32 位随机 hex}`，每个渠道在每个号池的投放各有一个。推送任务每次创建前先按 `remark` 反查（有只读库的号池查库，没有的经 `GET /api/channel/` 查最新的渠道），找到就直接登记，所以响应不带 id（如 v1.0.0-rc.47 只返回 `{"success":true,"message":""}`）、超时或进程中途退出都不会建出第二个。
- 推送跨系统非原子：渠道在 new-api 建好、本地却已被删除时，推送任务会删除刚建的渠道；删除也失败时服务端日志打印 `ORPHAN new-api channel ... remark=...`，同一 `remark` 反查到多个渠道时打印 `ORPHAN new-api channels [...] ... share remark=...`，需按 remark 人工清理。
- 在 new-api 面板直接删除门户渠道后，渠道列表 `exists=false`，启停、测试与刷新余额返回 404；管理员用「清理映射」只删除本地记录，不调用 new-api 删除（避免误删复用该 id 的渠道）。
- 刷新余额由 new-api 查询上游并写回 `channels.balance`，不支持的类型由 new-api 返回失败，原因原样透传。多 key 渠道 new-api 不支持，本系统直接返回 400，提示改用「管理 Key」按 key 查询。
- 添加模式同 new-api：`single` 一个渠道、`batch` 每行一个渠道、`multi_to_single` 多 key 聚合为一个渠道。每行去首尾空白并丢弃空行。`single` 下多行非 JSON 的密钥、Codex（类型 57）的非 `single` 模式直接 400：new-api 会接受，但建出的渠道必然不可用。
- 不支持 new-api 的 `batch_add_set_key_prefix_2_name`（请求里传了也不转发）：它把密钥前缀写进渠道名，而任何人都不应看到密钥。同一批的渠道同名。
- `extra` 是原样转发给 new-api 渠道的白名单参数，用于依赖额外参数的类型（如 Azure 的 API 版本、Vertex 的部署区域）：`other`、`tag`、`openai_organization` 为字符串，`auto_ban` 为 0 或 1，`setting`、`settings`、`model_mapping`、`status_code_mapping`、`header_override`、`param_override` 为内容是 JSON 对象的字符串；字符串为空视为未设置。未知键（包括 `key`、`base_url`、`remark` 等受控字段）、类型不对、JSON 不合法一律 400。类型 3、18、21、39、41、49 的 `other` 为空时，启用模板返回 400（同 new-api 前端校验；禁用的模板可以先保存）。创建渠道一律用模板的 `extra`，请求里传了也忽略。
- `GET /api/channel-types` 每个启用模板一项，同一类型可以出现多次，`template_id`、`template_name` 用于在创建时选择；渠道列表项的 `template_id`（模板已删除或没有记录时为 `null`）是渠道创建时用的模板。门户审计不记 `template_id`。只向 super 返回完整的模板 `extra`。门户用户和普通管理员只拿到 `auto_ban`、`tag`、`model_mapping`、`status_code_mapping`、`other_set`（`other` 是否已设置）、`vertex_key_type`、`aws_key_type`（取自 `settings`，只有已知取值才返回）；`other`、`header_override`、`param_override`、`setting`、`settings` 的其他内容、`openai_organization` 的值不下发。创建渠道时服务端用模板原值，不依赖客户端。**模板的名称、`base_url`、`models`、`group`、`test_model` 仍会显示给他们，不要在里面放敏感信息。** 审计只记 `extra` 的键名，不记值。
- `GET /api/pool-names`（门户和后台，所有角色）返回全部号池的 `[{id, name}]`，供日志和看板的号池筛选用；不含地址、状态和原因，不记审计。
- super 的模板抽屉经 admin 的 `/api/newapi/*`（`models`、`default-base-urls`、`groups`、`prefill-groups`、`fetch-models`）使用 new-api 的同名能力，普通管理员 403，门户没有。成功原样返回 new-api 的 `data`，失败返回 502 与原因（PAT 缺 scope 时写明缺哪个），不会返回空结果。`fetch-models` 记审计 `channel.fetch_models`，不含密钥；new-api 的错误信息若引用了这个密钥，返回与审计前替换为 `***`。
- 门户用户与普通管理员自定义 `base_url` 须为 `https://`，不得含用户信息、查询参数或片段，主机为 IP 字面量时不得是内网、回环、链路本地或未指定地址（不做 DNS 解析）。模板的地址由超级管理员设置，只校验格式（允许 http 与内网，不得含用户信息）。
- 日志默认对所有角色开放：列表、用量统计和导出是各号池 new-api 的全部日志，包括未经本系统创建的渠道和其他人的渠道名称、模型、费用、错误内容；`owner=self`（页面的「仅自己」）才收窄到自己的渠道。数据看板、渠道列表和审计仍按范围。
- **日志可见范围**只约束门户用户：`app_settings.log_scope` 是全局值（`all`/`self`，种子 `all`，super 设置），`portal_users.log_scope` 是单个用户的覆盖（空为跟随全局，super 和所属管理员设置）。生效值为 `self` 时，门户的日志列表、用量统计、导出和分组下拉只含该用户名下渠道（含已软删除的），在接口层强制，查询别人的 `channel_id` 返回 404。设置读不到或取值未知时这些接口报错，不会放开。
- **结账**（可选，不用不影响其他功能）：按门户用户记录「结账到 X USD」，只增不删，更正就再加一条，最新一条算已结。已跑取名下渠道在各号池的 `used_quota`（含已软删除的；已在 new-api 删除的不再计入），各按所在号池的 `quota_per_unit` 换算后相加，待结 = 已跑 - 已结，可为负。没有数据库的号池用推送任务上次读到的 `used_quota`，结账详情带 `as_of`（最早的读取时间，Unix 秒），早于 15 分钟前时另带 `stale:true`。有结账记录的用户不能删除，可改为禁用。任何一个相关号池不可用时用户列表照常返回，`used_usd` 为 `null`（不是 0），`settled_usd` 来自自有库不受影响；结账详情接口此时返回 502，金额不给部分结果。**已知限制**：已跑取的是 new-api 当前的 `used_quota`，没有快照。有人在 new-api 物理删除渠道后，它的用量随之消失，已跑和待结会变小，甚至已结大于已跑。
- **品牌**：`GET /api/status` 和 `GET /api/brand/logo` 公开。logo 按文件内容判断类型，只接受 PNG、JPEG、WebP、ICO，不超过 256KB，不接受 SVG；响应带 `nosniff` 和 `Content-Security-Policy: default-src 'none'`，带 `v` 参数时长期缓存，否则每次校验。
- 门户与普通管理员的日志和 super 结构相同，终端用户信息在服务端处理：`user_id`、`token_id` 为 0，`username`、`ip` 为空，`token_name` 只保留 `Model test`，`other` 按 new-api 对普通用户的规则过滤（删除 `admin_info`、`root_info`、`audit_info` 等，解析失败返回 `{}`）；上游假定读者是日志本人，而这里日志跨用户可见，所以另外删除能把同一用户的日志关联起来的 `subscription_*`、`billing_source`、`billing_preference`、`wallet_quota_deducted`（详情里不再有「订阅计费」一节，费用显示 `quota`），并遮盖 `other` 文字里的 IP（流式错误 `stream_status` 可能带客户端地址）；`group`、`request_id`、`upstream_request_id` 照常返回。页面上用户、令牌、IP 显示 `••••`。`username`、`token_name` 筛选参数服务端忽略（输入框显示但禁用），不能用来试探客户身份。只有 super 看到明文和完整的 `other`（其中的密钥同样遮盖）。
- 除下文「多 key 渠道」的按 key 测试和查余额外，本系统不读取密钥，任何情况下都不返回密钥。测试结果、刷新余额的错误和原始响应、`other_info` 中的禁用原因与错误日志内容来自 new-api，其中可能引用上游返回的文字；new-api 只掩码 `api_key:` 形式与 URL 查询参数，上游若在错误里原样回显密钥则无法拦截。这些文字和日志的 `content`、`other`（所有角色、列表和导出）在返回和写入审计前都会额外遮盖常见格式（`sk-`、`gsk_`、`rk-`、`nap_`、`ptl_`、`xai-`、`pplx-`、`hf_`、`r8_`、`fw_`、`jina_`、`AIza`、JWT、`Bearer` 令牌，以及 `x-api-key: 值`、`api_key=值`、`?key=值`、`access_token`、`client_secret` 这类带名字的写法），其他格式拦不住。
- 审计日志记录两端所有写操作的成功与失败，以及登录用户对数据接口的 GET（动作 `view.*`，分类「访问」，detail 带查询参数；不记 `/api/me`）。普通管理员访问 super 专属接口的 403 也记录（`success=false`）；未登录的 401 不记；登录失败记录尝试的用户名和 IP。每条 detail 带 `http_method`、`http_route`、`http_status`，不含密码、密钥、PAT。`view.*` 每个 GET 同步写一行且没有保留策略，表增长快。
- 审计的 `ip` 是真实客户端地址：对端在 `TRUSTED_PROXIES` 内时，`X-Forwarded-For` 从右往左跳过可信地址，取第一个不可信地址；否则取 `RemoteAddr`，伪造的头不影响 `ip`。`forwarded_for` 保存原始头。
- 门户 `GET /api/audit-logs` 只返回当前用户自己的记录（服务端强制按本人过滤，忽略 actor 参数），排除 `view.*`，没有 IP 过滤，不含 forwarded_for 与 user_agent；他人用该用户名登录失败的记录也会出现（含对方 IP）。

## 多 key 渠道

`multi_to_single` 建出的渠道（`channel_info.is_multi_key`）可以逐个管理 key。key 用编号 `index`（从 0 开始，界面显示 `index+1`）指代，编号与 new-api 的 `GetKeys()` 一致（按 `\n` 拆分，只去掉首尾的 `\n`，中间空行占编号）；任何接口、审计、日志都不出现 key 或其片段。所有接口按渠道范围校验（越权 404），已软删除或已在 new-api 删除的渠道 404，非多 key 渠道 400。

| 接口 | 审计动作 | 说明 |
|---|---|---|
| `GET /api/channels/{id}/keys` | `view.channel_keys` | `{probe_enabled,probe_supported,total_balance,keys:[{index,status,disabled_time,reason,balance,balance_at,balance_error}]}`。`status` 1 启用、2 手动禁用、3 自动禁用；`disabled_time` 为 0 表示无；`reason` 来自 new-api 并遮盖密钥；`balance`、`balance_at`、`balance_error` 是本系统上次查询的结果（没查过为 `null`/`null`/空，失败时 `balance` 为 `null`）；`total_balance` 是已存余额之和，没有为 `null`。`probe_enabled` 是系统开关，`probe_supported` 表示该渠道支持按 key 测试和查余额 |
| `POST /api/channels/{id}/keys/enable`、`/disable` | `channel.key_enable`、`channel.key_disable` | body `{indexes}`，200 `{done:[int],error}`。按所给顺序逐个经 new-api `POST /api/channel/multi_key/manage` 的 `enable_key`、`disable_key`，遇错停止：已完成的在 `done`，`error` 是原因，空表示全部成功。一批最长 100 秒，受 new-api 全局限流影响 |
| `POST /api/channels/{id}/keys/test` | `channel.key_test` | body `{indexes,model}`，200 `{results:[{index,ok,message,latency_ms}]}`，按 `index` 排序。OpenRouter 拒绝或请求失败为 `ok:false` 和原因；整批超时还没开始的 key 不出现在 `results`。模型不属于该渠道（`channels.models`）400，`param_override` 不支持 400 |
| `POST /api/channels/{id}/keys/balance` | `channel.key_balance` | 可选 body `{indexes}`，不传为全部；查询后存库，返回同 `GET .../keys` |

`indexes` 为 1–100 个编号，重复的去掉；空、超过 100 或有负数 400，编号超出 404。

- 开关、状态只经 new-api 管理 API：客户端只会发 `get_key_status`、`enable_key`、`disable_key`，永不发送删除 key 或批量开关的 action；new-api 返回的 `key_preview`（key 前 10 位）在解码时丢弃。
- **按 key 测试和查余额**由本系统读出 key 直连 OpenRouter。new-api 无法指定用哪个 key，所以不经过它：
  - 测试用所选模型真实调用一次 `POST https://openrouter.ai/api/v1/chat/completions`（`max_tokens` 16，不流式），**按该 key 产生费用，不进 new-api 的使用日志，也不计入渠道用量**。HTTP 2xx 且 `choices` 非空才算通过，HTTP 200 但带 `error` 算失败。通过说明这把 key 在此配置下能调通这个模型，不保证经 new-api 的请求一定成功。
  - 请求遵守 new-api 渠道的实际配置，不用模板值：`model_mapping` 按链解析（不处理 new-api 按去掉推理后缀的模型名再查一次的回退），再应用 `param_override`。`param_override` 只支持不含 `operations` 的 JSON 对象（顶层合并，键名按字面）和 `{"operations":[...]}`，后者每项只能有 `mode`（只能是 `set`）、`path`、`value`，`path` 为点号分隔的字段名（展开成嵌套对象，不支持数组下标和通配符）。空、`null`、`{}` 视为无覆盖。其他任何写法（别的 `mode`、条件、`keep_origin`、`operations` 与其他字段并存、不是 JSON 对象）返回 400，**不发出任何请求**：否则请求会绕过渠道里限定上游的配置（如 `provider.only`）。`model_mapping` 无效或有循环同样 400。
  - 余额为 `GET https://openrouter.ai/api/v1/credits` 的 `total_credits - total_usage`（同 new-api）。
  - 只读 key 的代码集中在 `internal/keyprobe`，只执行 `SELECT key, model_mapping, param_override FROM channels WHERE id = ?`，走号池独立的 key 库连接（每个号池每进程常驻最多 2 个连接；admin 测试连接和保存时另开 1 个临时连接，用完即关，角色上限见 README）。
  - 只支持类型 20（OpenRouter）且 `base_url` 为空或主机恰好是 `openrouter.ai` 的渠道，否则 400；请求的地址固定为 `https://openrouter.ai`，不用渠道的 `base_url`，不跟随重定向，单次超时 15 秒。
  - 须由 super 在系统设置打开 `key_probe`（`app_settings.key_probe`，默认关闭），关闭时这两个接口 403，查看和开关不受影响；渠道所在号池没配 key 库时 503；key 超过 500 个的渠道 400。
  - 测试和查余额都是 8 个并发，整批最长 100 秒（单次调用测试 30 秒、查余额 15 秒），超时未开始的 key 不处理，余额保留上次结果。同一渠道的批量操作（开关、测试、查余额）同一进程内不能同时进行，后来的 409。测试结果不存库。
  - 批量开关逐个调 new-api，每个进程 3 分钟最多 170 个（new-api 按 IP 限 360 次/180 秒，portal、admin 共用），超出的不执行，响应的 `error` 说明原因；单个开关不计入。每个渠道每小时最多测试 1000 次（每进程），超出 429。
  - OpenRouter 的错误原文截断到 200 字，并替换本次的 key、遮盖 `sk-` 等形似密钥的串后才返回、存库和写审计；审计 detail 只有渠道信息、`indexes`、`model` 和数量（`checked`、`failed`），没有 OpenRouter 或 new-api 的原文。模型的回答不解码，只读 `choices` 是否为空和 `error`。
- 余额存自有库 `channel_key_balances`（按渠道和编号）。渠道列表里多 key 渠道有已存余额时，`balance` 为其合计、`balance_updated_time` 为最近一次查询时间、`balance_from_keys` 为 `true`；否则 `balance` 保持 new-api 的值、`balance_from_keys` 为 `false`。
- **已知限制**：余额按编号存储。有人在 new-api 删除了中间的 key 后编号会前移，已存余额会对到别的 key 上，直到下一次全部查余额；编号超出当前 key 数的记录在全部查余额时清除。

## 升级与回退

- **多号池、本地渠道编号和日志同步**（`newapi_pools`、`channel_placements`、`newapi_pool_sync`、`log_sync_conflicts` 四张新表，`portal_channels`、`channel_templates` 加列，序列 `portal_channel_ids`，新增 `mongo` 服务）：
  - 升级：先备份 appdb，在 `.env` 加 `MONGO_PASSWORD`，换上新的 `compose.yaml`，portal、admin 换同一版本后 `up -d`。启动时原 `newapi_connection` 的配置迁移为「号池 1」（只在从未有过号池时迁移一次，`newapi_connection` 表保留不动），老渠道全部归入号池 1、编号不变，渠道模板的号池设为号池 1。确认后台「系统设置 → 号池」有号池 1、渠道列表里老渠道编号不变且推送状态为完成，再添加远端号池、在模板里勾选，按「[日志同步](#日志同步)」打开同步；有只读库的号池先用对账核对，再考虑改成 sync。
  - **不支持回退**到升级前的镜像：新渠道的本地编号从 10000 起（或原最大编号加 1），旧版会把它们当成号池 1 的 new-api 渠道 id，号池 1 的 id 涨到这里时会串号。
  - 程序接口的变化见「[程序接口](#程序接口)」末尾的「升级后的变化」。
- **两步验证和 new-api 连接配置**（`auth_flows`、`auth_factors`、`newapi_connection` 三张新表）：
  - 升级：先备份 appdb，保留旧的 `deploy/.env` 和 `compose.yaml`。在 `.env` 加 `APP_MASTER_KEY`、`PORTAL_PUBLIC_ORIGIN`、`ADMIN_PUBLIC_ORIGIN`，`NEWAPI_*` 先保留；portal、admin 换同一版本后 `up -d`。启动时把 `NEWAPI_*` 导入库里，到后台操作日志看 `settings.newapi_import`，在「系统设置 → 号池」确认号池 1 的地址和主机。确认后从 `.env` 删掉 `NEWAPI_*`（`NEWAPI_NETWORK` 保留）再 `up -d`。
  - 回退：恢复旧的 `.env` 和 `compose.yaml`（旧版要求 `NEWAPI_*` 必填），新表留着无害。旧版不认识这些表，**回退后两步验证静默失效，只剩密码登录**，通用密钥也不能用；删除用户不受影响（新表外键是 `ON DELETE CASCADE`）。升级后在后台改过 PAT 或连接串的，回退前要写回 `.env`。再升级时以库里的配置为准，环境变量不再导入。
  - 主密钥不和 appdb 备份放在一起。丢失后谷歌验证码全部失效（备份码、通用密钥仍可用，其余用下面的命令重置），连接配置在后台重新填写全部信息。

- **key 读取角色**：升级到读 `model_mapping`、`param_override` 的版本前，先给 `newapi_key_ro` 补这两列的 `SELECT`（README 第 1 步第 5 项）。没补时按 key 测试和查余额都返回 500，日志里是权限错误，其他功能不受影响。
- **API 令牌**：新表 `api_tokens` 启动时自动创建，退回旧镜像时保留，不影响运行。portal 和 admin 须同版本部署：管理员重置密码时撤销令牌的语句在 admin 进程里，旧版 admin 重置密码不会撤销。
- **一个类型多个模板**：启动时 `DROP INDEX IF EXISTS channel_templates_enabled_type_idx` 去掉了“每类型一个启用模板”的唯一索引。**退回旧镜像前，先把每种类型多余的启用模板停用到只剩一个**，否则旧版启动时重建这个唯一索引会失败、进程起不来。

## 账号安全（两步验证）

两个系统相同，全部自愿；用户名密码始终保留。每个账号最多一个谷歌验证码（TOTP，6 位、30 秒）和一个通用密钥（Passkey）。第一个因子绑定时生成 10 个备份码（明文只在这一次的响应里），最后一个因子解绑时一并删除；页面只显示剩余数量，可以重新生成。

- **登录**：密码正确且已绑定因子时，`POST /api/auth/login` 返回 200 `{mfa_required:true,flow_token,methods,expires_in:300}`，不发 cookie，审计 `auth.login_pending`。`flow_token` 是随机串，不能当 cookie 用。第二步 `POST /api/auth/login/2fa` 带 `flow_token` 和 `code` 或 `backup_code`，输错不用重输密码，成功才发 cookie 并记 `auth.login`（`detail.method`），失败 401 记 `auth.2fa_failed`。通用密钥可以直接免密登录（`/api/auth/passkey/begin`、`finish`，要求设备验证指纹或 PIN），也可以代替第二步，此时等待中的密码登录自然过期。
- **防重放**：验证码按时间步记录，同一步和更早的码不能再用，所以绑定或验证后 30 秒内重复用同一个码会失败。备份码一次性。
- **再验证**：绑定、解绑、重新生成备份码、创建 API 令牌、保存或删除号池、清空并重新同步日志前，先 `POST /api/me/security/proof`（或 `proof/passkey/begin`、`finish`）换一个 60 秒有效、只能用一次、绑定账号和用途的 proof，放在请求头 `X-Security-Proof`。已绑定因子时只能用验证码、备份码或通用密钥，未绑定时只能用密码。失败 400（不用 401，以免前端当成退出），proof 不对 403。
- **限流**：第二步和再验证按账号各自计数，15 分钟失败 5 次锁 15 分钟，和密码登录的计数互不影响。
- **会话**：任何因子变更都让该账号的其他会话、等待中的登录和未用的 proof 失效，当前会话换新 cookie 继续。API 令牌不受影响，也不走两步验证。
- 谷歌验证码密钥加密存库（`APP_MASTER_KEY`），备份码只存 SHA-256，通用密钥存公钥和计数器。计数器倒退（疑似复制的设备）仍允许登录，但记 `auth.passkey_clone_warning`。

| 接口 | 鉴权 | 说明 |
|---|---|---|
| `GET /api/auth/captcha` | 公开 | `{captcha_id,image,expires_in:120}`，`image` 是 PNG 的 data URL，6 位数字 |
| `POST /api/auth/login` | 公开 | `{username,password,captcha_id?,captcha_code?}` → `{id,username}`+cookie，或上述等待第二步的响应 |
| `POST /api/auth/login/2fa` | `flow_token` | `{flow_token,code}` 或 `{flow_token,backup_code}` → `{id,username}`+cookie；401；429 |
| `POST /api/auth/passkey/begin`、`finish` | 公开 | begin 返回 `{flow_token,options}`；finish `{flow_token,credential}` → `{id,username}`+cookie |
| `GET /api/me/security` | 登录 | `{totp,passkey,backup_codes_left,methods}` |
| `POST /api/me/security/proof` | 登录 | `{purpose,password|code|backup_code}` → `{proof,expires_in}`。`purpose`：`totp_bind`、`totp_unbind`、`passkey_bind`、`passkey_unbind`、`backup_regenerate`、`token_create`、`newapi_config` |
| `POST /api/me/security/proof/passkey/begin`、`finish` | 登录 | begin `{purpose}`；finish `{flow_token,credential}` → `{proof,expires_in}` |
| `POST /api/me/security/totp/setup` | proof `totp_bind` | `{flow_token,otpauth_url,secret}`，10 分钟内确认 |
| `POST /api/me/security/totp/confirm` | 登录 | `{flow_token,code}`，首个验证码对了才生效 → `{backup_codes?}`+cookie |
| `DELETE /api/me/security/totp` | proof `totp_unbind` | 204+cookie |
| `POST /api/me/security/passkey/begin` | proof `passkey_bind` | `{flow_token,options}` |
| `POST /api/me/security/passkey/finish` | 登录 | `{flow_token,credential}` → `{backup_codes?}`+cookie |
| `DELETE /api/me/security/passkey` | proof `passkey_unbind` | 204+cookie |
| `POST /api/me/security/backup-codes` | proof `backup_regenerate` | `{backup_codes}`+cookie，旧码作废 |
| `POST /api/users/{id}/2fa/reset` | 后台：所属管理员或 super | 删除门户用户全部因子并让其会话失效，不动 API 令牌；范围外 404 |
| `POST /api/admins/{id}/2fa/reset` | 后台：super | 同上，不能重置自己（400） |

用户列表、管理员列表每项增 `mfa_enabled`（绑定了验证码或通用密钥，备份码不算）；后台 `GET /api/me` 增 `mfa_enabled`、`restricted`。`POST /api/tokens` 需要 `token_create` 的 proof，没有时 403。

**唯一的 super 丢失全部因子**时在服务器上执行（只需要数据库，不需要主密钥，后台起不来时也能用）：

```sh
docker compose -f deploy/compose.yaml run --rm admin reset-2fa <用户名>
```

它删除该管理员的全部因子、让其会话失效，并记审计 `admin.2fa_reset`（`actor_name` 为 `cli`）。

**登录限制**（每进程内存计数，重启清零）：

| 限制 | 维度 | 数值 | 触发后 |
|---|---|---|---|
| 请求频率 | 客户端 IP | 登录相关接口（`/api/auth/login`、`login/2fa`、`passkey/begin`、`passkey/finish`、`captcha`）合计 30 次/分钟 | 429 + `Retry-After`，审计 `auth.rate_limited`（每个 IP 每分钟最多一条） |
| 图形验证码 | 用户名 15 分钟内失败 3 次，或 IP 15 分钟失败 10 次 | | 之后的密码登录必须带 `captcha_id`、`captcha_code`，没带或错误 428 `{error,captcha_required:true}`，审计 `auth.captcha_failed`，不检查密码、不计密码失败 |
| 密码重试 | 用户名 | 15 分钟失败 5 次 | 锁 15 分钟，429；第 5 次失败本身就返回 429，不再要验证码 |
| IP 失败 | 客户端 IP | 15 分钟密码失败 30 次 | 锁 15 分钟，429，审计 `auth.rate_limited`（每个 IP 每分钟最多一条） |

用户名按小写计数，不管账号是否存在。密码错且下次需要验证码时 401 带 `captcha_required:true`。登录成功清该用户名的计数，不清 IP 计数。客户端 IP 和审计一样，只在对端属于 `TRUSTED_PROXIES` 时才采信 `X-Forwarded-For`：**CDN 后如果 IP 判定错（审计里全是 CDN 或入口地址），这些限制会误伤所有用户**，上线时按 README 第 6 步核对。验证码一次性，2 分钟有效，对错都作废；库里只存 `captcha_id` 的 SHA-256 和以 `captcha_id` 为密钥的答案 HMAC。验证码和免密登录的未完成流程每个系统最多 10000 个，门户和后台分开计。免密登录和第二步不要图形验证码。

审计动作：`auth.login`（会话签发时才记成功）、`auth.login_pending`、`auth.2fa_failed`、`auth.proof`、`auth.totp_bind`、`auth.totp_unbind`、`auth.passkey_bind`、`auth.passkey_unbind`、`auth.backup_regenerate`、`auth.passkey_clone_warning`、`auth.captcha_failed`、`auth.rate_limited`、`user.2fa_reset`、`admin.2fa_reset`，detail 不含验证码、密钥、备份码、proof 和密码。

## 号池

后台「系统设置 → 号池」，仅 super，普通管理员 403。每个号池是一个 new-api，存在 `newapi_pools` 表，PAT 和三个连接串用 AES-256-GCM 加密（绑定号池 id 和字段名），不进 `app_settings`。号池 id 不复用，删掉的号池的 id 不会分给以后的号池。

| 接口 | 说明 |
|---|---|
| `GET /api/pools` | `[{id,name,base_url,log_mode,accept_new,pat_set,db:{set,host},log_db:{set,host},key_db:{set,host},available,reason,quota_per_unit,placements,templates,version,updated_by,updated_at,sync}]`。`host` 只有 `主机:端口/库名`，不含账号和密码，密文解不开时为空；`available`、`reason` 是 admin 进程看到的状态，还没载入的号池 `reason` 为“保存后两个应用重启时载入”；`placements`、`templates` 是引用它的投放数和模板数；`sync` 见「[日志同步](#日志同步)」，没设置过为 `null` |
| `POST /api/pools/test` | 请求体同 PUT，另加 `id`（已保存的号池用它的密文补上留空的字段，新号池为 0）。不保存，不要 proof。200 `{ok,steps:[{name,ok,message}]}`，依次是管理接口、状态接口、主库和日志库、key 库、key 读取、key 隔离、读取最新日志（direct）或日志导出权限（sync）、重复检测，遇错停止 |
| `POST /api/pools` | 需要 `newapi_config` 的 proof（第一步就用掉，校验最长 30 秒）。`{name,log_mode,accept_new,base_url,pat,db_dsn,log_dsn,key_dsn}`，校验全部通过才保存，201 返回同 GET 的一项 |
| `PUT /api/pools/{id}` | 同上，另有 `clear_log_dsn`、`clear_key_dsn`、`confirm_address_change`；密文字段为空表示保留原值，`clear_*` 清空可选的两项。别人已改过、或名称地址与其他号池重复时 409 |
| `DELETE /api/pools/{id}` | 需要 proof。204；还有投放或模板引用它时 409 |

审计动作 `pool.create`、`pool.update`、`pool.delete`、`pool.test`，`detail` 有 `name` 和改动的字段名 `fields`。

- 名称全局唯一，**门户用户也能看到**（推送状态、日志、统计按号池分开显示），取名时注意。地址规范化（去掉末尾 `/`，不区分大小写）后也不能和其他号池相同。重复检测再拿这个 new-api 最近 24 小时最新的一条日志，到其他号池按 `request_id` 和日志 id 查找，找到就拒绝保存，所以同一个 new-api 换一种地址写法也能发现；最近 24 小时没有日志时只提示未能比对。
- 地址变化时必须同时带新的 `pat` 和 `confirm_address_change:true`，否则 400，测试接口也一样，旧 PAT 不会发到新地址。换到另一个 new-api 后，这个号池已有投放的 new-api id 全部对不上；只改连接串指到另一个库时检测不到这种错位。
- `log_mode` 为 `direct`（日志直读主库或日志库）或 `sync`（读同步进 MongoDB 的日志，须打开同步）。direct 必须填主库；sync 可以不填，这时日志库和 key 库也必须为空，见「[没有数据库的号池](#没有数据库的号池)」。没配 `MONGO_URI` 时不能保存 sync。
- `accept_new` 关掉后，新提交的渠道不再推到这个号池，还在排队推给它的也暂停，打开后继续；已建好的投放照常落实改动。
- 配了 key 库时，校验用主库、日志库的连接执行 `SELECT "key" FROM channels WHERE 1=0`，能执行就拒绝保存：连接串不同但角色相同也会被发现。
- 测试和保存时主库、日志库、key 库各临时开 1 个连接，用完即关，加在两个进程的常驻连接之上；角色的连接上限要留出这几个（见 README 部署第 1 步第 3、5 项），不够时报“数据库连接数已满”。
- 错误信息只是固定文案（如“数据库用户名或密码错误”“数据库连接数已满”“连接超时”“new-api 拒绝了 PAT，请检查令牌和权限”），不含连接串、PAT、地址和 new-api 的原文，服务端日志也一样。
- 每次保存或删除号池（包括只改名称或 `accept_new`），两个进程每 10 秒检查一次，发现变化就优雅退出（退出码 0），由 `restart: unless-stopped` 拉起，不做热替换。读版本失败只打日志。日志同步的设置不在其内，改了不重启。
- 号池各自打开：一个解不开或连不上时只有它带 `reason`，每 30 秒重试，连上就原地换上。其余号池照常：日志、统计、看板的响应带 `degraded:[{pool_id,name,reason}]`（所有角色可见；门户用户看到的 `reason` 一律是“号池暂时不可用”，具体原因只给后台），导出返回 503（不给不完整的文件），结账返回 502，渠道列表里这个号池的投放按不可用显示。
- **受限模式**：库里一个号池都没有时，admin 只开放登录、账号、账户安全、品牌、系统设置和号池接口，其余 `/api/*` 返回 503；portal 启动失败，靠重启策略恢复。添加第一个号池后两个进程重启，退出受限模式。
- **主号池**是 id 最小的号池：页面的额度单位（`quota_per_unit`）取它的，其他号池的额度换算到这个单位；模板抽屉的 `/api/newapi/*` 不带 `?pool_id=` 时也用它。

### 渠道编号

- 页面、程序接口、日志、统计和审计里的渠道 `id`、`channel_id` 都是本系统的本地编号，不是哪个 new-api 的 id。升级前就有的渠道编号不变（等于它在号池 1 的 new-api id）；之后新建的取自序列 `portal_channel_ids`，从 10000 和原最大编号加 1 中较大的那个开始，所有号池共用，不会重复。
- 渠道在每个号池的 new-api id 只有 super 能在投放明细里看到（`placements[].newapi_channel_id`）。
- 日志、统计、流量图和导出里，不是本系统的渠道（号池里别人的渠道）`channel_id` 为 0，不会用它的 new-api id 冒充本地编号；super 另看到 `newapi_channel_id`。这种行的 `channel_name`（对方起的名字）只给后台，门户用户为空，页面显示“其他渠道”。`channel_id` 筛选参数只接受本地编号。

### 提交与推送

- 提交渠道（`POST /api/channels`、程序接口 `POST /api/channel/`）只在本地建档：校验后按模板的号池建投放，key 用主密钥加密存入 `portal_channels.key_sealed`，立刻返回 201 `{ids:[本地编号]}`（程序接口仍是 `success:true` 和 `data={ids}`）。key 密文一直保留，任何接口都不返回，也不进审计和日志，只有推送任务在内存里解开。
- 推送任务在 admin 进程里运行（多个 admin 时由 PostgreSQL advisory lock 保证只有一个在跑），每 5 秒处理一批投放，一次一个 new-api 写操作：先按 `remark` 反查，没有才创建。new-api 明确拒绝（`success:false`）直接标为失败；连不上、超时、5xx 退避重试（30 秒起翻倍，最长 1 小时），第 10 次仍失败就标为失败；被 new-api 限流、号池不可用时这个号池冷却 1 分钟，不计次数。每个号池 3 分钟最多 60 次写操作（new-api 按 IP 限 360 次/180 秒，页面和日志同步共用）。每次推送的结果记审计 `channel.push`（`actor_name` 为 `system`）。
- 还没有任何投放建好时，测试、刷新余额和 key 操作返回 409：推送中是“渠道正在推送到号池，请稍后再试”，全部失败是“渠道推送到号池失败，请联系管理员重试”。改名、改模型、启停、删除随时可以，本地先记下，推送任务落实到每个投放。建好以后 new-api 的同步延迟照旧（见「注意」的「新渠道的同步延迟」）。

### 投放状态与重试

渠道列表每项增 `push_state` 和 `placements`：

- `push_state`：`pending`（还没有投放建好）、`partial`（部分号池建好）、`failed`（一个都没建好，且有失败的）、`done`（全部建好）。
- `placements:[{state,pool_id,pool_name,error,newapi_channel_id,state_at,stale}]`，`state` 为 `pending`、`pushing`、`created`、`failed`。门户用户只有 `state`、`pool_id`、`pool_name`；管理员另有 `error`（固定文案）；super 另有 `newapi_channel_id`。没有数据库的号池的投放另有 `state_at`（缓存读取时间，Unix 秒，没读过时没有）和 `stale`（超过 15 分钟没读到时为 `true`），所有角色可见。
- 一个渠道合成一行：有一个投放启用就是启用，否则有自动禁用就是自动禁用，否则是手动禁用；例外是已经停用、还有号池等推送任务落实（仍是启用）时，`status` 为 2 并带 `status_pending:true`，页面显示“停用生效中”；`used_quota` 各投放换算到主号池单位后相加；余额取最近刷新的投放。还没有投放建好时用提交的内容合成，`exists:false`。

| 接口 | 权限 | 说明 |
|---|---|---|
| `POST /api/channels/{id}/placements/{pool_id}/retry` | 后台，渠道在范围内 | 只有 `failed` 的投放能重试，否则 409；改回排队、次数清零，202。审计 `channel.placement_retry` |
| `DELETE /api/channels/{id}/placements/{pool_id}` | 后台 super | 取消还没建出渠道的投放（`pending` 或 `failed`），已建好或正在推送 409；204。审计 `channel.placement_cancel` |

### 没有数据库的号池

日志模式为 sync 且不填主库的号池只经 new-api 管理接口使用，不占任何数据库连接：

- 推送任务每 5 分钟读一次本系统在这个号池的每个渠道，缓存状态、用量和余额：new-api 渠道总数不超过 3000 时先按列表读（`GET /api/channel/`，每页 100 个，读完整表才更新号池的分组），超过时只读列表第 1 页；列表里没有的渠道逐个按 id 读（`GET /api/channel/{id}`，PAT 只要渠道读取权限），最久没读的先读。每个号池 3 分钟最多 30 次读取，和推送、页面、日志同步共用 new-api 的限流；没读完的 1 分钟后接着读，上一轮有按 id 没读完的渠道时列表最多读 25 页，留 5 次给按 id 读取（这一轮列表读不完整，不更新分组）。只有 new-api 按 id 回答“没有这个渠道”才算已在 new-api 删除；读失败的渠道保留上次的缓存，服务端日志记一条。渠道列表、结账和分组下拉用这份缓存，正常晚几分钟；超过 15 分钟没读到的投放带 `stale:true`，结账带 `as_of` 和 `stale`。结账遇到从没读过的投放（刚建好，推送任务还没读）当场按 id 读一次，读不到才返回 502。
- 创建后按 `remark` 反查只看最新的几页渠道（最多 3 页，看到比第一次尝试早 5 分钟以上的渠道就停；退避、重试不会推迟这个起点）。
- 没有 key 库，按 key 测试和查余额不可用；测试、启停、刷新余额照常经管理接口。

### 日志同步

把号池的日志经 new-api `GET /api/log/export`（PAT 要有 `log:read`）按时间窗口拉进 MongoDB 的 `logs` 集合，所有号池共用。日志模式为 sync 的号池，页面读的就是这里；direct 号池也可以打开同步，只写不读，用来对账（影子模式）。同步任务和推送任务在同一把锁下，只在一个 admin 进程里运行。

- **去重**：唯一键 `(pool_id, log_id)`，重跑、窗口重叠都不会多出行。**保留期**：`ts` 上的 TTL 索引，30 天后由 MongoDB 自动删除（它的清理每分钟一次，到期后会稍有延迟）。
- **窗口**：每 5 秒看一次哪些号池到期。每轮最多 `max_windows` 个窗口，每个长 `window_s` 秒，起点往前多读 120 秒（晚提交的日志靠唯一键去重），只读到 30 秒前（new-api 写日志晚于 `created_at`，也不按 id 顺序）。一个窗口只发一次请求；读完且条数与 new-api 末行的 `count` 一致才前进游标，中途断开或条数不符都不前进，下一轮重读。游标早于 30 天前时从 30 天前开始。
- **控制**：`PUT /api/pools/{id}/sync`（super，不重启，审计 `pool.sync_update`），`{enabled,interval_s,window_s,max_windows,backfill_days}`：间隔 30–3600 秒，窗口 60–3600 秒，每轮窗口数 1–60，回补 0–30 天，超出 400。页面默认 60 秒、600 秒、6 个、1 天。第一次打开时游标从 `backfill_days` 天前开始，之后再开关都从原游标继续。没配 `MONGO_URI` 时不能打开。
- **节流和退避**：追平以后每轮只有一个窗口、一次请求；默认间隔下每 180 秒最多 18 次（追赶时），和页面、推送共用 new-api 每 IP 360 次/180 秒的限流。失败（new-api 限流、同一 new-api 用户已有导出在跑、网络错误、MongoDB 写入失败或连不上、号池在本进程不可用）后等 `interval_s × 2^连续失败次数`，最长 30 分钟，成功后清零；保存号池（如换了 PAT）或修改同步设置时也清零，下一个间隔就重试。服务端日志只记固定文案（如“new-api 拒绝了 PAT，请检查令牌和 log:read 权限”“new-api 暂时不可用”），不记 new-api 的原文。追不上就加大 `max_windows` 或减小 `interval_s`，new-api 压力大就反过来。
- **监控**：`GET /api/pools` 每项的 `sync`：`{pool_id,enabled,interval_s,window_s,max_windows,cursor,lag_s,max_log_id,max_log_at,paused,failures,backoff_until,last_run_at,last_ok_at,last_error,last_rows,last_ms,rows_total,newest_log_at,open_conflicts}`。时间是 Unix 秒，`backoff_until`、`last_run_at`、`last_ok_at`、`newest_log_at` 没有过为 `null`；`lag_s` 是游标落后现在的秒数，正常在 30 秒到一个间隔多一点之间，页面超过 10 分钟标黄；`last_error` 是固定文案；`paused` 是暂停它的冲突种类，空为运行中。
- **对账**：`GET /api/pools/{id}/sync/verify?hours=24`（super，`hours` 1–720），只限有主库的号池：取游标之前 `hours` 小时（不早于 30 天前），分别在 new-api 库和 MongoDB 数条数和额度，`{start,end,db:{count,quota},mongo:{count,quota},match}`。
- **清空并重新同步**：`POST /api/pools/{id}/sync/reset`（super + `newapi_config` 的 proof，`{backfill_days}`，审计 `pool.sync_reset`）：删掉这个号池在 MongoDB 的全部日志，游标退回 `backfill_days` 天前，未处理的冲突全部标为已处理，200 `{deleted}`。

### 冲突

同步不覆盖已有数据。发现下列情况记一条冲突（同号池、同种类、同日志 id 未处理时只累加次数）；`detail` 只有时间、`request_id`、指纹或另一个号池，不含日志内容：

| 种类 | 含义 | 结果 |
|---|---|---|
| `fingerprint` | 同一个日志 id，内容和已存的不同：new-api 换库、清表或重启后重用了 id | 暂停这个号池的同步，已存的不被覆盖 |
| `id_regression` | 出现了比以往都新的日志，最大 id 却比以前小：new-api 的 id 重新计数 | 暂停 |
| `zero_id` | 日志 id 为 0（new-api 的日志库是 ClickHouse） | 暂停，每次都会 |
| `duplicate_source` | 同一个 `request_id` 已在另一个号池出现：同一个 new-api 配成了两个号池 | 暂停 |
| `clock_skew` | new-api 响应头的时间和本机相差超过 60 秒 | 只记录。new-api 慢超过约 150 秒时，它的日志会写进已经读过的窗口而漏掉 |

| 接口 | 说明 |
|---|---|
| `GET /api/pools/{id}/conflicts` | super。最近 200 条 `[{id,pool_id,kind,log_id,detail,count,first_seen,last_seen,resolved_by,resolved_at}]`，未处理的 `resolved_at` 为 `null` |
| `POST /api/pools/{id}/conflicts/resolve` | super，`{id}`：确认并继续，200 `{resumed}`，审计 `pool.conflict_resolve`。确认过的 `fingerprint`、`duplicate_source` 重读同一行时不再记录、不再暂停；没有其他会暂停的未处理冲突时恢复同步（`resumed:true`），因 `id_regression` 暂停的从下一个窗口重新建立 id 基线 |

怎么处理：
- `fingerprint`、`id_regression`：new-api 确实换了库或重新计数，旧日志已经对不上时用「清空并重新同步」；只是个别行、已存的版本可以保留时「确认并继续」。
- `duplicate_source`：先查是不是同一个 new-api 配了两次。是就关掉多余号池的同步，它没有投放和模板引用时可以删除；不是就确认并继续。
- `zero_id`：本系统不支持 ClickHouse 日志库，确认后下一条 id 为 0 的日志会再次暂停，只能关掉这个号池的同步。
- `clock_skew`：校准 new-api 所在机器的时钟。

### MongoDB 运维

- `deploy/compose.yaml` 的 `mongo` 服务用官方镜像（固定 digest），以镜像自带的 mongodb 用户运行、不留任何 capability，只接内部 `data` 网络、不开端口，WiredTiger 缓存 0.5 GB，数据在卷 `mongodata`。root 账号 `app` 只在数据卷为空的首次启动时按 `MONGO_PASSWORD` 创建：之后改 `.env` 不会改库里的密码，要先在 mongo 里改（`db.changeUserPassword`），再改 `.env` 并重建两个应用。
- 两个应用启动时建索引（`(pool_id, log_id)` 唯一、TTL 和查询用的复合索引）。MongoDB 连不上不影响启动，每 30 秒重连，期间只有同步号池的日志缺失（带 `degraded`）。同步任务每轮先探测 MongoDB，连不上（启动时没连上或之后断开）时每个到期的同步号池记一次失败并退避，`last_error` 为“日志库暂时不可用”，游标不动；每个窗口前进游标前也再探测一次，没有日志可写的窗口同样不会在 MongoDB 连不上时前进。MongoDB 恢复后的下一轮清掉这些失败和退避，同步照常继续。连上之后又断开时，读取最多等 3 秒；一次读取发现连不上后，接下来 10 秒内的读取直接按降级返回，不再等待。进程退出时断开 MongoDB 最多等 2 秒。
- `mongodata` 只是缓存，可以不备份：丢了以后对每个同步号池「清空并重新同步」，从 new-api 读回最多 30 天（以 new-api 还留着的为限）。直读号池不受影响。
- 删除号池时它在 MongoDB 的日志不立即删除，30 天内由 TTL 清掉。

## 日志、统计与审计接口

门户和后台路径相同。日志三个接口默认是全部日志，`owner=self` 或门户用户的日志可见范围为 `self` 时收窄；看板接口范围按登录角色决定。范围内查询的渠道 id 超出范围返回 404，错误统一 `{"error":...}`。

跨号池：下表接口查询全部号池后合并（额度换算到主号池单位），所有角色都可以加 `pool_id` 只看一个号池（范围不变，非法值 400“无效的 pool_id”；没有这个号池，或门户用户名下渠道都不在这个号池时 400“号池不存在”）。响应顶层可能多两项：`degraded:[{pool_id,name,reason}]`，没能查询的号池，HTTP 仍为 200，门户用户的 `reason` 固定为“号池暂时不可用”；`retention:{since,pool_ids}`，查询起点早于 30 天前且有日志模式为 sync 的号池参与时，这些号池只有 `since` 之后的日志。`channel_id` 是本地编号，不是本系统的渠道为 0。多个号池时 `/api/logs` 的 `p × page_size` 超过 10000 返回 400“翻页太深，请缩小时间范围或加筛选条件”。

| 接口 | 参数 | 响应 |
|---|---|---|
| `GET /api/logs` | `start`、`end`（必填，秒，最长 92 天），`type`（非负整数，0 或缺省为 2、5、6），`channel_id`（可重复），`model_name`（含 `%` 时模糊匹配），`group`，`request_id`，`upstream_request_id`，`p`，`page_size`（1–200，默认 50）；`owner=self`；仅 super 生效：`username`、`token_name`；仅后台：`user_id` | `{items:[{id,created_at,type,channel_id,channel_name,model_name,quota,prompt_tokens,completion_tokens,use_time,is_stream,content,user_id,username,token_id,token_name,ip,group,request_id,upstream_request_id,other,pool_id,pool_name}],total}`；`channel_id` 为 0 的行 super 另有 `newapi_channel_id`（它在该号池的 id）。行的唯一键是 `pool_id` 加 `id` |
| `GET /api/logs/stat` | 同上（不分页） | `{quota,rpm,tpm,concurrency,synced_pool_ids}`；`synced_pool_ids` 是参与的 sync 号池，`rpm`、`tpm`、`concurrency` 不含它们 |
| `GET /api/logs/groups` | 无 | `{groups:[...]}`，各号池 new-api 渠道配置的分组合在一起；门户用户的日志可见范围为 `self` 时只含名下渠道的 |
| `GET /api/logs/export` | 同上（不分页），加 `tz_offset`（必填，秒），`lang=zh\|en`（默认 zh） | 带 BOM 的 CSV 流，列同 new-api 的导出；非 super 的用户、IP 为 `••••`，令牌为 `••••`（模型测试令牌除外）。和 new-api 一样没有行数上限；同一账号已有导出、或本进程已有 2 个导出在跑时返回 409，单次最长 30 分钟，开始输出后失败或超时直接断开连接，审计动作 `log.export`。有号池降级、或导出开始输出前有号池查询失败时返回 503，查询起点早于 30 天前且有 sync 号池参与时返回 400“同步号池只保留 30 天的日志，请缩小时间范围”，都不给不完整的文件；费用按各行所在号池的单位算；不是本系统的渠道只给 super 显示 `号池名#new-api id` |
| `GET /api/stats` | `start`、`end` 与范围参数 | `{rows:[{created_at,model_name,count,quota,token_used,cache_read_tokens,cache_creation_tokens}]}`，小时桶 |
| `GET /api/stats?by=channel` | 同 `/api/stats`，加 `split=pool` 时每个渠道按号池拆行 | `{rows:[{created_at,channel_id,channel_name,count,quota,token_used,cache_read_tokens,cache_creation_tokens}]}`，每个渠道一行（各号池的投放合成一行），是整个范围的合计，按消耗降序；`created_at` 为范围起点；`split=pool` 时每行另有 `pool_id,pool_name` |
| `GET /api/stats?by=pool` | 同 `/api/stats` | `{rows:[{created_at,pool_id,pool_name,count,quota,token_used,cache_read_tokens,cache_creation_tokens}]}`，每个号池一行，范围同上。门户用户看到的是自己渠道在各号池的数据 |
| `GET /api/stats/perf` | `hours`（默认 24，上限 720）与范围参数 | `{summary:{avg_latency_ms,success_rate,avg_tps}\|null,window_start,window_end,models:[{model_name,avg_latency_ms,success_rate,avg_tps}]}` |
| `GET /api/stats/flow` | 同 `/api/stats`，可加 `split=pool` | `{rows:[{channel_id,channel_name,model_name,token_used,count,quota}]}`；super 另有 `user_id,username,token_id,token_name,use_group`；`split=pool` 时每行另有 `pool_id,pool_name` |
| `GET /api/stats/users` | 同 `/api/stats`，仅 super，其他角色 403 | `{rows:[{created_at,username,count,quota,token_used,cache_read_tokens,cache_creation_tokens}]}` |
| `GET /api/audit-logs` | `action`，`category`（`auth`、`channel`、`user`、`admin`、`template`、`log`、`view`），`success`，`start`，`end`，`p`，`page_size`；仅后台：`actor_type`、`actor_name`、`actor_id`、`owner=self`、`ip`（完整地址精确匹配，否则按前缀） | `{items:[{id,actor_type,actor_id,actor_name,action,target_type,target_id,success,detail,ip,forwarded_for,user_agent,created_at}],total}`；门户不含 `forwarded_for`、`user_agent` |

## 系统设置、用户与结账接口

| 接口 | 权限 | 说明 |
|---|---|---|
| `GET /api/status` | 公开，两个进程 | `{system_name,logo}`，名称按进程取；`logo` 为 `/api/brand/logo?v=<毫秒时间戳>` 或 `/logo.png` |
| `GET /api/brand/logo` | 公开，两个进程 | 图片；未设置时 404 |
| `GET /api/settings`、`PUT /api/settings` | super | `{log_scope,portal_name,admin_name,portal_public_url,key_probe}`，`key_probe` 为布尔（按 key 测试和查余额，默认 `false`）；PUT 须传全部字段（缺 `key_probe` 即关闭），返回保存后的值 |
| `PUT /api/settings/logo`、`DELETE /api/settings/logo` | super | PUT 的请求体是图片本身；204。超过 256KB 返回 413，类型不符返回 400 |
| `GET /api/me` | 登录 | 门户增 `log_scope`（生效值）；后台增 `portal_url`（可为空） |
| `GET /api/users` | admin、super，按范围 | 每项增 `log_scope`（空/`all`/`self`）、`used_usd`（有相关号池不可用时为 `null`）、`settled_usd` |
| `POST /api/users/{id}/log-scope` | admin、super，按范围 | `{log_scope}`，空为跟随全局；204 |
| `GET /api/users/{id}/settlements` | admin、super，按范围 | `{used_usd,settled_usd,pending_usd,items:[{id,amount_usd,note,created_by,created_at}]}`，新的在前 |
| `POST /api/users/{id}/settlements` | admin、super，按范围 | `{amount_usd,note}`，`amount_usd` 必填且为 0 到 1e12，`note` 最多 200 字；201 和新记录 |
| `GET /api/settlements` | 门户用户 | 自己的，结构同上，但 `items` 不含 `created_by`（管理员登录名不给门户看） |

## 程序接口

门户用户用 API 令牌批量上 key、查询，路径、分页和响应对齐 new-api v1.0.0-rc.47，new-api 的脚本改地址和令牌就能用。只有门户进程有这组接口，权限、归属、审计和开关都和页面相同：适配层只转调页面的接口，只能操作自己名下的渠道，越权 404。

**令牌**在门户页面管理，这三个接口只接受浏览器登录，令牌不能签发令牌、改密码或登出：

| 接口 | 说明 |
|---|---|
| `GET /api/tokens` | `[{id,name,hint,expires_at,last_used_at,created_at}]`，新的在前；`hint` 形如 `ptl_abcd…wxyz`，时间为 Unix 秒，可为 `null` |
| `POST /api/tokens` | `{name, expires_at?}`，`name` 1–64 字符，`expires_at` 须在将来，不传不过期。201 和 `{id,name,hint,expires_at,created_at,token}`，**`token` 只在这里出现一次**。每人最多 10 个，超出 409 |
| `DELETE /api/tokens/{id}` | 204，别人的 404。撤销即删除，历史看审计 `auth.token_create`、`auth.token_delete` |

令牌形如 `ptl_` 加 43 位 base64url，库里只存 SHA-256。账号禁用或删除立即失效，账号重新启用后未撤销的令牌恢复有效；用户改密码、管理员重置密码时撤销该用户全部令牌；浏览器登出不影响令牌。用令牌发起的操作，审计 detail 多一个 `token_id`，用来区分是哪个令牌。

**调用**用 `Authorization: Bearer <令牌>`，不用 `New-Api-User` 头。和 new-api 一样，`Bearer` 不区分大小写，也接受不带前缀的裸令牌。响应是 `{success,message,data}`：

| HTTP 状态 | 含义 |
|---|---|
| 200 `success:true` | 成功 |
| 200 `success:false` | 业务失败，`message` 是原因，包括参数错误、new-api 拒绝或不可用、内部错误。上游失败不用 5xx，免得 HTTP 客户端自动重试创建。new-api 明确拒绝时 `message` 是它的原话（遮盖密钥）；连不上、超时、5xx 只说 `new-api 暂时不可用`，内部错误只说 `服务器内部错误`，完整错误只在服务端日志。页面接口和审计的 `detail.error` 同样如此 |
| 401 | 令牌缺失、无效、过期或账号禁用，响应 `{success:false,message}` |
| 403 | 功能未开放（按 key 测试和查余额的开关） |
| 404 | 渠道或 key 不存在、不在名下，或接口不存在。未列出的路径分两种：命中 `/api/channel/{id}` 的（如 `/api/channel/search`）先校验令牌，匿名 401，带令牌 404，响应都是 `{success:false,message}`；其余不校验令牌，一律 404，响应是 `{"error":...}` |
| 409 | 稍后再试：渠道还在推送到号池、推送失败（等管理员重试）、新渠道 new-api 尚未同步，或该渠道有批量操作在进行，`message` 说明是哪种 |
| 429 | 令牌限流：每个用户所有令牌合计 180 秒 120 次（每进程），`Retry-After` 是等待秒数，响应 `{success:false,message}` |

| 接口 | 参数 | 响应 |
|---|---|---|
| `GET /api/channel/` | `p`，`page_size`（默认 10，上限 100，别名 `ps`、`size`，非法值回默认），`status`（`enabled`/`1`、`disabled`/`0`，其他值不筛选），`type`，`group`。`sort_by`、`id_sort` 忽略，固定 id 倒序；`tag_mode=true` 失败 | `data={items,total,page,page_size}` |
| `GET /api/channel/{id}` | 无 | `data=` 单个渠道 |
| `GET /api/channel/types`（扩展） | 无 | `data=[{type,template_id,template_name,base_url,allow_custom_base_url,models,test_model}]`，开放的类型和模板 |
| `POST /api/channel/` | `{mode,multi_key_mode,template_id,channel:{type,name,key,base_url,models}}`，`mode` 为 `single`、`batch`、`multi_to_single`；`multi_key_mode` 可传 `random` 或 `polling`；`template_id` 也可放在 `channel` 里 | `data={ids}`，本地编号；只是提交成功，推送见下文 |
| `PUT /api/channel/` | `{id,name,models}`，`name`、`models` 不传时保持原值 | `{success:true,message:""}` |
| `POST /api/channel/{id}/status` | `{status}`，1 启用，2 禁用 | `data=true` |
| `GET /api/channel/test/{id}` | `model`，`endpoint_type`，`stream` | 顶层 `{success,message,time}`，测试失败是 `success:false` |
| `GET /api/channel/update_balance/{id}` | 无 | 顶层 `balance` 或 `raw_response` |
| `DELETE /api/channel/{id}` | 无 | `{success:true,message:""}`，是软删除，见上文 |
| `POST /api/channel/multi_key/manage` | `{channel_id,action,...}`，见下 | |
| `GET /api/log/self` | `start_timestamp`，`end_timestamp`（含端点），`channel`（本地编号），`type`，`model_name`，`group`，`request_id`，`upstream_request_id`，`p`，`page_size`（同列表） | `data={items,total,page,page_size}`，每项同 `/api/logs`（含 `pool_id`、`pool_name`），另有 `channel`，与 `channel_id` 相同。行的唯一键是 `pool_id` 加 `id` |
| `GET /api/log/self/stat` | 同上 | `data={quota,rpm,tpm,concurrency}` |
| `GET /api/data/self` | 同上 | `data=[{created_at,model_name,count,quota,token_used,cache_read_tokens,cache_creation_tokens}]` |

三个日志接口的数据可能不完整，这时多带两项，含义同「[日志、统计与审计接口](#日志统计与审计接口)」：`degraded:[{pool_id,name,reason}]`（没能查询的号池，`reason` 固定为“号池暂时不可用”）和 `retention:{since,pool_ids}`（这些 sync 号池只有 `since` 之后的日志）。`/api/log/self` 和 `/api/log/self/stat` 放在 `data` 里，`/api/data/self` 的 `data` 是数组，放在响应顶层（和 `success` 同级）。仍是 `success:true`；用于对账时先检查这两项。

渠道字段：`id,type,name,status,group,models,priority,weight,used_quota,balance,balance_updated_time,balance_from_keys,response_time,test_time,created_time,base_url,tag,test_model,auto_ban,channel_info,exists,template_id,push_state`，`push_state` 为 `pending`、`partial`、`failed`、`done`（见「[投放状态与重试](#投放状态与重试)」），不带号池信息；`channel_info` 只有 `is_multi_key,multi_key_size,multi_key_status_list,multi_key_polling_index,multi_key_mode`。不返回 `key`、`key_preview`、`header_override`、`param_override`、`setting`、`settings` 和其他字段；`base_url` 只读。

`multi_key/manage` 的 `action`：

| action | 参数 | `data` |
|---|---|---|
| `get_key_status` | `page`，`page_size`（默认 50），`status`（1 启用、2 手动禁用、3 自动禁用） | `{keys,total,page,page_size,total_pages,enabled_count,manual_disabled_count,auto_disabled_count,probe_enabled,probe_supported,total_balance}`，`keys` 每项 `{index,status,disabled_time,reason,balance,balance_at,balance_error}`，没有 `key_preview`；三个 count 不受筛选影响，页码超出时取最后一页 |
| `enable_key`、`disable_key` | `key_index`（必填） | 无 |
| `test_keys`（扩展） | `key_indexes` 或 `key_index`，`model`（必填） | `{results:[{index,ok,message,latency_ms}]}` |
| `get_key_balance`（扩展） | `key_indexes` 或 `key_index`，缺省全部 | 页面的 key 视图：`{probe_enabled,probe_supported,total_balance,keys}` |

`delete_key`、`delete_disabled_keys`、`enable_all_keys`、`disable_all_keys` 和未知 action 返回失败，并写 `success=false` 的审计：`enable_all_keys`、`disable_all_keys` 记为 `channel.key_enable`、`channel.key_disable`，其余记为 `channel.update`，`detail.rejected_action` 是 action 名，未知 action 一律写 `unknown`，不记录传入的文字。扩展的两个 action 受「多 key 渠道」一节的开关和限制约束。

和 new-api 的差异：

- 除上表外的 new-api 接口一律 404，包括硬删除、`/api/channel/search`、`/api/channel/models`、`/api/log/self/export`。
- **创建**：分组、优先级、权重、`param_override` 等由模板决定，请求里的这些字段忽略，不报错；`base_url` 只在模板允许时生效；`batch_add_set_key_prefix_2_name` 忽略（会把 key 片段写进渠道名）。类型下有多个模板时必须传 `template_id`。`key` 最多 64KB。`multi_to_single` 不传 `multi_key_mode` 时和 new-api 一样：创建成功，但之后固定用第一个启用的 key，既不随机也不轮询。要用上全部 key，请传 `random` 或 `polling`。
- **修改**：`key`、`base_url`、`key_mode`、`multi_key_mode`、`status` 带非空值时整个请求失败（`""`、`null` 视为未带），其余字段忽略。状态用 `/status` 接口改。失败时写 `channel.update` 的 `success=false` 审计，`detail.rejected_fields` 只有字段名，没有字段值。
- 列表没有 `type_counts`。
- **`exists:false`**：渠道已在 new-api 被硬删除，本系统只剩自己的记录。这种渠道 `status` 为 0，会出现在 `status=disabled` 的结果里。启停、测试、查余额返回 404；`DELETE` 返回成功，只把本系统的记录标记删除，不调用 new-api。“查禁用再删除”的脚本会连这些一起清掉，要保留时先判断 `exists`。
- `/api/log/self` 按门户日志页的口径，不是令牌用户自己的请求：默认全部日志（终端用户打码），门户用户的日志可见范围为 `self` 时收窄到名下渠道。`type` 只有 2、5、6，缺省或 0 为这三种。`username`、`token_name` 不生效。
- `/api/data/self` 只含名下渠道（含已删除渠道的历史）。
- 时间跨度上限 92 天；都不传时取最近 92 天，超出返回失败。`end_timestamp` 含端点，所以 `end_timestamp - start_timestamp` 等于 92 天整会失败，少 1 秒通过。

**创建不去重，也没有幂等键**：同一个 key 提交两次就是两个渠道。创建请求返回失败或超时时不要盲目重试，先用列表核对渠道是否已经建好；`message` 含“结果未知”“请联系管理员”时不要重试，联系管理员核对。

**提交是异步的**：创建成功只表示本系统收下了，后台再逐个推到模板勾选的号池。推送完成前测试、查余额和 key 操作返回 409“渠道正在推送到号池，请稍后再试”，启停、改名、删除可以直接调；用 `GET /api/channel/{id}` 看 `push_state`，`done` 或 `partial` 后再测。`failed` 时 409“渠道推送到号池失败，请联系管理员重试”，不要重新创建，联系管理员在后台重试。

**新渠道同步前 409**：推送完成后，new-api 的内存缓存要到下一次同步（`SYNC_FREQUENCY`）才认识新渠道，这期间启停、查余额、删除返回 409，等一会儿再调，渠道本身已建好，不要重新创建。

```bash
BASE=https://portal.example.com
TOKEN=ptl_...

# 批量上 key，一行一个，每个 key 一个渠道
curl -sS "$BASE/api/channel/" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{
  "mode": "batch",
  "channel": {"type": 20, "name": "my-keys", "key": "<key1>\n<key2>", "models": "model-a,model-b"}
}'
# {"success":true,"message":"","data":{"ids":[10001,10002]}}

# 等推送完成：push_state 为 done 或 partial 后再测试、查余额
curl -sS "$BASE/api/channel/10001" -H "Authorization: Bearer $TOKEN"

# 查禁用的渠道
curl -sS "$BASE/api/channel/?status=disabled&p=1&page_size=100" -H "Authorization: Bearer $TOKEN"

# 关掉多 key 渠道的一个 key
curl -sS "$BASE/api/channel/multi_key/manage" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"channel_id": 10001, "action": "disable_key", "key_index": 3}'
```

**升级后的变化**（多号池版本，对照之前的程序接口）：

- 创建返回的 `ids` 是本地编号，新渠道从 10000 起（或原最大编号加 1），不再等于 new-api 的渠道 id；已有渠道的编号不变。
- 创建立刻返回，推送完成前测试、查余额和 key 操作返回 409（见上文），按 409 重试的脚本不用改；202 也算成功（`success:true`）。
- 渠道多了 `push_state`。
- 日志的 `channel`、`channel_id` 和 `channel` 筛选都是本地编号；不是本系统的渠道（号池里别人的渠道）为 0，不再是它的 new-api id。
- 日志和统计包含所有号池，额度已换算到主号池单位；sync 号池只有最近 30 天。
- 日志行多了 `pool_id`、`pool_name`。日志 `id` 是各号池 new-api 自己的编号，不同号池会重复，也不再按时间单调递增：去重用 `pool_id` 加 `id`；增量拉取按 `created_at` 的时间窗口，不要按“`id` 大于上次”。
- 日志接口可能带 `degraded`、`retention`（见上文），这时数据不完整，但仍是 `success:true`。

## 前端开发约定

- 修改复制来的文件：保留原版权头，在其后加 `// Modified YYYY-MM-DD by new-api-sub: <摘要>`；新文件使用 `Copyright (C) 2026 new-api-sub contributors` + AGPL 头。品牌为「小蓝号池」「小蓝号池管理后台」与自有 Logo，不使用 New API 名称或 Logo。
- 目录：`src/apps/<app>/`（入口、`app-config.tsx` 菜单与品牌、路由、各自 `routeTree.gen.ts`）；两端共用业务组件放 `src/features/`；中文译文放 `src/i18n/app/*.zh.json`（key 为英文原文）。
