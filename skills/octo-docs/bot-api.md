# Bot API operations

Operations a Bot token can call directly. Use `octo-cli schema <operation-id>` (or MCP `get_skill` with intent=describe) for the exact parameters; this page only says what each one is for and what it needs.

## Access requests and invites

Admin-side sharing flows. Approving a request, creating an invite, accepting an invite and batch forward grants all widen access.

| Operation | Command | Risk | Purpose |
| --- | --- | --- | --- |
| `docs.access-requests.create` | `octo-cli docs access-requests create` | write | Ask the document admins for access as the bot itself (no document role needed) |
| `docs.access-requests.list` | `octo-cli docs access-requests list` | read | List a document's access requests by status (needs admin) |
| `docs.access-requests.approve` | `octo-cli docs access-requests approve` | high-risk-write | Approve a pending access request and grant the requester a role (needs admin) |
| `docs.access-requests.deny` | `octo-cli docs access-requests deny` | write | Deny a pending access request (needs admin) |
| `docs.invites.create` | `octo-cli docs invites create` | high-risk-write | Create a link invite token for a document (needs admin) |
| `docs.invites.list` | `octo-cli docs invites list` | read | List a document's active link invites (needs admin) |
| `docs.invites.revoke` | `octo-cli docs invites revoke` | write | Revoke a document link invite (needs admin) |
| `docs.invites.accept` | `octo-cli docs invites accept` | high-risk-write | Accept a document link invite as the bot itself |
| `docs.forward-grant.batch` | `octo-cli docs forward-grant batch` | high-risk-write | Grant (never downgrade) reader/commenter/writer access to many uids at once (needs admin) |

## Raw uploads and imports

These routes take the file bytes as the request body: pass file_path (MCP) or --file (CLI). MCP uploads need OCTO_MCP_UPLOAD_ROOT (or stdio --allow-local-upload). Imports replace document content.

| Operation | Command | Risk | Purpose |
| --- | --- | --- | --- |
| `docs.ppt.media.upload` | `octo-cli docs ppt media upload` | write | Upload one local media file into a PPT document (writer, html_ppt only) |
| `docs.attachments.svg-upload` | `octo-cli docs attachments svg-upload` | write | Upload an SVG image as a document attachment (writer) |
| `docs.raw-import.docx` | `octo-cli docs raw-import docx` | high-risk-write | Import a .docx file into a doc document (writer) |
| `docs.raw-import.markdown` | `octo-cli docs raw-import markdown` | high-risk-write | Import a Markdown file into a doc document (writer) |
| `docs.raw-import.xlsx` | `octo-cli docs raw-import xlsx` | high-risk-write | Import an .xlsx workbook into a sheet document (writer) |
| `docs.scene.import` | `octo-cli docs scene import` | high-risk-write | Import an Excalidraw scene into a whiteboard (needs writer) |

## Attachments, exports and media

Binary results (exports, media) come back inline over MCP up to 4 MiB as content_text or content_base64 with a sha256; larger bodies report inline:false.

| Operation | Command | Risk | Purpose |
| --- | --- | --- | --- |
| `docs.attachments.copy` | `octo-cli docs attachments copy` | write | Copy stored attachments from other documents into this document (needs writer) |
| `docs.attachments.ingest` | `octo-cli docs attachments ingest` | write | Fetch external URLs server-side and store them as document attachments (needs writer) |
| `docs.ppt.media.get` | `octo-cli docs ppt media get` | read | Download one PPT media attachment's bytes (reader or higher, html_ppt only) |
| `docs.file.export` | `octo-cli docs file export` | read | Export a document or sheet as an md, docx, pdf or xlsx file (needs reader) |
| `docs.link-card` | `octo-cli docs link-card` | read | Resolve an Open Graph link preview card for a URL (needs reader) |

## Comment tasks, history and summaries

Comment-task progress for the Bot's own tasks, comment threads and markers, recent-view history and summary sources. For an HTML comment task the path docId must be the octo_doc_slug.

| Operation | Command | Risk | Purpose |
| --- | --- | --- | --- |
| `docs.comment-task.report` | `octo-cli docs comment-task report` | write | Report progress and public tool events for the bot's own comment task |
| `docs.comment-task.get` | `octo-cli docs comment-task get` | read | Read the progress record of a bot comment task (needs reader) |
| `docs.comment-task.events` | `octo-cli docs comment-task events` | read | Read the public tool-event transcript of a comment task (needs reader) |
| `docs.comments.thread` | `octo-cli docs comments thread` | read | Fetch one comment thread root with its replies (needs reader) |
| `docs.comments.markers` | `octo-cli docs comments markers` | read | List unresolved comment markers of a whiteboard (needs reader) |
| `docs.recent.list` | `octo-cli docs recent list` | read | List documents the bot recently viewed (keyset-paginated, newest first) |
| `docs.recent.creators` | `octo-cli docs recent creators` | read | List distinct creators of the bot's recently viewed documents |
| `docs.view` | `octo-cli docs view` | write | Record that the bot opened a document in its recent-view history (needs reader) |
| `docs.published` | `octo-cli docs published` | write | Notify docs-backend that an HTML document was durably published (owner bot only) |
| `docs.summary-source` | `octo-cli docs summary-source` | read | Read a rich-text document flattened to plain text for summarization (needs reader) |
| `docs.octo-doc.rename` | `octo-cli docs octo-doc rename` | write | Rename a registered HTML document by its octo-doc slug (needs admin) |
