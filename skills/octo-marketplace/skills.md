# octo-marketplace — Skill workflows

Read `SKILL.md` first for authentication, payload normalization, and confirmation
rules.

## Search

```bash
octo-cli marketplace skill-category list
octo-cli marketplace skill tag list --q "<tag>"
octo-cli marketplace skill list --q "<keywords>" --sort latest --page-size 20
octo-cli marketplace skill get <skill-id>
```

Search uses cursor pagination: read results from CLI `.data[]` and use
`--page-all` when all matches are required. Use the immutable `skill_id`, not a
name. Sort may be `latest`, `comprehensive`, `downloads`, or `views`.
The category command is non-paginated; normalize it and use each returned
`skill_category_id` for create, update, or search filters.

Skill tags are strings normally parsed from `SKILL.md` frontmatter. Use
`skill tag list` for current-Space and global suggestions; new valid tag values
may still be submitted when publishing or updating.

## Install

Before downloading or modifying the runtime, show the Skill name and version,
destination Skills root, and whether an existing installation will be replaced.

After confirmation:

1. Run `marketplace skill get <skill-id>` and verify name, version, file name,
   and file size.
2. Run `marketplace skill download <skill-id> --response-format json`.
   `--response-format json` is mandatory for this workflow; without it the
   backend returns an artifact redirect that octo-cli intentionally does not follow.
3. Normalize the result, then read `download_url` and `file_sha256`. Never infer
   an artifact URL from metadata or log the short-lived URL.
4. Download into a new temporary directory and require an exact SHA-256 match.
5. Reject absolute paths, `..` traversal, links, devices, and entries escaping
   the extraction directory. The extracted root must contain `SKILL.md`; one
   wrapping ZIP directory is allowed.
6. Atomically move the verified directory to `<skills-root>/<skill-name>`.
7. After successful activation, remove staging and backup directories. On
   failure, restore the backup before returning the error.
8. Read the installed `SKILL.md` and follow the runtime's normal reload flow.

Never execute archive scripts during installation.

## Publish as a Bot

The user must first provide a `.zip` / `.skill` package, or an accessible Skill
directory. Do not search the machine, guess a path, explain Skill loading,
repeat the prompt, or narrate checks step by step.

1. For a directory, copy it into a fresh `mktemp -d` staging directory and
   package the copy. Never modify the source directory or use a fixed temporary
   path. Exclude `.git`, `.github`, caches, dependencies, and build output; keep
   `SKILL.md`, referenced files, README, and LICENSE. If `version` is absent,
   use `1.0.0` and write it only to the staged `SKILL.md`.
2. Inspect the package without executing it. Read the root `SKILL.md` and obtain
   its `name`, `version`, optional `id`, byte size, and SHA-256. Reject an unsafe
   archive using the same path, link, and size checks required for installation.
3. Run `marketplace skill mine list --q <name> --page-all`, then compare exact
   names. This owned-Space lookup is authoritative for the backend uniqueness
   scope; do not use the public search result for duplicate detection.
4. Choose exactly one flow:
   - No exact owned name: continue with the create flow below. An existing
     package `id` represents its source and the server will publish a new Skill
     with a new ID and `forked_from` metadata.
   - Exact owned name and package `id` equals that Skill's `skill_id`: stop the
     create flow and follow **Update ZIP version** below.
   - Exact owned name but the package has no `id`, or its `id` differs: report a
     name conflict and ask the user to rename the package or stop. Never guess
     that this is an update and never overwrite the existing Skill.
5. Run `marketplace skill-category list`, resolve the intended category, then
   show one final plan containing the package path, name, version, size,
   SHA-256, create/update decision, visibility, and category. Ask for one final
   confirmation; do not initialize or upload before it.
6. Initialize with `marketplace skill-upload create --file-name <file>
   --file-size <bytes>`, then upload to its `presigned_url` using the returned
   HTTP `method` and `headers`.
7. Publish once with `marketplace skill publish --skill-upload-id <id>
   --visibility <visibility>` plus reviewed optional metadata. This endpoint
   synchronously parses and creates the Skill; do not call `skill-upload parse`,
   poll `skill-parse-task`, or call `skill create` in the normal Bot flow.
8. Read the returned `skill_id`, then run `marketplace skill get <skill-id>` to
   verify the name, version, creator, visibility, and current version.

Optional metadata may override parsed values, including `category_id` and a
JSON string array `tags`, but do not silently replace a package version. Only
default a missing version to `1.0.0`.

`skill-upload parse`, `skill-parse-task get`, and `skill create` remain exposed
for Web/legacy compatibility and diagnosis. They are not an alternative Agent
workflow and must not be used unless the Bot publish endpoint is unavailable.
If parsing returns `RATE_LIMITED`, wait and retry within the user's timeout. If
Bot publish returns a gateway timeout, query `skill mine list` before retrying
so an already-created Skill is never duplicated.

## Update metadata

Metadata-only changes do not create a release:

```bash
octo-cli marketplace skill update <skill-id> --data \
  '{"display_name":"New title","description":"New description","category_id":"<id>","tags":["automation","cli"]}'
```

Resolve `category_id` through `skill-category list`. Tags remain free-form
strings and need no dictionary lookup.

## Update ZIP version

Only enter this flow after exact-name lookup identifies an owned Skill and the
package `id` equals its `skill_id`. A missing or different package ID is not
enough evidence to update an existing record.

1. Require the parsed package version to differ from the current version.
2. Initialize with `marketplace skill reupload create <skill-id>` plus the new
   ZIP's `file_name` and `file_size`.
3. Upload to the returned presigned target.
4. Trigger and poll parsing with `skill-upload parse` and
   `skill-parse-task get`.
5. Show the new version, changelog, file size, and SHA-256 and request
   confirmation.
6. Apply the release with `marketplace skill update <skill-id>` using
   `parse_task_id`, `version`, and `changelog`.
7. Run `marketplace skill version list <skill-id>`, normalize the response, and
   read releases from `payload.items`.

If create returns `DUPLICATE_NAME` after exact owned-name lookup found no live
record, report that a soft-deleted Skill may still reserve the name. Do not
retry, auto-rename, or switch to update without user direction.

## Manage owned Skills

Use `marketplace skill mine list` to resolve owned records before update or
delete. Deletion is a confirmed destructive action:

```bash
octo-cli marketplace skill delete <skill-id>
```

Use `marketplace skill skillmd get <skill-id>` when the current raw `SKILL.md`
is needed without downloading the full package. Skill icon upload is a
presigned flow initialized by `marketplace skill-icon-upload create`.
