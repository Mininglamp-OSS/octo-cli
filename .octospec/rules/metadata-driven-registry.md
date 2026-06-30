---
type: Rule
title: Metadata-driven command registry
description: The service command tree is auto-registered from embedded OpenAPI specs; change an endpoint by editing a spec, not code. The CLI is a thin client.
tags: ["spec", "openapi", "registry", "command-registration", "architecture", "thin-client"]
timestamp: 2026-06-30T00:00:00Z
# --- octospec extension fields (OKF-permitted; consumers must preserve) ---
id: metadata-driven-registry
tier: repo
priority: 90
load_bearing: true
inject_when:
  paths: ["internal/registry/specs/**", "internal/registry/**/*.go", "cmd/service/**/*.go"]
  touches: ["spec", "openapi", "registry", "command-registration", "x-octo", "endpoint", "business-logic"]
source: self
supersedes: []
---

# Metadata-driven command registry

整棵 service 命令树在启动时由内嵌 OpenAPI 3.x spec 自动注册——**改端点 = 改 spec，不改代码**。

> See CLAUDE.md § "Architecture" → "Metadata-driven" & "Thin client"。

## Rules

- spec 经 `//go:embed specs/*.json` 内嵌；运行期无文件访问（`registry/loader.go`）。
- 新增 domain（`Makefile` help 亦载明）：写 `internal/registry/specs/<domain>.json` → 重新构建
  （embed 自动拾取）→ 全部 operation 自动注册，无需写命令代码。
- spec 保留为 `map[string]any`（非强类型 struct），以容忍 additive 变更和未知 `x-octo-*` 扩展。
- operationId `domain.verb` / `domain.resource.verb` → `octo-cli <domain> [<resource>] <verb>`；
  path 参数→位置参数；query 参数→typed flag；简单 body 顶层字段自动 promote 成 flag，
  复杂 body 走 `--data` 逃生口（`cmd/service/service.go`、`flags.go`）。
- 已用到的 `x-octo-*` 扩展契约（必须在 spec 侧表达）：`x-octo-service`、`x-octo-base-url`、
  `x-octo-space-header`、`x-octo-risk`、`x-octo-multipart`、`x-octo-binary-response`、
  `x-octo-pagination`、`x-octo-disabled`、`x-octo-status-values`。
- query flag 只在用户**显式 set** 时下发（`cobraCmd.Flags().Changed`），避免默认值覆盖后端默认（`run.go`）。
- 父命令对未知子命令必须 fail loud（`rejectUnknownSubcommand`）——否则 cobra 打印 help 并 exit 0，
  会让自动化把「已删除命令」误当成功。
- 注：`x-octo-risk` 目前仅作为元数据呈现在 `--help` / `schema`（`service.go: buildLongDesc`），
  **不是**交互式确认门（CLI 面向非交互 agent）。

## Thin-client positioning

业务逻辑在后端服务（matters / dmworkim）；**CLI 只做传输 + 校验 + 格式化**，不内联业务规则。

- 命令层只负责：从 flag/arg 构请求（`run.go: runOperation/resolveBody`）、发请求、把信封发出。
- 分页 `--page-all` 是少数 CLI 侧编排（`runPaginated` 跟 `has_more`/`next_cursor` 走页），但仍只搬运后端数据。
- 客户端校验限于本地可确定的（缺 base URL、path/query 非法、body 非 JSON 对象），其余交后端并经 `ParseBackendError` 透传。

## Why load-bearing

这是整个 CLI 的核心架构约束。绕过 registry 手写命令、或把业务规则写进 CLI = 双份真相源、
spec/规则与后端漂移，破坏「spec 即契约」的根基。
