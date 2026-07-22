# octo-cli 消息检索集成 · 完整技术方案

> 汇总自三份评审文档(CLI 集成方案 / 统一端点设计 / 命令树 v5)。
> 依据:octo-server @ `ef4e1854`(PR #608 已合)+ octo-cli @ `640e71a`,全部实读源码。
> 目标:让 octo-cli 支持消息检索,覆盖 server 侧三主体(as-bot / OBO / uk),Agent 友好,且零改 CLI loader 核心。

---

## 一、背景与目标

PR #608 已在 octo-server 落地消息检索(messages_search 模块),对外通过三条并行入口暴露 8 个搜索端点。现要在 octo-cli 增加对应命令,让 Agent/人可用 CLI 检索消息。

约束:
- octo-cli 命令 **spec 驱动**(`//go:embed specs/*.json` 自动注册),不手写 cobra 命令 → 集成应以「加 spec」为主。
- 命名 Agent 友好:`--chat-id`(不是 --channel-id)。
- 权限判定全在 server,CLI 只做前缀路由 + 参数透传,不复制鉴权。
- octo-cli upstream push 禁用;两仓远端写均需授权。

---

## 二、Server 侧现状(可对接的接口)

### 2.1 三主体 / 三入口

| 主体 | 前缀 | token | 授权模型 |
|---|---|---|---|
| as-bot | /v1/bot/messages | bf_ | bot allowlist(好友 ∪ 已入群 ∪ 群下 Thread) |
| OBO | /v1/bot/messages(body on_behalf_of) | bf_ | active OBO grant + 逐频道 scope |
| uk | /v1/user/messages | uk_ | 真人可达集 + 实时 Space 成员门(YUJ-58) |

- **as-bot**:User Bot 自身身份。P2P 只判 IsFriend(authz.go:210 botCheckP2PAccess,已实现)。App Bot(app_)→ 403。
- **OBO**:resolveSearchPrincipal 按 on_behalf_of 切主体;入口 validateSearchOBO 查 grant 存在性(无→403),逐频道 scope+TOCTOU 下沉到 obo gate,经 SetOBOChecker 注入,复用发消息侧同一 checkOBO(读写一致)。
- **uk**:botfather 挂载,以真人 API Key 身份直接搜;resolveUKPrincipal 内联补 CheckMembership 实时 Space 成员门(绕过 SpaceMiddleware 后必须补,否则存量 key 越权枚举 DM)。

### 2.2 八个搜索端点(三入口共用同一 messages_search Handler)

| 端点 | 用途 | 态 |
|---|---|---|
| _search | 频道内纯消息(type 1,11,14) | 会话内 |
| _search_all | 消息+文件混排(1,8,11,14) | 会话内 |
| _search_around | 锚点上下文窗口 | 会话内 |
| _search_files | 频道内文件(type 8) | 会话内 |
| _search_media | 图片/视频(2,5,keyword 必空) | 会话内 |
| _search_global_messages | 跨频道消息(混合,同 _search_all 白名单) | 跨会话 |
| _search_global_files | 跨频道文件 | 跨会话 |
| _search_global_groups | 跨频道按群聚合概览(L1) | 跨会话 |

关键事实:_search_all 与 _search_global_messages payload.type 白名单**完全一致**(都是混合 feed),差别仅频道范围 term(单) vs terms(allowlist)——这正是「有无 chat_id」本身。

### 2.3 搜索中间件链
```
authBot → botActorUID → SharedUIDRateLimiter → resolveSearchPrincipal → searchRateLimiter → audit → backendGate
```
刻意不挂 SpaceMiddleware(bot 无 space_member);限流按 botUID(botActorUID 落 uid)。

---

## 三、octo-cli 现状(决定集成形态)

- loader(`internal/registry/loader.go`):embed specs,按 `x-octo-service` 建表;`OperationDetail.Path` 是**单一固定 path**——一个 operationId 死绑一个 URL,**不支持运行时按参数切 path**。
- 命令树构建(`cmd/service/service.go:68`):`strings.Split(d.ID, ".")` 按 operationId 的 `.` 分段建命令层级(`message.search.all` → `message search all`),中间 resource 节点自动补。
- 现有 9 spec 无 search;credential 层(token.go)只认 `app_`/`bf_`,**无 uk_**。
- message.json 的 sendMessage 已带 `on_behalf_of` 字段 → OBO 在 CLI 侧已有模板。
- 支持机制:`x-octo-flag`(body 字段提升为干净 flag 名,wire key 不变)、`x-octo-pagination`(--page-all)、`x-octo-risk`、`x-octo-space-header`。

**核心矛盾**:v5 设计里 search/all/files「带 --chat-id 走会话内、不带走 global」= 一个 operationId 两个 path,与 loader 单 path 模型冲突。

---

## 四、解决方案:后端新增 unified 端点(主人拍板)

不在 CLI 做条件路由,而是给需要切换的命令各加一个后端 unified 门面端点,由后端按 body 有无 channel_id 分发。CLI 侧每命令绑一个固定 path,**零改 loader**。

### 4.1 新增 3 个端点

| CLI 命令 | 新增端点 | 有 chat_id → | 无 chat_id → |
|---|---|---|---|
| message search | `POST /v1/bot/messages/_search_unified` | _search(纯消息) | _search_global_messages(混合) |
| message search all | `POST /v1/bot/messages/_search_all_unified` | _search_all | _search_global_messages |
| message search files | `POST /v1/bot/messages/_search_files_unified` | _search_files | _search_global_files |

不新增(本就单态,绑原端点):media(仅会话内)· around(仅会话内)· groups(仅跨会话)。

### 4.2 门面(facade)落地:不重写检索逻辑

```go
func (ba *BotAPI) searchUnified(inScope, globalScope wkhttp.HandlerFunc) wkhttp.HandlerFunc {
    return func(c *wkhttp.Context) {
        chatID := probeChannelID(c)   // 复读+还原 body,抄 parseSearchOnBehalfOf(search_route.go:157) 模式
        if strings.TrimSpace(chatID) != "" {
            inScope(c)                // 现有 _search / _search_all / _search_files handler
        } else {
            globalScope(c)            // 现有 _search_global_messages / _search_global_files handler
        }
    }
}
```
- 门面挂**同一条中间件链末端**,principal(as-bot/OBO/uk)已解析好,下游 handler 直接读同 context → 三主体天然复用,无需为 unified 单独处理鉴权。
- 校验下沉到被分发的 handler,门面不做参数校验。

### 4.3 装配(推荐形态 A)

messages_search.Handler 加 `MountUnified(r, prefix, mws...)`,把 3 个 unified 路由用已有内部 handler 组合注册。bot_api / botfather 各调一次 MountUnified → **三入口对称**,分发逻辑单点收在 messages_search 内。

---

## 五、CLI 侧改动

### 5.1 新增 search.json spec
```
message.search        → /v1/bot/messages/_search_unified        (--chat-id 可选)
message.search.all    → /v1/bot/messages/_search_all_unified     (--chat-id 可选)
message.search.files  → /v1/bot/messages/_search_files_unified   (--chat-id 可选)
message.search.media  → /v1/bot/messages/_search_media           (--chat-id 必填)
message.search.around → /v1/bot/messages/_search_around          (--chat-id 必填)
message.search.groups → /v1/bot/messages/_search_global_groups   (--chat-id 禁用)
```
每 operationId 一个固定 path,loader 零改。全部 `x-octo-risk: read`、`x-octo-space-header: true`。

### 5.2 credential 层加 uk_
token.go 加 `prefixUK = "uk_"`;TokenKind 补 `user_key`;MaskToken 同步;env/file provider 放行 uk_ 加载。

### 5.3 client 按 kind 选前缀
```
user_bot (bf_) → /v1/bot/messages
user_key (uk_) → /v1/user/messages
app_bot (app_) → fail-fast:提示"App Bot 不支持检索"(server 侧 403)
```

### 5.4 OBO flag
search.json 各端点 requestBody 加 `on_behalf_of` + CLI flag `--on-behalf-of <grantorUID>`(抄 message.json 模式),仅 user_bot 可用。

---

## 六、命令树(Agent 友好)

```
octo search messages   --chat-id <id> --keyword <kw> [--chat-type --page-size --cursor]
octo search all        --chat-id <id> --keyword <kw>          # 消息+文件混排
octo search around     --chat-id <id> --anchor-message-id <m> # 上下文窗口
octo search files      --chat-id <id> [--keyword]
octo search media      --chat-id <id>                         # keyword 必空
octo search groups     --keyword <kw>                         # 跨会话聚合概览 L1(--chat-id 禁用)
# messages/all/files:带 --chat-id 会话内,不带则后端 unified 自动跨会话
# OBO(bf_ only):任意子命令 + --on-behalf-of <grantorUID>
# uk:换 uk_ token 自动走 /v1/user 前缀,命令树不变
```
两级检索:先 `groups` 看关键词命中哪些会话 → 再对具体会话 `messages`/`all` 拉逐条。

---

## 七、语义提醒(写进接口文档)

- `_search_unified` 无 chat_id 时唯一可落 global 端点是混合版(_search_global_messages)→ 跨会话 search 会带文件命中,语义从「纯消息」变「消息+文件混合」。必须在 description 明示。
- all/files 无此问题(两态语义本就一致)。
- 校验差异(后端做,CLI 透传):会话内 keyword+filter 不能同时空、relevance 要 keyword 非空、media keyword 必空;跨会话允许全空(浏览模式)、不支持 relevance。
- 结果为空可能是越权被静默过滤(存在性隐藏),不可据此推断频道不存在。

---

## 八、实施顺序与改动面

阶段(建议):
1. 后端:messages_search 加 MountUnified + 3 门面 handler(纯分发)。
2. 后端:bot_api / botfather search_route.go 各追加 MountUnified(三入口对称)。
3. 后端:3 unified 端点 OpenAPI 描述 + 语义说明 + 门面分发单测 + 三主体×unified 鉴权回归。
4. CLI:新增 search.json(先只挂 bf_ 跑通 as-bot)。
5. CLI:credential 补 uk_ + client 前缀路由。
6. CLI:--on-behalf-of(OBO)+ App Bot fail-fast。

改动面清单:
- 后端(octo-server,需授权):messages_search MountUnified + 3 handler;bot_api/botfather 装配;OpenAPI;测试。纯新增,不改老 8 端点,web 前端零影响。
- CLI(octo-cli,需授权):search.json;token.go uk_;client 前缀路由;OBO flag。

风险/边界:
- 门面不含检索逻辑 → 不引入新鉴权/可见性风险,判定仍在被分发 handler + principal 链。
- 老端点全保留(web 在用)。
- octo-cli upstream push 禁用;两仓远端写均需主人明确授权。
