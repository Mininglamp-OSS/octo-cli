---
type: Rule
title: Credential at-rest security
description: Tokens persist only as AES-256-GCM machine-bound ciphertext; metadata stays plaintext; tight file perms; tokens never touch argv.
tags: ["security", "token", "secret", "encryption", "authstore", "trust-boundary"]
timestamp: 2026-06-30T00:00:00Z
# --- octospec extension fields (OKF-permitted; consumers must preserve) ---
id: credential-at-rest
tier: repo
priority: 92
load_bearing: true
inject_when:
  paths: ["internal/authstore/**/*.go", "cmd/auth.go", "cmd/auth_test.go"]
  touches: ["token", "secret", "encryption", "authstore", "file-permissions", "argv", "credential"]
source: self
supersedes: []
---

# Credential at-rest security

凭证落盘的安全不变量（`internal/authstore/`）。信任边界 = OS 用户账户：加密密钥机器绑定，
密文抵抗离机泄露（误提交/备份/云同步），但不防同机同用户进程。

> See CLAUDE.md § "Identity Model"（credential resolution & isolation boundary）。

## Rules

- token 只以 **AES-256-GCM** 写入 `credentials.enc`，密钥 `SHA256(machineID ‖ salt)`
  （`crypto.go: deriveKey/seal/open`）。
- 非密元数据（`ProfileMeta`）写明文 `config.json`；**token 绝不进明文**。
- 文件权限：目录 `0700`、密文/盐 `0600`、元数据 `0644`；`ensureDir` 会收紧已存在的过宽目录
  （防 restore-from-backup 的 0755 泄露 profile 名）。
- 所有写入走 `atomicWrite`（temp + rename），杜绝半写文件。
- `SaveProfile`/`RemoveProfile` 固定顺序（先 token 后元数据 / 先元数据后 token），
  使崩溃只留「孤儿 token（不可见）」而非「列出但无 token 的 profile」。
- 密钥/盐变化或盐长度异常**必须 fail loud**（提示删文件重 login），绝不静默重生成盐
  （会让所有 token 不可解）。
- `octo-cli auth login` 的 token 来源仅限：隐藏终端提示（`golang.org/x/term`）、`--with-token`（stdin）、
  `--token-file`——**永不经 argv**（防 shell history / 进程列表泄露，`cmd/auth.go`）。
- `status`/`list`/`--dry-run` 输出的 token 一律经 `credential.MaskToken` 掩码。

## Why load-bearing

这是 CLI 唯一的密钥静态存储面；任何权限放宽、明文落盘或 argv 泄露都是直接的凭证泄露。
