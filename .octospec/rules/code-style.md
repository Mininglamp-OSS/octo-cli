---
type: Rule
title: Code style
description: gofmt/vet clean; wrap errors with %w; all user-facing text English (no i18n); dependency allowlist; underscore JSON fields.
tags: ["style", "gofmt", "error-wrap", "dependency", "i18n-english"]
timestamp: 2026-06-30T00:00:00Z
# --- octospec extension fields (OKF-permitted; consumers must preserve) ---
id: code-style
tier: repo
priority: 55
load_bearing: false
inject_when:
  paths: ["**/*.go", "go.mod"]
  touches: ["style", "gofmt", "error-wrap", "dependency", "i18n-english"]
source: self
supersedes: []
---

# Code style

> See CLAUDE.md § "Code Style" & "Build & Test"。

## Rules

- `gofmt` + `go vet` 必过；golangci-lint 在 CI 强制（`.golangci.yml`、`Makefile ci`）。
- 错误包裹 `fmt.Errorf("context: %w", err)`；CLI 错误用 `*output.ExitError` taxonomy（见 `error-taxonomy`）。
- 外部依赖严格限定 cobra / gojq / `golang.org/x/term` + 标准库；**新增依赖需强理由**。
- **全部用户可见文案 English；本仓不做 i18n**（与 octo-server 的 i18n 信封相反——别误把 i18n 约定搬过来）。
- on-disk JSON 字段下划线命名（如 `api_base_url`、`robot_id`，`authstore.ProfileMeta`）。
- 注：通用 CLAUDE.md 模板里的 JavaDoc `@author` 约定**不适用**本 Go 仓（无此约定，勿误植）。
