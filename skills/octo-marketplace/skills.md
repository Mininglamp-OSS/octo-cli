# octo-marketplace — Skill workflows

Read `SKILL.md` first for authentication, payload normalization, the
save→publish/review lifecycle, and the shared safety rule. Skills are
`plugin_type=skill`. A skill package is a **flat attachment tree**:
`plugin_json.attachments` is the file list, one attachment per file (text inline
as `raw`, binary as `storage`), always including a root `SKILL.md`.

## Search

```bash
octo-cli marketplace plugin-category list --scene-code default --plugin-type skill
octo-cli marketplace plugin-tag list --plugin-type skill --q "<tag>"
octo-cli marketplace plugin list --scene-code default --plugin-type skill \
  --q "<keywords>" --sort newest --page 1 --page-size 20
octo-cli marketplace plugin get --plugin-id <plugin-id>
```

`plugin list` is page-paginated (`{data:[...],pagination}` → CLI `.data` +
`._pagination`). Add `--mode mine` to list only owned skills. Sort may be
`newest`/`oldest`/`updated`/`name`/`placement`/`downloads`/`views`/
`installs`/`comprehensive`. A plugin has at most 100 tags; each tag is non-empty
UTF-8 and at most 128 bytes.

## Read content without downloading

```bash
octo-cli marketplace plugin skillmd --plugin-id <plugin-id>   # SKILL.md text
octo-cli marketplace plugin get --plugin-id <plugin-id>       # full attachment tree
```

`plugin get` returns `plugin_json.attachments`; each entry has `path`,
`content_type` (`raw`/`storage`), and for text files an inline `raw_content`.

## Install

Before touching the runtime, show the skill name/version, the destination Skills
root, and whether an existing install is replaced. After confirmation:

1. `plugin get --plugin-id <id>` — verify name, version, and the attachment list.
2. `plugin download --plugin-id <id> -o <tmp>/skill.zip` — the backend streams a
   zip reconstructed from the attachment tree (authenticated; no presigned URL).
3. Extract into a fresh temp dir. Reject absolute paths, `..` traversal, links,
   devices, and entries escaping the dir. The root must contain `SKILL.md`; one
   wrapping directory is allowed.
4. Atomically move the verified dir to `<skills-root>/<skill-name>`; remove
   staging on success, restore the backup on failure.
5. Read the installed `SKILL.md` and follow the runtime's reload flow.

Never execute archive scripts during installation.

## Publish as a Bot (upload → parse → import)

The user must provide a `.zip`/`.skill` package or an accessible skill directory.
Do not search the machine or guess a path.

1. For a directory, copy into a fresh `mktemp -d` and package the copy (exclude
   `.git`, caches, build output; keep `SKILL.md`, referenced files, README,
   LICENSE). Default a missing `version` to `1.0.0` in the staged `SKILL.md`.
2. Inspect without executing; read the stable machine `name` and `version` from
   the root `SKILL.md`. On create, choose a concise, human-facing Marketplace
   display name in the user's language; do not silently reuse the machine slug
   unless the user explicitly wants that as the visible title.
3. Check ownership exhaustively: `plugin list --scene-code default --plugin-type
   skill --mode mine --page 1 --page-size 100`, then walk `--page` until a short
   page. Re-pass `--page-size 100` on every request: if it is omitted, the
   default silently falls back to 20. There is no `--page-all`, and a full page
   is never a stop condition; stopping early reports a false "no match" and
   creates a duplicate card. Compare each row's `manifest_json.name` to the
   exact machine name from `SKILL.md`. The display-name search filter cannot
   reliably find an existing skill by machine name, so scan the complete owned
   list. Decide create vs. update. For an update, retain the matched row's
   `plugin_id` and current `plugin_name`; omit `--plugin-name` to preserve that
   title unless the user explicitly requested a rename.
4. Show the final plan (path, display name, machine name, version, visibility,
   category) and get one confirmation.
5. Presign + upload + parse + import:

   ```bash
   octo-cli marketplace skill-upload create --file-name "<file>.zip" --file-size <bytes>
   # PUT the bytes to the returned presigned_url with its method/headers
   octo-cli marketplace skill-upload parse <skill_upload_id>
   octo-cli marketplace skill-parse-task get <parse_task_id>   # poll until success
   # create: pass `--plugin-name` and omit `--plugin-id`
   octo-cli marketplace plugin import --parse-task-id <parse_task_id> \
     --plugin-name "<display-name>" --name "<skill-name>" \
     --visibility space --version 1.0.0
   # update: pass `--plugin-id` and omit `--plugin-name` to preserve the title
   octo-cli marketplace plugin import --plugin-id <plugin-id> \
     --parse-task-id <parse_task_id> --name "<skill-name>" \
     --visibility space --version <next-version>
   ```

   `--plugin-name` is the visible Marketplace title; `--name` is the stable
   machine name from `SKILL.md`. `plugin import` builds the attachment tree and
   saves a draft version. On update, add `--plugin-name "<new-display-name>"`
   only when the user explicitly confirmed a rename. Optional `--category-id`,
   `--tags`, `--icon`, `--changelog`.
6. Read the returned `plugin_id`, then `plugin get --plugin-id <plugin-id>` to
   verify the draft.
7. Publish explicitly:

   ```bash
   octo-cli marketplace plugin publish --plugin-id <plugin-id> \
     --version 1.0.0 --changelog "Initial release"
   ```

   A Space-visible skill returns `pending_review` plus a `review_id`; it becomes
   catalog-visible only after a Space owner/admin approves that request.

If parse returns `RATE_LIMITED`, wait and retry within the user's timeout. Parse
itself is not idempotent: re-triggering an already-parsed upload returns `409
CONFLICT` rather than the original task, so poll `skill-parse-task get` instead
of re-posting. If a create import returns a gateway timeout or RESULT_UNKNOWN,
re-check `plugin list --scene-code default --plugin-type skill --mode mine
--page 1 --page-size 100`, then re-pass `--page-size 100` on every request while
walking every page; the default silently falls back to 20 when omitted. Never
stop after a full page. Compare `manifest_json.name` exactly before retrying so
the skill is never duplicated.
If an existing-id import is ambiguous, use `plugin get`, version history,
hashes, and content comparison instead—the row existed before the request, so
finding it does not prove the update committed.

## Release a new version

First inspect `display_status` with `plugin get`:

- For an unpublished draft, re-run upload/parse and update it with `plugin
  import --plugin-id <plugin-id> --parse-task-id <parse-task-id> ...`, omitting
  `--plugin-name` to retain the current title unless the user explicitly
  requested a rename, then run `plugin publish` as in the initial flow.
- For an already-published Space skill, **do not call import or upsert**: direct
  edits are rejected so live content cannot bypass review. Upload and parse the
  new archive, then submit that fresh parse task as the frozen upgrade:

```bash
cat >review.json <<'JSON'
{
  "plugin_id": "<plugin-id>",
  "version": "<new-version>",
  "changelog": "What changed",
  "parse_task_id": "<parse-task-id>"
}
JSON
octo-cli marketplace plugin review-request create --data @review.json
```

Keep `<plugin-id>` and `<parse-task-id>` distinct. The published version remains
live until a Space owner/admin approves the request. Version labels must be
forward-moving `MAJOR.MINOR.PATCH` values; if review is rejected, correct the
content and submit another review request.

```bash
octo-cli marketplace plugin version list --plugin-id <id>
```

## Update metadata / manage owned skills

Metadata edits on a draft, delisted plugin, or published-private plugin go
through `plugin upsert` with the existing `plugin.plugin_id`. A published Space
plugin rejects direct metadata edits. Because review snapshots content rather
than arbitrary market metadata, a Space owner/admin must delist it first; then
the owner may edit and publish it for review again.
`plugin import` is **not** a metadata-only path: its `--parse-task-id` is
required, so reusing it always means a fresh upload+parse cycle to ship new
package content.

> **`plugin upsert` replaces the row; it does not patch it.** There is no
> metadata-only PATCH on this API. The backend rebuilds the whole plugin from
> your document, keeping only `created_at`, the version history and the creator
> identity. An omitted `category_id`, `publisher` or `icon` is accepted as empty
> and **clears the stored value** — editing one field by sending only that field
> silently wipes the rest. Always read-modify-write: `plugin get --plugin-id
> <id>` first, rebuild the full write document from what it returns (the read
> shape is flat; the write shape is `{"plugin":{...},"relations":[...]}`), change
> the one field, and send the whole document via `--data @plugin.json`.
> `manifest_json` and `plugin_json` are required on every write, and
> `manifest_json` must agree with the outer fields (see the invariant in
> `expert.md`). For a Skill, preserve the existing `manifest_json.name` exactly:
> changing it replaces the machine identity used by the next import's ownership
> check and can cause a duplicate card. Note `plugin import` behaves the
> *opposite* way — omitted fields there fall back to the existing row — so do
> not carry habits between the two.

Deletion is destructive and confirmed:

```bash
octo-cli marketplace plugin delete --plugin-id <id>
```

> **Relations and expert/team edits:** every upsert replaces the relation set.
> When updating an `expert` or `expert_team` that carries relations, always
> resubmit the complete list — omitting the `relations` key soft-deletes all
> relations on save.

Skill icon upload is a presigned flow: `marketplace skill-icon-upload create`.
