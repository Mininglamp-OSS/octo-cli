# octo-marketplace — Expert workflows

Read `SKILL.md` first for authentication, payload normalization, and the shared
safety rule. This file covers the **专家市场**: two symmetric entities —
`expert` (专家, a single agent) and `squad` (专家团, an expert team). Every verb,
flag, and envelope below is identical between them; only the payload shape and
the id argument name (`<expert-id>` / `<squad-id>`) differ.

## Pagination is offset-based (not cursor)

Unlike the cursor-based `skill list` / `skill mine list`, the expert lists page
by number, so `--page-all` does **not** apply (the same offset scheme as
`mcp list`). Walk pages with `--page` (one-based) and `--page-size` (max 100):

```bash
octo-cli marketplace expert list --page 1 --page-size 20
```

The backend returns `{data:[...], pagination:{total,page,page_size}}`, and the
CLI output layer flattens that shape onto the envelope: list **items are at
`.data`** (already an array) and pagination metadata is at `._pagination`. Do
NOT apply the `.data.data // .data` normalization here — `.data` is the array,
so indexing it errors.

```bash
items=$(octo-cli marketplace expert list --keyword "架构" | jq -c '.data')
total=$(octo-cli marketplace expert list --keyword "架构" | jq '._pagination.total')
```

Single-record and non-paginated reads (`expert get`, `expert-category list`,
`expert-tag list`) return no `pagination` key, so they are NOT flattened —
apply the shared `.data.data // .data` rule from `SKILL.md` to those instead.

## Search

```bash
octo-cli marketplace expert-category list --kind agent      # kind: agent | squad
octo-cli marketplace expert-tag list --kind agent --q "<tag>"
octo-cli marketplace expert list \
  --keyword "<kw>" --category <category_id> --tag "<tag>" \
  --visibility public --created-by-type human --sort updated --page 1
octo-cli marketplace expert get <expert-id>
```

- `--category` / `--tag` / `--visibility` / `--created-by-type` are repeatable
  (repeat the flag to combine). `category` is a `category_id`; `all` disables
  the filter. Resolve ids and live tag names from `expert-category list` and
  `expert-tag list` for the current Space.
- `--sort updated` surfaces recently edited records first; anything else
  (including omitting the flag) sorts newest-first. There is no relevance mode.
- Use the immutable `expert_id` / `squad_id`, never a name.

The `squad` family is identical: `squad list`, `squad get <squad-id>`,
`squad mine list`, and `--kind squad` on the taxonomy commands.

## View owned records

```bash
octo-cli marketplace expert mine list --page 1     # own records, any visibility
octo-cli marketplace squad  mine list --page 1
```

## Create / update

Bodies are nested (an expert carries `instruction` / `mcp_config` / `skills`; a
squad carries `members[]`), so submit them as JSON via `--data`. Show the target
and the intended change, then continue only after explicit confirmation.

```bash
# Create a standalone expert (name/summary/category/instruction required;
# new records publish public)
octo-cli marketplace expert create --data '{
  "name": "后端架构师",
  "summary": "评审服务边界、数据模型和可靠性方案。",
  "category": "研发工具",
  "tags": ["架构评审", "可靠性"],
  "instruction": "你是资深后端架构师……",
  "mcp_config": "{\"mcpServers\":{}}",
  "skills": [{"name": "架构评审清单"}]
}'

# Partial update — owner only; send only mutable fields
octo-cli marketplace expert update <expert-id> --data '{"summary":"新的一句话简介"}'
```

- `category` is the category **NAME** (e.g. `研发工具`), resolved from
  `expert-category list`; an empty or unknown name is rejected.
- `mcp_config` is the raw `mcpServers` config as a JSON **string**; validated as
  well-formed JSON and size-capped, stored verbatim. Use the
  `__OCTO_SECRET_PLACEHOLDER__` sentinel instead of real tokens.
- A `visibility` field is ignored on write (records stay their current
  visibility; `system` is rejected).

For a **squad**, the body adds `leader` / `strategies` / `dependencies` /
`permission` / `members`. `category` and `members` (≥ 1) are required on
create; each member is `{member_key?, template_id?, name, role, is_leader?,
instruction, mcp_config?, skills?}` (`?` marks the optional fields). `leader`
is a display label (free text) — which member actually leads is chosen by
`is_leader` (else the first member). `dependencies` accepts only
`{blocking: [..], recommended: [..]}` string lists; any other key is
rejected. On update, sending `members` **replaces the whole array** — always
submit the complete member list.

## Delete (owner only, confirmed)

Soft delete; the name frees up for reuse:

```bash
octo-cli marketplace expert delete <expert-id>
octo-cli marketplace squad  delete <squad-id>
```

## Skills are whole Agent-Skill packages

A skill on an expert/squad is a `.zip`/`.skill` package containing `SKILL.md`.
Publishing one is a three-step client-side flow — never send raw bytes through
`--data`:

1. **Presign** an upload (`.zip`/`.skill`, ≤ 20 MiB):

   ```bash
   octo-cli marketplace expert-skill-upload create \
     --file-name "架构评审清单.zip" --file-size <bytes>
   ```

   Returns `{upload_object_key, presigned_url, method, headers, expires_in}`.
2. **PUT** the raw package bytes to `presigned_url` using the returned HTTP
   `method` and `headers`. Never print the presigned URL or its headers.
3. **Reference** the key in a create/update `skills[]` entry — the server
   extracts `SKILL.md`, derives the authoritative skill `name`, stores the
   package, and records the file manifest:

   ```json
   { "skills": [ { "name": "架构评审清单",
                   "upload_object_key": "expert-uploads/…",
                   "file_name": "架构评审清单.zip", "file_size": 12345 } ] }
   ```

   A name-only skill is `{ "name": "…" }`; inline `SKILL.md` text is
   `{ "name": "…", "content": "…" }`. Sending only a name on update preserves
   the existing stored package.

### Read / download a stored skill package

On read, each `skills[]` item carries `has_content` / `can_download` /
`file_name` / `file_size` / `files`. Fetch by index `i`:

```bash
octo-cli marketplace expert skillmd get <expert-id> --index <i>        # SKILL.md text
octo-cli marketplace expert skill-download <expert-id> --index <i>     # {download_url}
```

For a squad member, add `--member <member_key>`:

```bash
octo-cli marketplace squad skillmd get <squad-id> --member <member_key> --index <i>
octo-cli marketplace squad skill-download <squad-id> --member <member_key> --index <i>
```

`skill-download` returns a short-lived presigned GET `download_url`; download
into a fresh temp dir and never infer or log the URL. Apply the same archive
safety checks as Skill install (`skills.md`): reject absolute paths, `..`
traversal, links, and devices before extraction.
