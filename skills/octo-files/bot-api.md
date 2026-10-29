# Bot API operations

Operations a Bot token can call directly. Use `octo-cli schema <operation-id>` (or MCP `get_skill` with intent=describe) for the exact parameters; this page only says what each one is for and what it needs.

## Bot identity and targets

Self-inspection and lookup helpers for the calling Bot. obo-grants-self and space-principal take an explicit space_id; an MCP connection with a forced space overrides it.

| Operation | Command | Risk | Purpose |
| --- | --- | --- | --- |
| `bot.card-profile` | `octo-cli bot card-profile` | read | Read the card capability manifest and the bot's effective card settings |
| `bot.obo-grant` | `octo-cli bot obo-grant` | read | Read the active on-behalf-of grant whose grantee is the calling bot |
| `bot.obo-grants-self` | `octo-cli bot obo-grants-self` | read | Read the bot's policy grant status in one space (User Bot only, advisory) |
| `bot.resolve-targets` | `octo-cli bot resolve-targets` | read | Resolve groups and threads the bot belongs to by name |
| `bot.space-principal` | `octo-cli bot space-principal` | read | Resolve one principal inside an explicit space (User Bot only) |
| `bot.users-batch` | `octo-cli bot users-batch` | read | Resolve minimal identity info for a batch of user UIDs |
