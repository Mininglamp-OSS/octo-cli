# octo-cli 完整命令树 · 消息检索（🆕 高亮 · 以源码为依据）

> 依据：octo-server modules/messages_search 源码核对 + 本轮讨论修订。
> 🆕 = 本次新增。命名遵循既有约定：`域 资源 动作` 由 OpenAPI spec 自动注册；flag 走 Agent 友好口语化（`--chat-id` 而非 `--channel-id`）。
> --chat-id 下沉到各子命令，由每个 operation 的 spec 单独声明必填/可选。

## 一、完整命令树（全域，🆕 高亮）

```
octo-cli
│
├─【service 域 · spec 自动注册】
│
├─ docs (31) ─ 文档 / 表格 / 白板
│    ├─ 直接        create · list · get · rename · delete · forward-grant
│    ├─ content     get · edit
│    ├─ sheet       get · edit
│    ├─ scene       get · edit · export
│    ├─ members     list · set · remove
│    ├─ share       get · set
│    ├─ comments    list · add · edit · delete
│    ├─ versions    list · create · state · rename · delete · restore
│    └─ attachments presign · get · resolve
│
├─ message (4 → 10) ─ 消息
│    ├─ 直接        send · edit · sync · read-receipt
│    └─ 🆕 search ─ 消息检索（OpenSearch 直连 · risk=read）
│         │  会话内 / 跨会话 = --chat-id 有无；每个子命令自带 --chat-id 并声明必填/可选
│         │
│         ├─ 🆕 (默认)  --chat-id 可选：纯消息全文（text/forward/richtext，payload.type∈[1,11,14]）
│         ├─ 🆕 all     --chat-id 可选：消息+文件混合 feed（payload.type∈[1,8,11,14]，空 kw 追加图/视频）
│         ├─ 🆕 files   --chat-id 可选：文件（payload.type=8，按文件名/caption）
│         ├─ 🆕 media   --chat-id 必填：图片+视频（payload.type∈[2,5]，keyword 必空）
│         ├─ 🆕 around  --chat-id 必填：定位 anchor 消息上下文窗口
│         └─ 🆕 groups  --chat-id 禁用（仅跨会话）：按群/子区/DM 聚合概览（L1，哪些会话命中了）
│
├─ group (9) ─ 群
│    └─ list · get · members · md-get · md-update
│       create · update · member-add · member-remove          # 后 4 仅 User Bot
│
├─ thread (8) ─ 线程 / 子区
│    └─ create · list · get · members · join · leave · md-get · md-update
│
├─ bot (6) ─ Bot 生命周期
│    └─ register · user-info · space-members · set-commands · typing · heartbeat
│
├─ file (4) ─ 文件
│    └─ upload · download · credentials · presigned
│
├─ event (2) ─ 事件轮询
│    └─ list · ack
│
├─ matter (14) ─ 待办   ⚠️ x-octo-disabled 隐藏
│
└─【手写叶子命令 · 非 spec 生成】
     └─ schema · api · config(show) · auth(login·status·logout·list)
        skills · version · completion(bash·zsh·fish·powershell)
```

## 二、🆕 search 子命令 ↔ 端点映射（源码核对）

| CLI 命令              | operationId          | 会话内(带 --chat-id) | 跨会话(不带 --chat-id)            | --chat-id |
|-----------------------|----------------------|----------------------|-----------------------------------|-----------|
| 🆕 message search      | message.search       | POST /_search        | POST /_search_global_messages ※   | 可选      |
| 🆕 message search all  | message.search.all   | POST /_search_all    | POST /_search_global_messages     | 可选      |
| 🆕 message search files| message.search.files | POST /_search_files  | POST /_search_global_files        | 可选      |
| 🆕 message search media| message.search.media | POST /_search_media  | ✗（无 global media 端点）         | 必填      |
| 🆕 message search around | message.search.around | POST /_search_around | ✗（定位需单会话锚点）           | 必填      |
| 🆕 message search groups | message.search.groups | ✗（groups 仅跨会话）  | POST /_search_global_groups       | 禁用      |

> operationId 用**点号**分段（`message.search.all` → `message search all` 三级命令）；loader 按 `.` split 建命令树，连字符会错误生成两级命令。
> 「会话内 → 跨会话」的 path 改写由 **CLI client**(`internal/client/search_route.go` 的 `routeSearchPath`)按 token 前缀 + chat-id 有无完成,**非后端分发**;spec 里每个 operationId 仍绑一个真实固定 path。


后端有 3 个 global 端点：_search_global_messages（消息+文件混合）、_search_global_files（文件）、_search_global_groups（按群聚合概览 L1）。
media/around 没有跨会话端点 → --chat-id 必填；groups 只有跨会话端点 → --chat-id 禁用。

【🆕 补充：_search_global_groups 是什么】源码核对（search_global_groups.go）：这是「聚合优先(L1)」的跨会话概览端点，不返回逐条消息，而是返回「哪些群/子区/DM 命中了关键词」的聚合桶列表。每个桶（GroupBucket）含：channel_id/channel_type、群名、（子区还带 thread_id/name）、match_count（命中条数，OS doc_count 近似、match_count_approx=true）、latest_at（最新命中时间，从可见命中重算防泄露）、preview（若干条预览）。响应体 {sequence, query_id, total_groups(HLL 近似), groups[]}，无 sort/page_size/cursor（一次给一份概览，桶按 latest_at 倒序固定）。sequence 请求带、响应原样回显，供前端丢弃陈旧响应。
典型用法：两级检索的第一级——先用 groups 看「关键词命中了哪些会话」，再对具体会话用 _search/_search_all 拉逐条。

## 三、源码核对结论（为什么 search 跨会话 = 混合，带 ※）

payload.type 白名单（modules/messages_search 源码）：
- _search              = [1,11,14]           纯消息，无 file，返回 MessageHit
- _search_all          = [1,8,11,14]（空 kw +2,5）  消息+文件混合，返回 SearchAllHit{message|file}
- _search_global_messages = [1,8,11,14]（空 kw +2,5） 与 _search_all 白名单完全一致，混合
- _search_files / _search_global_files = [8]
- _search_media        = [2,5]

关键事实：
- _search（纯消息）与 _search_all（混合）不是同一套：前者无 file(8)、返回 MessageHit。
- _search_all 与 _search_global_messages 是同一套混合 feed，差别仅频道范围（term vs terms allowlist）——这正是"带/不带 --chat-id"本身。
- ※ 因此 search 不带 --chat-id 时，后端唯一可落的 global 消息端点是 _search_global_messages（混合）。
  即 search 跨会话会带上文件命中，语义从"纯消息"降级为"消息+文件混合"，与 all 跨会话一致。
  → 采纳主人决定：search 跨会话复用 _search_global_messages，接受该混合语义（文档明示，避免使用者困惑）。

## 四、🆕 检索主体（三选一：as-bot / OBO / user-key）

【🆕 修正 2026-07-20】PR #608 后检索主体从两种扩为三种，前缀不同：
```
默认(不传)              → as bot：bot 自身可见范围（bot 好友 ∪ 已入群 ∪ 群下 Thread），路由 /v1/bot/messages
🆕 --on-behalf-of <uid>  → as user(OBO)：以某用户身份检索（需该 user 预授 OBO grant 给本 bot，同用 bot_token），路由 /v1/bot/messages
🆕 user-key(uk_)         → 携用户 API Key 以真人身份检索（不在 bot_api，在 botfather），路由 /v1/user/messages
```
服务端 principal 解析（对齐后端「主体中立 SearchService」）：
```
无 flag          → requesterUID = robotID，按 bot 可见性解析 allowlist
🆕 --on-behalf-of → 校验 (grantor,bot) active OBO grant → requesterUID = grantor，按 grantor 重算 allowlist
```
前置：
- as_user 跨会话:checkOBO 是单频道门,全局无单一 channel_id → 以 grantor 为主体重算 allowlist。
- 放开 on_behalf_of「只能跟 token 创建者」(oboCreateGrant 的 CreatorUID==uid) → 配额度+审计+反枚举。（已按 grant 谓词处理，不再限制创建者）
- 【🆕 2026-07-20】App Bot(app_) 打检索 → bot_api 直接 403 ErrBotAPIBotUnavailable，CLI 客户端建议提前拦。
- ✅ 【已修正 2026-07-20】as bot 可见性:**已实现**，非待办。源码 messages_search/authz.go:210 botCheckP2PAccess（#C / YUJ-50）——bot 的 P2P 只判 IsFriend(bot,peer)，不做黑名单、不做 bot 分类；bot 可达集 buildBotAllowlist = 好友 ∪ 已入群 ∪ 群下 Thread。CLI 侧无需再等此后端设计。

## 五、🆕 flag 约定

范围开关:各子命令自带 `--chat-id`(=channel_id)，spec 单独声明必填/可选(search/all/files 可选;media/around 必填);会话内配 `--channel-type`。
会话内:`--keyword` · `--sort`(time_desc/time_asc/relevance) · `--page-size`;around 特有 `--anchor-message-id`。
跨会话:专属过滤走 `--data`(channel_ids · channel_types · member_uids · content_types · sender_ids · sent_at_*);files 额外 file_exts · file_size_min · file_size_max。
主体:`--on-behalf-of <uid>`(不传 = as bot)。
通用复用:`--page-all` / `--page-limit` · `--jq` · `--format table` · `--dry-run`。
spec 标注:`x-octo-risk: read` · `x-octo-space-header: true`。
校验差异(后端做,CLI 透传):会话内 keyword+filter 不能同时为空、relevance 要求 keyword 非空、media 要求 keyword 空;跨会话允许全空(浏览模式)、不支持 relevance。

## 六、🆕 用法示例

### 会话内（带 --chat-id）

```bash
# 纯消息全文检索（关键词 + 排序 + 分页）
octo-cli message search --chat-id c_123 --channel-type 2 --keyword "发布" --sort time_desc --page-size 50

# 会话内 · 按发送人 + 时间段（keyword 可空，但 keyword 与 filter 不能同时为空）
octo-cli message search --chat-id c_123 --channel-type 2 \
    --data '{"filters":{"sender_ids":["u_1"],"sent_at_from":"2026-07-01","sent_at_to":"2026-07-15"}}'

# 消息+文件混合
octo-cli message search all --chat-id c_123 --channel-type 2 --keyword "预算"

# 文件（按文件名 / caption）
octo-cli message search files --chat-id c_123 --channel-type 2 --keyword report.pdf

# 图片+视频（media 必带 --chat-id，keyword 必空）
octo-cli message search media --chat-id c_123 --channel-type 2

# 定位某条消息的上下文窗口（around 必带 --chat-id）
octo-cli message search around --chat-id c_123 --channel-type 2 --anchor-message-id 2077314883757445121
```

### 跨会话（不带 --chat-id，搜 bot 能读的所有会话）

```bash
# 纯消息动词跨会话 → 落 _search_global_messages（混合，会含文件命中，见第三节 ※）
octo-cli message search --keyword "上线"

# 混合 feed 跨会话
octo-cli message search all --keyword "发布" \
    --data '{"filters":{"channel_types":[2,5],"member_uids":["u_1"]}}'

# 跨会话只搜文件
octo-cli message search files --data '{"filters":{"file_exts":["pdf"],"file_size_min":1024}}'

# 浏览模式（keyword 与 filter 全空，列所有会话近期消息）
octo-cli message search all

# ✗ media / around 无跨会话端点，不带 --chat-id 由后端拒绝（channel_id 必填；CLI 不做本地强制，请求会发到后端返回校验错误）
octo-cli message search media          # 后端拒：channel_id required
octo-cli message search around         # 后端拒：channel_id required

# groups 仅跨会话（聚合概览，看哪些会话命中）；带 --chat-id 应报错
octo-cli message search groups --keyword "发布"
octo-cli message search groups --keyword "预算" --data '{"filters":{"channel_types":[2,5]}}'
```

### as user（--on-behalf-of，以某用户身份检索）

```bash
# 默认 as bot（不传 = bot 自身可见范围）
octo-cli message search all --keyword "上线"

# as user：以 alice 能读的会话为边界（需 alice 预授 OBO grant 给本 bot）
octo-cli message search all --keyword "上线" --on-behalf-of u_alice

# as user 会话内
octo-cli message search --chat-id c_123 --channel-type 2 --keyword "发布" --on-behalf-of u_alice
```

### 通用 flag（任意子命令可叠加）

```bash
# cursor 全量翻页 + jq 提取 + 表格输出
octo-cli message search all --keyword "发布" --page-all --jq '.data[].message.content' --format table

# 只看请求结构不真发（联调用）
octo-cli message search all --chat-id c_123 --keyword "x" --dry-run
```

## 七、落地优先级

1. P0:后端抽主体中立 SearchService + bot handler;CLI 加 5 operation(各自声明 --chat-id 必填/可选)。→ 会话内/跨会话靠 --chat-id 统一,纯封装现有端点,行为不变。
2. P1:🆕 --on-behalf-of(as user),前置补「as_user 全局 allowlist 生成」小设计 + 放开创建者限制配额/审计。
