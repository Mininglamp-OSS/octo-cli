---
name: octo-summary
description: Read, create, find, and cite Octo summaries through octo-cli. Use when an agent needs to create an owner-only summary from explicit channels, discover summaries visible to its bot owner, inspect one summary, retrieve its current body, or ground an answer in returned citation metadata. Load after octo-shared.
disabled: true
---

# Octo Summary

This skill can read summaries and create an owner-only asynchronous summary. It does not edit, regenerate, publish, or delete summaries.

## Authentication and scope

Authenticate with the owner's `bf_*` personal Agent token through a stored profile or `OCTO_BOT_TOKEN`. Do not pass or rely on `--space`: the Summary service resolves both the human owner and Space from the bot token and applies the owner's existing Summary permissions.

## Create a summary

Creation is a write operation. Confirm the requested channels and time range with the user before calling it. Only explicit sources are accepted; the service does not let the model search all owner channels implicitly.

Pass `time_range` and `sources` through `--data`, and always provide a stable idempotency key:

```bash
octo-cli summary create \
  --idempotency-key project-weekly-2026w31 \
  --data '{"title":"本周项目进展","topic":"整理决策、进展和风险","time_range":{"start":"2026-07-21T00:00:00+08:00","end":"2026-07-28T00:00:00+08:00"},"sources":[{"source_type":1,"source_id":"group-id"}]}'
```

Reuse the same key only when retrying the exact same request body after an uncertain outcome. If any body field changes, generate a new key; reusing the old key with a changed body returns `409/40009`. Never generate a fresh key merely because an otherwise unchanged first request timed out.

Source types are `1=group`, `2=thread`, and `3=DM`. `include_archived` defaults to false. An optional origin channel must also be present in `sources`. Owner, bot identity, Space, source names, and participants are server-controlled; do not send `uid`, `participants`, `source_name`, or `confirm_timeout_hours`.

The result contains `task_id` with an initial pending status. Poll with `summary get <task-id>` until status is completed (`3`), failed (`4`), or cancelled (`5`), then use `summary result <task-id>`. Poll at most 60 times with a 5-second interval; if it is still pending after 5 minutes, report the task id and current status instead of continuing.

Treat `40301/40302` as a source authorization or Space mismatch. Treat `50301` as the create feature being disabled. Treat `409/40009` as key reuse with a changed body: preserve the existing task id for audit and use a new key only if the user still wants the modified request. Do not retry authorization or feature-disabled errors with altered identity or broader sources.

## Discover summaries

```bash
octo-cli summary list
octo-cli summary list --keyword "launch review" --page 1 --page-size 20
octo-cli summary list --status 3 --created-after 2026-07-01T00:00:00Z
```

`--keyword` matches the summary title or topic. Results are page-based `{total, items}`; increment `--page` manually because `--page-all` is not available.

Choose a candidate using its title, topic, sources, participants, time range, and `task_id`. Do not assume a title match alone proves relevance.

## Read and cite

```bash
octo-cli summary get <task-id>
octo-cli summary result <task-id>
```

Use `get` for metadata plus the current visible result. Use `result` when the full current body and citations are needed.

When answering from a summary:

1. Identify the summary by title and `task_id`.
2. Attribute claims to the returned citation entry using its index, sender, sent time, source, and message sequence.
3. Preserve the summary's citation marker such as `[1]` or `[P1]` when useful.
4. Never invent missing surrounding conversation. Bot responses intentionally omit `context_before` and `context_after`.

Treat `401` as an invalid/ineligible bot identity. Treat `403` or `404` as unavailable or not visible; do not infer the contents of a hidden summary.

Inspect exact flags and schemas when needed:

```bash
octo-cli schema summary.list
octo-cli schema summary.get
octo-cli schema summary.result
```
