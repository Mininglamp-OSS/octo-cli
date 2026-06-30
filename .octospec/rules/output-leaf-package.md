---
type: Rule
title: output is a leaf package
description: internal/output must not import any other internal/* package.
tags: ["output", "leaf", "imports", "architecture"]
timestamp: 2026-06-30T00:00:00Z
# --- octospec extension fields (OKF-permitted; consumers must preserve) ---
id: output-leaf-package
tier: repo
priority: 75
load_bearing: false
inject_when:
  paths: ["internal/output/**/*.go"]
  touches: ["output", "leaf", "imports", "architecture"]
source: self
supersedes: []
---

# output is a leaf package

`internal/output` 是叶子包：**不得 import 任何其它 `internal/*` 包**（`internal/output/doc.go`，grep 核实无跨 internal 依赖）。

> See CLAUDE.md § "Code Style"（the internal/output package is a leaf）。

## Rules

- 依赖单向流入：`ExitError` 是所有 CLI 路径收敛的结构化错误类型；`WriteSuccess`/`WriteError` 是唯一被认可的输出口。
- 保持叶子可避免循环依赖，并把信封契约固定为单一、可审计的边界。

## Why

架构不变量。一旦 output 反向依赖上层包，信封契约就不再是可独立审计的边界，且易引入 import cycle。
非安全 P0，故 load_bearing: false，但违反会破坏分层。
