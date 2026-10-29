# Bot API operations

Operations a Bot token can call directly. Use `octo-cli schema <operation-id>` (or MCP `get_skill` with intent=describe) for the exact parameters; this page only says what each one is for and what it needs.

## Workspace membership and capabilities

Fleet public-API routes. Adding a member needs the owner or admin workspace role.

Workspace create/update/delete and runtime management (update, delete, archive-and-delete, update/model/local-skill requests and local-skill imports) are intentionally not available through the CLI or MCP.

| Operation | Command | Risk | Purpose |
| --- | --- | --- | --- |
| `workspace.member.add` | `octo-cli loop workspace member add` | high-risk-write | Add workspace member |
| `capability.install` | `octo-cli loop capability install` | write | Install a capability definition |
