# Bot API operations

Operations a Bot token can call directly. Use `octo-cli schema <operation-id>` (or MCP `get_skill` with intent=describe) for the exact parameters; this page only says what each one is for and what it needs.

## Message lookup and card history

Read single messages by id and manage card revision history.

| Operation | Command | Risk | Purpose |
| --- | --- | --- | --- |
| `message.group-get` | `octo-cli message group-get` | read | Get one group message by id (User Bot only) |
| `message.person-get` | `octo-cli message person-get` | read | Get one direct message by id |
| `message.thread-get` | `octo-cli message thread-get` | read | Get one thread message by id (User Bot only) |
| `message.card-revisions-clear` | `octo-cli message card-revisions-clear` | high-risk-write | Erase the revision history of a card message the bot sent |

## Group and thread lifecycle

Mention preference and thread archive/delete. thread.delete is destructive; prefer thread.archive when the goal is to close a thread. A Bot has no unarchive route, so an archive is not reversible through the Bot API.

| Operation | Command | Risk | Purpose |
| --- | --- | --- | --- |
| `group.mention-pref` | `octo-cli group mention-pref` | read | Read whether the bot may reply in a group without being @mentioned |
| `thread.archive` | `octo-cli thread archive` | high-risk-write | Archive a thread (User Bot only; thread creator or group owner/manager) |
| `thread.delete` | `octo-cli thread delete` | high-risk-write | Delete a thread (User Bot only; thread creator or group owner/manager) |

## Incoming webhooks

Group- and thread-bound incoming webhooks. Create and regenerate return the push token once; treat it as a credential and never post it into a chat. Management needs group owner/admin or the webhook creator; writes require incomingwebhook.enabled on the server.

| Operation | Command | Risk | Purpose |
| --- | --- | --- | --- |
| `group.webhook.create` | `octo-cli group webhook create` | high-risk-write | Create a group incoming webhook (returns its push token once) |
| `group.webhook.list` | `octo-cli group webhook list` | read | List the group's incoming webhooks |
| `group.webhook.update` | `octo-cli group webhook update` | write | Update a group incoming webhook |
| `group.webhook.delete` | `octo-cli group webhook delete` | high-risk-write | Delete a group incoming webhook |
| `group.webhook.regenerate` | `octo-cli group webhook regenerate` | high-risk-write | Rotate a group incoming webhook's push token |
| `group.webhook.test` | `octo-cli group webhook test` | write | Send a test message through a group incoming webhook |
| `group.webhook.deliveries` | `octo-cli group webhook deliveries` | read | List recent deliveries of a group incoming webhook |
| `thread.webhook.create` | `octo-cli thread webhook create` | high-risk-write | Create a thread-bound incoming webhook (returns its push token once) |
| `thread.webhook.list` | `octo-cli thread webhook list` | read | List a thread's incoming webhooks |
| `thread.webhook.update` | `octo-cli thread webhook update` | write | Update a thread-bound incoming webhook |
| `thread.webhook.delete` | `octo-cli thread webhook delete` | high-risk-write | Delete a thread-bound incoming webhook |
| `thread.webhook.regenerate` | `octo-cli thread webhook regenerate` | high-risk-write | Rotate a thread-bound incoming webhook's push token |
| `thread.webhook.test` | `octo-cli thread webhook test` | write | Send a test message through a thread-bound incoming webhook |
| `thread.webhook.deliveries` | `octo-cli thread webhook deliveries` | read | List recent deliveries of a thread-bound incoming webhook |
