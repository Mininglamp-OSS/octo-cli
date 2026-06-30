---
type: Rule
title: JSON envelope I/O contract
description: All command output goes through the Factory→output JSON envelope; never write raw to stdout/stderr.
tags: ["envelope", "output", "wire-contract", "exit-code"]
timestamp: 2026-06-30T00:00:00Z
# --- octospec extension fields (OKF-permitted; consumers must preserve) ---
id: envelope-io-contract
tier: repo
priority: 92
load_bearing: true
inject_when:
  paths: ["cmd/**/*.go", "internal/output/**/*.go", "internal/cmdutil/factory.go"]
  touches: ["envelope", "output", "stdout", "stderr", "wire-contract", "exit-code"]
source: self
supersedes: []
---

# JSON envelope I/O contract

命令产物只能通过统一 JSON 信封发出。agent runtime 把 stdout/stderr 当作唯一解析入口，
任何裸输出都会破坏这个 wire contract。

> See CLAUDE.md § "Architecture" → "JSON envelope I/O"。

## Rules

- 成功走 `Factory.EmitSuccess` / `EmitSuccessWithMeta` → `output.WriteSuccess`，stdout 形如
  `{ok:true, identity, data, _pagination, _rate_limit, _notice}`（`internal/output/envelope.go`）。
- 失败走 `Factory.EmitError` → `output.WriteError`，stderr 形如
  `{ok:false, error:{type,code,message,hint,detail}}`。
- `--jq` 在**信封成形之后**再过滤（`Factory.emit`：先 `WriteSuccess` 进 buffer 再 `ApplyJQ`），
  保证 jq 作用于规范信封而非后端原始形状。
- `identity` 永远是对象：默认 `{type:"bot"}`，认证过的命令补 `{profile,robot_id,bot_kind,source}`
  （`Factory.identityValue`）——只读已缓存的 credential，从不强制解析。
- `_pagination` 仅在后端返回 `{data:[], pagination:{}}` 形状时拆分平铺（`splitPagination`）。
- `Factory.ErrorEmitted` 防止 RunE 与顶层 main 双重输出错误信封。
- **禁止**在 handler/RunE 里直接 `fmt.Fprintln(os.Stdout, …)` 或手写 JSON 响应。

## Why load-bearing

stdout/stderr 的信封是 agent runtime 唯一解析入口；任何绕过信封的裸输出都会让上游误判命令成败。
