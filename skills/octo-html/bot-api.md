# Bot API operations

Operations a Bot token can call directly. Use `octo-cli schema <operation-id>` (or MCP `get_skill` with intent=describe) for the exact parameters; this page only says what each one is for and what it needs.

## Versions, comments and reactions

A Bot bearer works on /docs-html only when the deployment sets BOT_AUTH_ENABLED=true. Revert republishes an old version as the new latest; comment rm with all=1 wipes every comment.

| Operation | Command | Risk | Purpose |
| --- | --- | --- | --- |
| `html.version.source` | `octo-cli html version source` | read | Download one version's stored HTML source as plain text (needs reader) |
| `html.version.revert` | `octo-cli html version revert` | write | Republish an earlier version as a new latest version (needs edit) |
| `html.diff` | `octo-cli html diff` | read | Compare two published versions of a document (needs reader) |
| `html.comment.reanchor` | `octo-cli html comment reanchor` | write | Move a comment's anchor (own comment, or manage) |
| `html.comment.rm` | `octo-cli html comment rm` | high-risk-write | Delete one comment, or wipe every comment with all=1 |
| `html.comment.react` | `octo-cli html comment react` | write | Toggle an emoji reaction on a comment or reply (needs commenter) |

## Rendered pages and assets

Rendered pages and assets are returned inline over MCP up to 4 MiB. HEAD variants report status and size only.

| Operation | Command | Risk | Purpose |
| --- | --- | --- | --- |
| `html.page.draft` | `octo-cli html page draft` | read | Render the draft as an HTML page (needs edit) |
| `html.page.draft_head` | `octo-cli html page draft-head` | read | Check the draft page without downloading it (needs edit) |
| `html.page.version` | `octo-cli html page version` | read | Render a published version as an HTML page (needs reader) |
| `html.page.version_head` | `octo-cli html page version-head` | read | Check a rendered version page without downloading it (needs reader) |
| `html.page.export` | `octo-cli html page export` | read | Download a self-contained export bundle of a version (needs reader) |
| `html.page.fork` | `octo-cli html page fork` | read | Download a self-contained fork bundle of a version (needs reader) |
| `html.asset.get` | `octo-cli html asset get` | read | Download a document media asset (needs reader or signed URL) |
| `html.asset.head` | `octo-cli html asset head` | read | Check a document media asset without downloading it (needs reader or signed URL) |
