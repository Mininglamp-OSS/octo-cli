---
type: Rule
title: Credential resolution & selector ambiguity
description: Credentials resolve through a file→env chain; multi-profile selection ambiguity is a hard error, never a silent guess.
tags: ["credential", "auth", "profile", "bot-id", "identity"]
timestamp: 2026-06-30T00:00:00Z
# --- octospec extension fields (OKF-permitted; consumers must preserve) ---
id: credential-resolution
tier: repo
priority: 88
load_bearing: true
inject_when:
  paths: ["internal/credential/**/*.go", "internal/cmdutil/factory.go"]
  touches: ["credential", "auth", "profile", "bot-id", "identity", "ambiguity"]
source: self
supersedes: []
---

# Credential resolution & selector ambiguity

运行期凭证解析走 `credential.Provider` 链：`FileProvider`(profile) → `EnvProvider`(env)。

> See CLAUDE.md § "Identity Model"（selecting a credential at runtime）。

## Rules

- Source 约定：源**缺席**返回 `(nil, nil)` 让链继续；源**存在但损坏**返回 error 终止链
  （`provider.go` 接口注释）。
- 选择器优先级：`--bot-id`/`OCTO_BOT_ID`（robot id，agent 主选择器）与 `--profile`（友好名）
  > 唯一/隐式 profile > `OCTO_BOT_TOKEN`。
- **歧义即硬错误，绝不静默猜测**：
  - ≥2 个 profile 且无选择器 → `StatusAmbiguous` → validation error（exit 2）；
  - 选择器给了但不匹配 → `StatusMissing` → auth error（exit 3）；
  - 0 个 profile → `StatusNone` → 落到 env（`file_provider.go` / `authstore.ActiveProfile`）。
- env token 不携带可验证身份：从 env 解析的 credential 的 `Profile/RobotID/BotKind` 留空
  （`provider.go: BotCredential` 注释）。
- `buildConfig` 只吞 `ErrNoCredential`（让 `cfg.Validate` 报熟悉的 `OCTO_BOT_TOKEN` 提示）；
  结构化解析错误与真实 IO 错误一律上浮，不得伪装成「缺 token」。
- token 类型仅看前缀：`app_*`→app_bot，`bf_*`→user_bot（`token.go: TokenKind`）；其余路由在服务端。

## Why load-bearing

错误的凭证解析 = agent 以错误身份执行操作；静默选择某个 bot 是跨身份越权风险。
