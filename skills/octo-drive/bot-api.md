# Bot API operations

Operations a Bot token can call directly. Use `octo-cli schema <operation-id>` (or MCP `get_skill` with intent=describe) for the exact parameters; this page only says what each one is for and what it needs.

## Trash, share transfer and lookups

Bot-mount drive operations. trash delete is permanent. Share tokens are credential-equivalent and stay masked in output. mount-by-role takes the caller's Octo space in X-Space-Id (forced by an MCP connection that sets a space).

| Operation | Command | Risk | Purpose |
| --- | --- | --- | --- |
| `drive.hello` | `octo-cli drive hello` | read | Probe the drive mount and report the verified caller identity |
| `drive.file.ancestors` | `octo-cli drive file ancestors` | read | List the folder path from the space root to a node's parent |
| `drive.file.doc-alive` | `octo-cli drive file doc-alive` | read | Check whether a mounted document still exists in the docs backend |
| `drive.doc.mount-by-role` | `octo-cli drive doc mount-by-role` | write | Mount an online document after checking the caller's docs-backend admin role |
| `drive.im-transfer.lookup` | `octo-cli drive im-transfer lookup` | write | Check which IM attachments are already saved in the caller's drive spaces |
| `drive.share.transfer` | `octo-cli drive share transfer` | write | Save a copy of a shared blob into a drive space |
| `drive.share.existing-copies` | `octo-cli drive share existing-copies` | read | List copies of a shared blob the caller already holds |
| `drive.trash.list` | `octo-cli drive trash list` | read | List recycle-bin entries the caller may see |
| `drive.trash.restore` | `octo-cli drive trash restore` | write | Restore a recycle-bin entry |
| `drive.trash.delete` | `octo-cli drive trash delete` | high-risk-write | Permanently delete a recycle-bin entry (irreversible) |
