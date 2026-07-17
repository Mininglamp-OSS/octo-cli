# octo-marketplace — Skill workflows

Read `SKILL.md` first for authentication, payload normalization, and confirmation
rules.

## Search

```bash
octo-cli marketplace skill-category list
octo-cli marketplace skill list --q "<keywords>" --page-size 20
octo-cli marketplace skill get <skill-id>
```

Search is paginated: read results from CLI `.data[]`. Use the immutable
`skill_id`, not a name. Follow `--page-all` only when all results are needed.
The category command is non-paginated; normalize it and use each returned
`skill_category_id` for create, update, or search filters.

Skill tags are free-form strings, normally parsed from `SKILL.md` frontmatter.
There is no separate tag dictionary endpoint.

## Install

Before downloading or modifying the runtime, show the Skill name and version,
destination Skills root, and whether an existing installation will be replaced.

After confirmation:

1. Run `marketplace skill get <skill-id>` and verify name, version, file name,
   and file size.
2. Run `marketplace skill download <skill-id> --response-format json`.
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

## Publish

The ZIP's `SKILL.md` supplies the Skill version. Do not call `skill create`
before parsing succeeds.

1. Inspect the ZIP and obtain its byte size.
2. Initialize with `marketplace skill-upload create --file-name skill.zip
   --file-size <bytes>`.
3. Normalize the response, then upload the ZIP to `presigned_url` with the
   returned HTTP `method` and `headers`.
4. Run `marketplace skill-upload parse <skill-upload-id>`.
5. Poll `marketplace skill-parse-task get <skill-parse-task-id>`. Continue only
   when normalized `payload.status` is `success`. Parsed ZIP metadata is in
   `payload.result`. On `failed`, return `payload.error` and stop.
6. Show parsed name, version, description, tags, file size, SHA-256, intended
   visibility, and selected category. Obtain `category_id` from
   `marketplace skill-category list`; do not send the display name as the ID.
   Request confirmation.
7. Publish with `marketplace skill create`, passing `parse_task_id` and
   `visibility`.

Optional metadata may override parsed values, including `category_id` and a
JSON string array `tags`, but do not silently replace the ZIP version.

## Update metadata

Metadata-only changes do not create a release:

```bash
octo-cli marketplace skill update <skill-id> --data \
  '{"display_name":"New title","description":"New description","category_id":"<id>","tags":["automation","cli"]}'
```

Resolve `category_id` through `skill-category list`. Tags remain free-form
strings and need no dictionary lookup.

## Update ZIP version

1. Initialize with `marketplace skill reupload create <skill-id>` plus the new
   ZIP's `file_name` and `file_size`.
2. Upload to the returned presigned target.
3. Trigger and poll parsing with `skill-upload parse` and
   `skill-parse-task get`.
4. Require the parsed version to differ from the current version. Show the new
   version and changelog and request confirmation.
5. Apply the release with `marketplace skill update <skill-id>` using
   `parse_task_id`, `version`, and `changelog`.
6. Run `marketplace skill version list <skill-id>`, normalize the response, and
   read releases from `payload.items`.
