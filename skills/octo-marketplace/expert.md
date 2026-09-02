# octo-marketplace — Expert and Squad workflows

Read `SKILL.md` first for authentication, payload normalization, versioning, the
no-separate-publish rule, and the shared safety rule. This file covers the
**专家市场**: `expert` (专家, a single agent, `plugin_type=expert`) and `squad`
(专家团, an expert team, `plugin_type=expert_team`). Both are plugins; only the
type and package shape differ.

An expert's instruction lives in the package's root `AGENTS.md`; an
expert_team is a single `AGENTS.md` describing collaboration/dispatch, and its
members and their skills/connectors are expressed as **relations** (fetch with
`--include-relations`).

## Search

```bash
octo-cli marketplace plugin-category list --scene-code default --plugin-type expert
octo-cli marketplace plugin-tag list --plugin-type expert --q "<tag>"
octo-cli marketplace plugin list --scene-code default --plugin-type expert \
  --q "<kw>" --category-id <id> --tag "<tag>" --sort updated --page 1 --page-size 20
octo-cli marketplace plugin get --plugin-id <plugin-id> --include-relations
```

Squads are identical with `--plugin-type expert_team`. `plugin list` is
page-paginated (CLI `.data` + `._pagination`); add `--mode mine` for owned
records of any visibility. Use the immutable `plugin_id`, never a name.

## Read member skills

`plugin get --include-relations` returns the relations. For an expert_team, each
member is an `expert_team_expert` relation; a member/expert's skills are
`expert_skill` relations whose target is a skill plugin. Read a skill's document
by its own plugin id:

```bash
octo-cli marketplace plugin skillmd --plugin-id <skill-plugin-id>
octo-cli marketplace plugin download --plugin-id <skill-plugin-id> -o <tmp>/skill.zip
```

## Create / update

Create or edit through the unified write path. Every save is a version snapshot
and attaches/self-heals the default market placement, so a created expert or
team is listable immediately — there is no separate publish step. Show the
target and intended change, then continue only after explicit confirmation:

```bash
octo-cli marketplace plugin upsert --data @plugin.json
```

The `--data` body is `{"plugin":{plugin_name, plugin_type, visibility,
category_id, tags, icon, version?, manifest_json, plugin_json},
"relations":[...]}`. Both `manifest_json` and `plugin_json` are required on
every write:

- **expert**: `plugin_json` carries a root `AGENTS.md` (the instruction) and,
  when it uses connectors, a root `mcp.json` with `${VAR}` placeholders for
  consumer secrets.
- **expert_team**: `plugin_json` is a single `AGENTS.md` rendering the
  collaboration/dispatch, leader, strategies, dependencies, and permissions.
  Members and their skills are `relations` entries; valid `relation_type`
  values are `expert_team_expert` (team → member expert), `expert_skill`
  (expert → skill), and `expert_connector` (expert → connector). The team
  leader is the member relation whose `data.is_leader` is true; every
  relation requires `target_plugin_id` and `relation_type`.

Set `plugin.plugin_id` to update; omit to create. Inspect history with `plugin version list --plugin-id <id>` after a release.

> **`manifest_json` must agree with the outer `plugin` fields.** The backend
> rejects the write unless all three hold:
> `manifest_json.plugin_name == plugin.plugin_name`,
> `manifest_json.plugin_type == plugin.plugin_type`, and
> `manifest_json.labels == plugin.tags` **in the same order** (they are compared
> as canonical JSON arrays, not as sets, so reordering, a duplicate or an
> untrimmed label fails). Every violation returns the same opaque
> `400 VALIDATION_ERROR` with `details {"field":"body","reason":"invalid"}` and
> no hint as to which field disagreed — check all three before retrying.
>
> ```jsonc
> {
>   "plugin": {
>     "plugin_name": "deep-miner",
>     "plugin_type": "expert",
>     "visibility": "space",
>     "tags": ["research", "analysis"],
>     "manifest_json": {
>       "plugin_name": "deep-miner",        // == plugin.plugin_name
>       "plugin_type": "expert",            // == plugin.plugin_type
>       "labels": ["research", "analysis"]  // == plugin.tags, same order
>     },
>     "plugin_json": { "attachments": [] }
>   },
>   "relations": []
> }
> ```

> **Upsert replaces the row; it does not patch it.** The backend rebuilds the
> whole plugin from your document, preserving only `created_at`, the version
> history and the creator identity. An omitted `category_id`, `publisher` or
> `icon` is accepted as empty and **clears the stored value**. Read-modify-write
> with `plugin get` and resubmit the complete document on every edit. (`plugin
> import` is the opposite — it falls back to the existing row — so do not carry
> habits between the two paths.)

> **Every upsert replaces the relation set.** Always resubmit the complete list
> — including on metadata-only edits. Omitting the `relations` key is identical
> to submitting `[]` and soft-deletes every live relation (team members,
> attached skills, attached connectors) on save. Nothing validates this locally,
> so the destruction is silent. Echo `relation_id` for rows you want to keep
> verbatim.

Bumping `plugin.version` (and adding a changelog via the import flow for
skill-bearing experts) is how a new release is recorded; there is no separate
publish command.

## Install into a Loop workspace

Provision an expert or expert_team into a Loop workspace/runtime (creates the
agent or squad, acting as the caller against octo-fleet, with rollback):

```bash
octo-cli marketplace plugin install --plugin-id <id> --workspace-id <ws> --runtime-id <rt>
```

The response carries exactly one of `agent_id` (expert) or `squad_id`
(expert_team), depending on the plugin's type.

## Delete (owner only, confirmed)

Soft delete; the name frees for reuse:

```bash
octo-cli marketplace plugin delete --plugin-id <id>
```

Skill packages attached to an expert/team are themselves `skill` plugins — to
release a new version of one, re-run the upload → parse → import flow in
[`skills.md`](skills.md) with the existing `--plugin-id`; wire or re-wire it
with an `expert_skill` relation in a subsequent `plugin upsert` that resubmits
the full relation list.
