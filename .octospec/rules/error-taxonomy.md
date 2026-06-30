---
type: Rule
title: Error taxonomy & exit codes
description: Errors converge on *output.ExitError; backend errors map via ParseBackendError; exit codes are fixed and part of the wire contract.
tags: ["error", "exit-code", "taxonomy", "backend-error", "wire-contract"]
timestamp: 2026-06-30T00:00:00Z
# --- octospec extension fields (OKF-permitted; consumers must preserve) ---
id: error-taxonomy
tier: repo
priority: 85
load_bearing: true
inject_when:
  paths: ["internal/output/errors.go", "internal/client/client.go", "internal/cmdutil/factory.go", "cmd/**/*.go"]
  touches: ["error", "exit-code", "taxonomy", "backend-error", "wire-contract"]
source: self
supersedes: []
---

# Error taxonomy & exit codes

所有到达顶层的错误必须是 `*output.ExitError`，使信封渲染器能输出结构化错误，agent 据
`type`/`code` 决策。与 `envelope-io-contract` 分工：envelope 管「怎么发」，本 rule 管「错误如何分类」。

> See CLAUDE.md § "Code Style"（ExitError taxonomy）& "Architecture"（exit codes）。

## Rules

- taxonomy：`auth_error | validation | api_error | network | rate_limited | permission | internal | config`（`errors.go`）。
- 退出码固定：`auth_error`→3，`validation`/`config`→2，其余→1（`ExitError.ExitCode`）。
- 构造器：`ErrAuth/ErrValidation/ErrAPI/ErrNetwork/ErrWithHint`；非 ExitError 经 `WrapCLIError`
  （窄启发式：缺 token / 未知 flag / 参数错）分类后再渲染。
- 后端错误经 `ParseBackendError` 三层解析：matters 信封 `{error:{code,message,details}}` →
  dmworkim `{msg,status}` → 原始 body + status 推断；code→type/hint 经**不可变**映射表 `backendErrorMapping`。
- HTTP status→type/code 的映射（`typeFromStatus`/`codeFromStatus`）是 wire 契约的一部分，改动需同步 agent 侧预期。
- 用 `errors.As` 解包（`AsExitError`），使 retry 包装器 `retryableErr` 的结构化信息仍可穿透。

## Why load-bearing

退出码与 `error.type/code` 是 agent 的分支依据；裸 error 或错误码会让上游误判可重试性 / 是否换凭证。
