---
type: Rule
title: Disabled-flag parity & skills sync
description: "x-octo-disabled (spec) and the skill frontmatter disabled flag must flip together; command-shape changes must sync the matching SKILL.md."
tags: ["disabled", "withhold", "skill", "spec", "parity", "skills-sync"]
timestamp: 2026-06-30T00:00:00Z
# --- octospec extension fields (OKF-permitted; consumers must preserve) ---
id: disabled-flag-parity
tier: repo
priority: 62
load_bearing: false
inject_when:
  paths: ["internal/registry/specs/*.json", "skills/*/SKILL.md", "cmd/skills.go", "cmd/root.go", "internal/registry/loader.go", "cmd/service/**/*.go"]
  touches: ["disabled", "withhold", "skill", "spec", "parity", "skills-sync"]
source: self
supersedes: []
---

# Disabled-flag parity & skills sync

withhold 一个 domain（如 `matter`，后端 API 未稳）需**两处同步翻转**，否则两个面会漂移。

> See CLAUDE.md § "Command Structure"（matter withheld via x-octo-disabled + skill disabled frontmatter）。

## Rules

- spec 侧 `x-octo-disabled: true`（`internal/registry/specs/matter.json`）——服务仍 load
  （`schema` 与引擎仍可见），但从命令树与全局发现列表中移除
  （`registry.ServiceDisabled/EnabledServices`，`cmd/root.go: withholdDisabledServices`）。
- skill 侧 frontmatter `disabled: true`（`skills/octo-matter/SKILL.md`）——从 `octo-cli skills` 列表移除
  （`cmd/skills.go: skillDisabled`）。
- 两处共用同一 truthiness：JSON `true` 或字符串 `"true"`（`registry.truthy`），刻意一致以防漂移。

## Skills documentation sync（附注）

agent 面向用法在 `skills/`（`octo-shared`、`octo-messaging`、`octo-files`、`octo-matter`(withheld)）。
**命令形态（新增 / 改名 / 参数变化）变更时，必须同步对应 SKILL.md**（CLAUDE.md 末「keep those in sync」）——
否则 agent 会按过时形态调用。

## Why

仅改一处会导致命令隐藏但 skill 仍宣传（或反之），给 agent 矛盾信号。非安全级，故 load_bearing: false。
