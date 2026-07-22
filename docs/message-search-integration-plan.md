# octo-cli 消息检索能力落地方案

> 依据文档:`octo-cli-search-integration.html`(基于 octo-server PR #608 `feat/messages-search-principal`)
> 核实代码:octo-server @ `ef4e1854`(已同步到最新 upstream/main) + octo-cli @ `0eb40d1`
> 全部结论来自实读代码,非文档转述。

> ⚠️ **与既有设计的关系**:octo-cli/docs 里已有一份 `octo-cli-search-command-tree.md`(2026-07-16),定义了命令形态(`message search [all|files|media|around]`)、口语化 flag(`--chat-id`/`--keyword`)、端点映射、payload.type 白名单——**那部分命令树设计仍有效,不要推翻**。本方案是它的**契约增量**:PR #608 把检索主体从「as-bot + OBO 两态」升级为「as-bot / OBO / **user-key(uk)** 三主体 + 三条并行路由前缀 + YUJ-58 Space 复检」。即命令树照旧,凭证/路由/主体这层按下文对齐。

---

## 0 · 一句话结论

服务端(PR #608)已把同一套 `_search*` handler 通过**子树挂载**暴露成三条并行路由,octo-cli 侧要做的核心不是"写一堆命令",而是:
**加一份 search OpenAPI spec(走现有 spec 自动注册机制)+ 补齐凭证层的 `uk_` 主体 + 处理 base 路由前缀/OBO 请求体字段**。
最小可用集 = 加 spec + 让 client 按凭证 kind 选前缀,as-bot / user-key 单频道 `_search` 即可打通。

---

## 1 · 服务端已提供什么(实读核实)

PR #608 核心机制在 `modules/messages_search/route_subtree.go:54 MountSubtree`:同一个 `Handler` 实例(共享限流桶/sender 缓存),按前缀挂三棵路由树,端点集合与 web `/v1/messages` 完全一致。

| 路由前缀 | 主体 | 鉴权 | subject | 挂 SpaceMiddleware? |
|---|---|---|---|---|
| `/v1/messages` | 真人 web(现存) | 登录态 | loginUID | 是 |
| `/v1/bot/messages` | as-bot / as-user(OBO) | `Bearer <bot_token>` | botUID / grantorUID | **否** |
| `/v1/user/messages` | user-key(uk) | `Bearer uk_…` | keyUID(真人) | **否** |

bot/uk 两链刻意不挂 SpaceMiddleware:
- bot 无 space_member(spaceID 恒空,走 per-principal allowlist 收敛)
- uk 的 spaceID 走 key 上的 `api_key_space_id`,成员资格由 `resolveUKPrincipal` 内联复检(YUJ-58)

核实锚点(都已读到真实代码):
- `bot_api/search_route.go:42 mountSearchRoutes` → `MountSubtree(r,"/v1/bot/messages",...resolveSearchPrincipal...)`
- `bot_api/search_route.go:62 resolveSearchPrincipal`:按 body 有无 `on_behalf_of` 切 as-bot / OBO 主体
- `bot_api/search_route.go:142 validateSearchOBO`:OBO 入口 fail-fast(grant 存在性)
- `bot_api/obo_check.go checkOBO`:grant + scope + `grantorCanReadChannel` 实时 TOCTOU
- `botfather/search_route.go:48 resolveUKPrincipal`:uk 主体 + YUJ-58 Space 成员实时复检 → `ErrSharedForbidden`

---

## 2 · 三种主体 & 凭证(与 octo-cli 现状对照)

| 主体 | 凭证形态 | 路由前缀 | 额外字段 | octo-cli 现状 |
|---|---|---|---|---|
| ① as-bot | User Bot `bf_` token | `/v1/bot/messages` | 无 | ✅ 已识别 `bf_`(user_bot) |
| ② as-user(OBO) | 同 `bf_` token | `/v1/bot/messages` | body `on_behalf_of` | ⚠️ 需加 `--on-behalf-of` 注入 |
| ③ user-key(uk) | 用户 API Key `uk_` | `/v1/user/messages` | 无 | ❌ 未识别 `uk_` 前缀,无 user_key kind |

octo-cli 凭证层现状(`internal/credential/token.go`):
- 只认两个前缀:`prefixApp="app_"`、`prefixUser="bf_"`;`TokenKind` 返回 app_bot / user_bot / unknown
- **没有 `uk_`**,`MaskToken` 也不认 `uk_`(会 fallback 到 `***`)
- ⚠️ App Bot(`app_`)在服务端 search 分支**整体 403 拒绝**(`ErrBotAPIBotUnavailable`),octo-cli 要在客户端就拦掉给友好提示

---

## 3 · octo-cli 命令是 spec 驱动的(关键机制,即"自动注册")

**octo-cli 没有手写 cobra 命令。** 命令来自 `internal/registry/specs/*.json`(嵌入的 OpenAPI 3.1 文档),由 `internal/registry/loader.go` 在运行时解析:

```
specs/*.json  ──go:embed──▶  registry.New()  ──▶  遍历 paths[].operationId
                                                   ──▶ 生成命令 + flag + 请求构建
```

- `loader.go:29 //go:embed specs/*.json`;`New()` 遍历目录,每份 spec 以 `x-octo-service` 命名注册,重名报错
- 每个 operation 的元数据(parameters / requestBody schema / `x-octo-risk` / 分页)驱动 flag 生成、请求构建、`octo-cli schema` 输出
- spec 顶层扩展字段:`x-octo-service`(服务名)、`x-octo-base-url`(如 `OCTO_API_BASE_URL`)、`x-octo-space-header`(是否发 `X-Space-Id`)
- 每个 operation:`x-octo-risk: read|write`

**含义:加 search 能力 = 新增一份 `search.json` spec,大部分工作是声明式的,不用为每个端点写 Go 命令。**

client 侧(`internal/client/client.go`):
- `attempt()` 统一 `Authorization: Bearer <cred.Token>`(line 208)
- `cred.SpaceID != "" && !suppressSpaceHeader` 时发 `X-Space-Id`(line 210);spec `x-octo-space-header:false` → `SuppressSpaceHeader`
- URL = base(来自 `OCTO_API_BASE_URL`)+ path;retry 只对 5xx/429 退避(line 158+)

---

## 4 · 8 个端点(三主体共用,path 只换前缀)

> **⚠️ 订正:** 下表把 global messages/files 建模为独立 operationId(`search.global.*`),**与最终实现不符**。实际 operationId 前缀是 `message.search.*`(挂在 message 域下,非顶层 search 域),且 **global messages / global files 不是独立命令**——它们由 `message.search` / `message.search.all` / `message.search.files` 在**无 --chat-id** 时,由 CLI client(`search_route.go` 的 `searchGlobalFallback`)改写落到 `_search_global_messages` / `_search_global_files`。真正独立成命令的 global 端点只有 **groups**(`message.search.groups`)。即:6 个命令 + chat-id→global 回退,而非 8 个独立命令。

| operationId(实际) | path 后缀 | 用途 | risk |
|---|---|---|---|
| `message.search` | `_search`(无 chat-id → `_search_global_messages`) | 频道内消息 / 跨频道混合 | read |
| `message.search.all` | `_search_all`(无 chat-id → `_search_global_messages`) | 消息+文件混排 | read |
| `message.search.around` | `_search_around` | 锚点上下文(仅会话内) | read |
| `message.search.files` | `_search_files`(无 chat-id → `_search_global_files`) | 文件(硬锁 type=8) | read |
| `message.search.media` | `_search_media` | 图片/视频(仅会话内,keyword 必空) | read |
| `message.search.groups` | `_search_global_groups` | 按群聚合(仅跨会话,带 sequence 回显) | read |

- `channel_type`:1=DM(peer uid) / 2=群(group_no) / 5=Thread(`{group_no}____{short_id}`)
- 单频道端点空 keyword+无 filter → 400;全局端点允许(浏览模式)

---

## 5 · 与现有架构的落地 gap(要动的地方)

### gap A — 凭证层加 `uk_`(P1 必须)
`internal/credential/token.go`:
- 加 `prefixUK = "uk_"`,`TokenKind` 增加 `user_key` 分支
- `MaskToken` 识别 `uk_` 前缀
`internal/credential/provider.go` / `config.go`:承载 user_key(env `OCTO_USER_KEY` 或 profile),`BotCredential` 已有 `BotKind` 字段可复用

### gap B — base 路由按主体选前缀(P1 必须)
现有 spec path 是写死的(如 `/v1/bot/sendMessage`)。search 三主体只有前缀不同:
- 方案(推荐):spec path 写 `/v1/bot/messages/_search`(as-bot/OBO 默认),client 在 kind=user_key 时把 `/v1/bot/messages` **重写为** `/v1/user/messages`
- 或:spec 里用 `x-octo-search-mount` 扩展声明可切换前缀,loader/client 据 cred.kind 选择
- 关键:前缀切换是**凭证 kind 驱动**,不是用户手选(避免 token 与前缀错配)

### gap C — OBO 请求体字段(P4)
`--on-behalf-of <uid>` → 仅当 kind=user_bot 时,往请求 body 注入 `on_behalf_of`;kind=user_key/app_bot 时该 flag 应报错拒绝

### gap D — 错误码 → 友好文案(P1)
| 服务端 | 客户端处理 |
|---|---|
| 403 `ErrBotAPIBotUnavailable` | "本期仅支持 User Bot 搜索",不重试 |
| 403 `ErrBotAPIOBONotAuthorized` | "grantor 需先授权该 bot",不重试 |
| 5xx `ErrBotAPIOBOInternal` | 可退避重试 |
| 403 `ErrSharedForbidden`(uk) | "key 失效/需重新签发",不重试 |
| 401 | 检查凭证 |
| 200 但结果不含目标 | 视为"无此结果",**不得**据此推断频道存在/无权限 |

fail-close 语义:越权一律折叠为空结果(防枚举),基础设施出错一律 fail-close。octo-cli **不应**把"空结果"与"无权限/不存在"区分展示。

### gap E — 分页/限流(P5)
- `pagination.next_cursor` 恒输出(可为空串),续页原样回传到 `cursor`;游标含 HMAC,**只透传不拼接**;深度上限 10000
- 限流按主体(as-bot/OBO 按 botUID,uk 按 keyUID),同 bot 多入口共享配额;429 退避
- `_search_global_groups` 的 `sequence` 请求带、响应回显,用于丢弃陈旧响应

---

## 6 · 分阶段落地清单

- **P1 凭证 & 传输层**:token.go 加 `uk_`;client 按 kind 选前缀;错误码→文案映射(§5D)。**打通 as-bot + user_key**
- **P2 单频道搜索**:`search.json` 声明 `_search`/`_search_all`/`_search_files`/`_search_media`/`_search_around`;flag=channel_type/id、keyword、sender、时间范围;表格/JSON 输出
- **P3 全局搜索**:`_search_global_*`;channel_ids/types/member_uids/content_types/file_exts;空 keyword 浏览模式
- **P4 OBO**:`--on-behalf-of`,专门处理 `OBONotAuthorized` 文案;仅 bot profile 生效
- **P5 分页**:游标透传、`--all` 自动翻页(带上限)、`--page-size`、429 退避
- **P6 uk 生命周期**:可选,`uk_` 获取/轮换走 botfather onboarding;403 时提示 Space 成员变更失效

**最小可用 = P1 + P2 的 as-bot / user_key 单频道 `_search`。** OBO(P4) 依赖服务端 grant 已建立,是独立里程碑。

---

## 7 · 风险 / 待确认

1. **base 前缀切换 + chat-id→global 回退的实现位置**:最终由 **client 侧 `search_route.go` 的 `routeSearchPath` 处理两处逻辑**——(a) 按 cred.kind 切换路由前缀(`uk_` → `/v1/user/messages`),(b) `search`/`all`/`files` 在无 chat-id 时把 suffix 改写为 `_search_global_*`。spec 保持每个 operationId 单一固定 path。(原以为「前缀切换是唯一需碰 client 的点」,实际还多了 chat-id→global 回退这一处。)
2. **App Bot 拦截**:服务端已 403,客户端建议在发请求前就按 `app_` 前缀拦掉,省一次往返。
3. **uk 获取路径**:文档说 `uk_` 由 botfather onboarding 签发(`getOrCreateUserAPIKey`),octo-cli 是否要集成签发,还是仅消费已有 key —— 建议 P1 只消费,P6 再考虑签发。
4. 服务端 PR #608 已合入 upstream main(octo-server 现 HEAD `ef4e1854` 含全部 6 个 commit),契约稳定,可直接对接。
