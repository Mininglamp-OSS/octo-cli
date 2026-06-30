---
type: Rule
title: Transport client — auth, retry, redirects
description: All outbound HTTP goes through client.Client — centralized auth headers, retry with backoff, and no auto-following redirects.
tags: ["http", "retry", "redirect", "auth-header", "security", "transport"]
timestamp: 2026-06-30T00:00:00Z
# --- octospec extension fields (OKF-permitted; consumers must preserve) ---
id: transport-client
tier: repo
priority: 78
load_bearing: true
inject_when:
  paths: ["internal/client/**/*.go", "cmd/service/run.go"]
  touches: ["http", "retry", "redirect", "auth-header", "backoff", "timeout"]
source: self
supersedes: []
---

# Transport client — auth, retry, redirects

所有出站 HTTP 经 `client.Client.Do`，统一注入鉴权、重试、dry-run。

> See CLAUDE.md § "Architecture"（multi-backend / unified API base URL）。

## Rules

- 鉴权头集中注入：`Authorization: Bearer <token>` 与 `X-Space-Id`（`attempt`）——**不要在命令层自己拼鉴权头**。
- 重试：仅对 429/502/503/504（`isRetryableStatus`）做指数退避 + full jitter
  （`backoffDelay`，jitter ∈ [0.5,1.0) 用 `crypto/rand`），尊重 `Retry-After`，`--no-retry` 关闭；默认 3 次（`defaultMaxRetries`）。
- **不自动跟随 3xx**（`CheckRedirect: ErrUseLastResponse`）：`file.download` 返回 302 到 presigned URL，
  需把该 URL 放进信封而非 fetch；其它端点的 3xx 视为错误。
- `BinaryResponse`（`x-octo-binary-response`）时 2xx/3xx 不按 JSON 解析，返回描述性元数据信封。
- 非 JSON payload（multipart 上传）经 `RawBody`+`ContentType`，文件用 `io.Copy` 流式
  （`cmd/service/multipart.go`），内存与文件大小无关。
- `--dry-run` 渲染请求描述（token 经 `MaskToken` 脱敏）且不实际发请求。

## Why load-bearing

鉴权头与重定向处理集中在此；命令层各自拼鉴权或误跟随 302 会泄露 token 到 presigned URL 主机或破坏下载语义。
