---
type: Rule
title: Commit & PR style
description: English Conventional Commits enforced by lefthook; read CONTRIBUTING.md before a PR; never bypass hooks with --no-verify.
tags: ["commit", "git", "pr"]
timestamp: 2026-06-30T00:00:00Z
# --- octospec extension fields (OKF-permitted; consumers must preserve) ---
id: commit-style
tier: repo
priority: 55
load_bearing: false
inject_when:
  paths: ["**"]
  touches: ["commit", "git", "pr"]
source: octo-spec@1.1.0
supersedes: []
---

# Commit & PR style

继承全局 `commit` / `pr` 规则。仓库具体约定如下。

> See CONTRIBUTING.md（Git Hooks）& CLAUDE.md § "Build & Test"。

## Rules

- 英文 **Conventional Commits**（`feat/fix/test/refactor/chore/docs`），由 lefthook `commit-msg` 钩子
  `.lefthook/commit-msg-check.sh` 校验。
- pre-commit：gofmt + go vet + golangci-lint(可选)；pre-push：mod-tidy 漂移检查 + build + vet（`lefthook.yml`）。
- 开 PR 前读 `CONTRIBUTING.md`。
- **禁用 `--no-verify` 绕过钩子。**
