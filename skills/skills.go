// Package skills embeds the agent-facing SKILL.md documents so the binary can
// export them via `octo skills` without shipping separate files alongside it.
package skills

import "embed"

// FS holds every <skill-name>/SKILL.md under this directory, keyed by its
// relative path (e.g. "octo-messaging/SKILL.md").
//
//go:embed */SKILL.md
var FS embed.FS
