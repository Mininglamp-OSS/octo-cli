# Bot API operations

Operations a Bot token can call directly. Use `octo-cli schema <operation-id>` (or MCP `get_skill` with intent=describe) for the exact parameters; this page only says what each one is for and what it needs.

## Workspace and runtime management

Fleet public-API management routes. A Bot needs the matching workspace role (owner for delete, owner/admin for update and member add) or runtime ownership; many runtime actions also need the runtime online.

| Operation | Command | Risk | Purpose |
| --- | --- | --- | --- |
| `workspace.create` | `octo-cli loop workspace create` | write | Create workspace |
| `workspace.update` | `octo-cli loop workspace update` | write | Update workspace |
| `workspace.delete` | `octo-cli loop workspace delete` | high-risk-write | Delete workspace |
| `workspace.member.add` | `octo-cli loop workspace member add` | high-risk-write | Add workspace member |
| `runtime.update` | `octo-cli loop runtime update` | write | Update runtime |
| `runtime.delete` | `octo-cli loop runtime delete` | high-risk-write | Delete runtime |
| `runtime.archive_and_delete` | `octo-cli loop runtime archive-and-delete` | high-risk-write | Archive experts and delete runtime |
| `runtime.update_request.create` | `octo-cli loop runtime update-request create` | write | Request runtime update |
| `runtime.model_request.create` | `octo-cli loop runtime model-request create` | write | Request runtime models |
| `runtime.local_skill_request.create` | `octo-cli loop runtime local-skill-request create` | write | Request local skills |
| `runtime.local_skill_import.create` | `octo-cli loop runtime local-skill-import create` | write | Request local skill import |
| `capability.install` | `octo-cli loop capability install` | write | Install a capability definition |
