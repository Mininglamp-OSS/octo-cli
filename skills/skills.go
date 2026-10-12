// Package skills embeds the agent-facing SKILL.md documents so the binary can
// export them via `octo-cli skills` without shipping separate files alongside it.
package skills

import "embed"

// FS holds every markdown doc under each <skill-name>/ directory: the skill's
// own SKILL.md plus any progressive-disclosure reference files (e.g.
// "octo-docs/sheet.md"), keyed by relative path. Listing still keys off
// */SKILL.md; the sibling .md files are reference docs loaded on demand.
//
//go:embed */*.md
var FS embed.FS

// Manifest is the embedded skills/manifest.json: the operation-level Skill
// navigation overrides (level / advice / section), per-skill short summaries,
// and reference-file topic registrations that the MCP server merges with each
// SKILL.md's frontmatter `services:` declaration. It ships in the same binary
// as FS and the OpenAPI specs, so the three can never drift across a release.
//
//go:embed manifest.json
var Manifest []byte
