package cmd

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
	"github.com/Mininglamp-OSS/octo-cli/skills"
)

type skillEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	path        string // embed path, e.g. "octo-messaging/SKILL.md"
	content     []byte
}

// loadSkills reads every embedded SKILL.md and parses its frontmatter
// description. Sorted by name for deterministic output.
func loadSkills() ([]skillEntry, error) {
	paths, err := fs.Glob(skills.FS, "*/SKILL.md")
	if err != nil {
		return nil, err
	}
	out := make([]skillEntry, 0, len(paths))
	for _, p := range paths {
		b, err := skills.FS.ReadFile(p)
		if err != nil {
			return nil, err
		}
		// Skills marked `disabled: true` in their frontmatter are withheld
		// (e.g. octo-matter, whose backend API is not yet stable). The file
		// stays embedded — drop the flag to re-list it.
		if skillDisabled(b) {
			continue
		}
		out = append(out, skillEntry{
			Name:        strings.SplitN(p, "/", 2)[0],
			Description: parseSkillDescription(b),
			path:        p,
			content:     b,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// parseSkillDescription pulls `description:` out of the leading `---` YAML
// frontmatter block with a line scan (no YAML dependency). It assumes a
// single-line `description:` value — the shape every bundled SKILL.md uses;
// multi-line or block-scalar descriptions are not supported.
func parseSkillDescription(b []byte) string {
	inFrontmatter := false
	for _, ln := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(ln)
		if t == "---" {
			if inFrontmatter {
				break
			}
			inFrontmatter = true
			continue
		}
		if inFrontmatter && strings.HasPrefix(t, "description:") {
			return strings.TrimSpace(strings.TrimPrefix(t, "description:"))
		}
	}
	return ""
}

// skillDisabled reports whether the SKILL.md frontmatter sets `disabled: true`.
// Uses the same single-line frontmatter scan as parseSkillDescription, and the
// same "true" truthiness as the spec's x-octo-disabled flag (registry.truthy).
func skillDisabled(b []byte) bool {
	inFrontmatter := false
	for _, ln := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(ln)
		if t == "---" {
			if inFrontmatter {
				break
			}
			inFrontmatter = true
			continue
		}
		if inFrontmatter && strings.HasPrefix(t, "disabled:") {
			return strings.TrimSpace(strings.TrimPrefix(t, "disabled:")) == "true"
		}
	}
	return false
}

// newSkillsCmd returns `octo-cli skills`. Three mutually exclusive modes:
//   - `octo-cli skills`: list embedded skills (name + description).
//   - `octo-cli skills <name>`: print one skill's name, description, content
//     (SKILL.md), and any progressive-disclosure reference files.
//   - `octo-cli skills --install <dir>`: write every skill to <dir>/<name>/SKILL.md.
func newSkillsCmd(f *cmdutil.Factory) *cobra.Command {
	var install string

	cmd := &cobra.Command{
		Use:   "skills [name]",
		Short: "List, print, or install the embedded agent skill docs",
		Long: `Agent-facing SKILL.md docs are embedded in this binary.

  octo-cli skills                    list available skills
  octo-cli skills <name>             print one skill (name, description, content + references)
  octo-cli skills --install <dir>    write every skill (SKILL.md + references) to <dir>/<name>/`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("install") && install == "" {
				ee := output.ErrValidation(
					"--install requires a directory",
					"pass a target like --install ~/.config/octo/skills",
				)
				_ = f.EmitError(ee) //nolint:errcheck // best-effort emit before returning err
				return ee
			}
			entries, err := loadSkills()
			if err != nil {
				_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
				return err
			}
			switch {
			case install != "":
				if len(args) == 1 {
					ee := output.ErrValidation(
						"cannot combine a skill name with --install",
						"drop the name to install all skills, or drop --install to print one",
					)
					_ = f.EmitError(ee) //nolint:errcheck // best-effort emit before returning err
					return ee
				}
				return runSkillsInstall(f, entries, install)
			case len(args) == 1:
				return runSkillsShow(f, entries, args[0])
			default:
				return runSkillsList(f, entries)
			}
		},
	}

	cmd.Flags().StringVar(&install, "install", "", "write all skills to this directory (<dir>/<name>/SKILL.md)")
	return cmd
}

// runSkillsList emits every skill as {name, description}.
func runSkillsList(f *cmdutil.Factory, entries []skillEntry) error {
	buf, err := json.Marshal(map[string]any{"skills": entries})
	if err != nil {
		_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
		return err
	}
	return f.EmitSuccess(buf)
}

// loadSkillReferences returns a skill's progressive-disclosure reference files
// — every sibling *.md next to SKILL.md, excluding SKILL.md itself — keyed by
// base filename (e.g. "sheet.md"). A monolithic skill (only SKILL.md) yields an
// empty map. This is what lets `skills <name>` reprint the whole skill set, not
// just SKILL.md, after a skill is split into references.
func loadSkillReferences(skillPath string) (map[string]string, error) {
	skillDir := strings.TrimSuffix(skillPath, "/SKILL.md")
	paths, err := fs.Glob(skills.FS, skillDir+"/*.md")
	if err != nil {
		return nil, err
	}
	refs := make(map[string]string)
	for _, p := range paths {
		if filepath.Base(p) == "SKILL.md" {
			continue
		}
		b, rerr := skills.FS.ReadFile(p)
		if rerr != nil {
			return nil, rerr
		}
		refs[filepath.Base(p)] = string(b)
	}
	return refs, nil
}

// runSkillsShow emits one skill's full content — SKILL.md plus any
// progressive-disclosure reference files — or a validation error naming the
// available skills when name doesn't match. `content` holds SKILL.md;
// `references` maps each sibling reference filename to its content so the whole
// skill set is reprinted, not just the router.
func runSkillsShow(f *cmdutil.Factory, entries []skillEntry, name string) error {
	for _, s := range entries {
		if s.Name != name {
			continue
		}
		refs, rerr := loadSkillReferences(s.path)
		if rerr != nil {
			_ = f.EmitError(rerr) //nolint:errcheck // best-effort emit before returning err
			return rerr
		}
		payload := map[string]any{
			"name":        s.Name,
			"description": s.Description,
			"content":     string(s.content),
		}
		if len(refs) > 0 {
			payload["references"] = refs
		}
		buf, err := json.Marshal(payload)
		if err != nil {
			_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
			return err
		}
		return f.EmitSuccess(buf)
	}
	names := make([]string, len(entries))
	for i, s := range entries {
		names[i] = s.Name
	}
	ee := output.ErrValidation(
		fmt.Sprintf("unknown skill %q", name),
		fmt.Sprintf("available: %s", strings.Join(names, ", ")),
	)
	_ = f.EmitError(ee) //nolint:errcheck // best-effort emit before returning err
	return ee
}

// runSkillsInstall writes every enabled skill's whole markdown set to
// <dir>/<name>/ — SKILL.md plus any sibling progressive-disclosure reference
// files (e.g. octo-docs/sheet.md) — and reports the files written.
func runSkillsInstall(f *cmdutil.Factory, entries []skillEntry, dir string) error {
	written := make([]string, 0, len(entries))
	seen := make(map[string]bool)
	for _, s := range entries {
		// The skill's directory is the SKILL.md path minus the filename; glob it
		// for every markdown doc so reference files ride along with SKILL.md.
		skillDir := strings.TrimSuffix(s.path, "/SKILL.md")
		refs, err := fs.Glob(skills.FS, skillDir+"/*.md")
		if err != nil {
			_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
			return err
		}
		for _, p := range refs {
			if seen[p] {
				continue
			}
			seen[p] = true
			b, err := skills.FS.ReadFile(p)
			if err != nil {
				_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
				return err
			}
			dst := filepath.Join(dir, p)
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
				return err
			}
			if err := os.WriteFile(dst, b, 0o644); err != nil {
				_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
				return err
			}
			written = append(written, dst)
		}
	}
	buf, err := json.Marshal(map[string]any{"dir": dir, "installed": written})
	if err != nil {
		_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
		return err
	}
	return f.EmitSuccess(buf)
}
