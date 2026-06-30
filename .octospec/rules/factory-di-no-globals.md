---
type: Rule
title: Factory DI, no mutable globals
description: State flows only through cmdutil.Factory; no mutable package-level globals (three documented exceptions only).
tags: ["factory", "di", "dependency-injection", "globals", "architecture"]
timestamp: 2026-06-30T00:00:00Z
# --- octospec extension fields (OKF-permitted; consumers must preserve) ---
id: factory-di-no-globals
tier: repo
priority: 82
load_bearing: true
inject_when:
  paths: ["internal/**/*.go", "cmd/**/*.go"]
  touches: ["factory", "di", "globals", "dependency-injection", "state"]
source: self
supersedes: []
---

# Factory DI, no mutable globals

`cmdutil.Factory` 是唯一 DI 容器，命令只拿 `*Factory` 再按需解析其余依赖。

> See CLAUDE.md § "Architecture" → "Factory DI" & "Code Style"（no mutable package-level globals）。

## Rules

- 所有 accessor 惰性 + 缓存（`ConfigFunc/CredentialFunc/ClientFunc/RegistryFunc`，`factory.go`）；命令只取自己需要的。
- 测试经 hook 注入 stub（`TestFactory.SetClient/SetConfig/SetCredential`），不碰真实环境。
- **禁止可变包级全局**；唯一豁免（CLAUDE.md 明列、代码可核对）：
  - (a) ldflags 注入的构建元数据 `cmd/build.go`；
  - (b) `//go:embed` 文件系统 `internal/registry`、`skills`；
  - (c) 不可变查找表（`backendErrorMapping`、`httpMethods`）。
  这些是 const 等价、运行期不改。
- 新 leaf command 经 `NewXxxCmd(f *cmdutil.Factory)` 构造并在 `cmd/root.go` AddCommand。

## Why load-bearing

包级可变状态会破坏测试隔离与并发安全；Factory 边界是可测试性的根基。
