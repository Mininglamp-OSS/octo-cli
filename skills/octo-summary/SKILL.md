---
name: octo-summary
disabled: true
description: Read, find, and cite existing Octo summaries through octo-cli. Use when an agent needs to discover summaries visible to its bot owner, inspect one summary, retrieve its current body, or ground an answer in returned citation metadata. Load after octo-shared.
---

# Octo Summary

Use this skill only for existing summaries. It does not create, edit, regenerate, publish, or delete summaries.

## Authentication and scope

Authenticate with the owner's `bf_*` personal Agent token through a stored profile or `OCTO_BOT_TOKEN`. Do not pass or rely on `--space`: the Summary service resolves both the human owner and Space from the bot token and applies the owner's existing Summary permissions.

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
