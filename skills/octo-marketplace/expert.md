# octo-marketplace — Expert and Squad workflows

Read `SKILL.md` first for authentication, payload normalization, and the shared
safety rule. This file covers the **专家市场**: `expert` (专家, a single agent,
`plugin_type=expert`) and `squad` (专家团, an expert team,
`plugin_type=expert_team`). Both are plugins; only the type and package shape
differ.

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

Create or edit through the unified write path. Show the target and intended
change, then continue only after explicit confirmation:

```bash
octo-cli marketplace plugin upsert --data @plugin.json
```

The `--data` body is `{"plugin":{plugin_name, plugin_type, visibility,
category_id, tags, manifest_json, plugin_json}, "relations":[...]}`:

- **expert**: `plugin_json` carries a root `AGENTS.md` (the instruction) and,
  when it uses connectors, a root `mcp.json` with `${VAR}` placeholders for
  consumer secrets.
- **expert_team**: `plugin_json` is a single `AGENTS.md` rendering the
  collaboration/dispatch, leader, strategies, dependencies, and permissions.
  Members and their skills are `relations` entries
  (`expert_team_expert` / `expert_skill` / `expert_connector`); the leader is
  the member relation with `is_leader`.

Set `plugin.plugin_id` to update; sending `relations` replaces the relation set,
so submit the complete list. Publish an immutable version separately:

```bash
octo-cli marketplace plugin publish --plugin-id <id> --version 1.1.0 --changelog "…"
```

## Install into a Loop workspace

Provision an expert or expert_team into a Loop workspace/runtime (creates the
agent or squad, acting as the caller against octo-fleet, with rollback):

```bash
octo-cli marketplace plugin install --plugin-id <id> --workspace-id <ws> --runtime-id <rt>
```

The response carries `agent_id` (expert) or `squad_id` (expert_team).

## Delete (owner only, confirmed)

Soft delete; the name frees for reuse:

```bash
octo-cli marketplace plugin delete --plugin-id <id>
```

Skill packages attached to an expert/team are themselves `skill` plugins — to
publish or update one, follow the upload → parse → import flow in `skills.md`,
then wire it with an `expert_skill` relation in `plugin upsert`.
