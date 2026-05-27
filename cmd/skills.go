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

// newSkillsCmd returns `octo skills`. Three mutually exclusive modes:
//   - `octo skills`: list embedded skills (name + description).
//   - `octo skills <name>`: print one skill's name, description, and content.
//   - `octo skills --install <dir>`: write every skill to <dir>/<name>/SKILL.md.
func newSkillsCmd(f *cmdutil.Factory) *cobra.Command {
	var install string

	cmd := &cobra.Command{
		Use:   "skills [name]",
		Short: "List, print, or install the embedded agent skill docs",
		Long: `Agent-facing SKILL.md docs are embedded in this binary.

  octo skills                    list available skills
  octo skills <name>             print one skill (name, description, content)
  octo skills --install <dir>    write every skill to <dir>/<name>/SKILL.md`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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

// runSkillsShow emits one skill's full content, or a validation error naming
// the available skills when name doesn't match.
func runSkillsShow(f *cmdutil.Factory, entries []skillEntry, name string) error {
	for _, s := range entries {
		if s.Name == name {
			buf, err := json.Marshal(map[string]any{
				"name":        s.Name,
				"description": s.Description,
				"content":     string(s.content),
			})
			if err != nil {
				_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
				return err
			}
			return f.EmitSuccess(buf)
		}
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

// runSkillsInstall writes every skill to <dir>/<name>/SKILL.md and reports the
// files written.
func runSkillsInstall(f *cmdutil.Factory, entries []skillEntry, dir string) error {
	written := make([]string, 0, len(entries))
	for _, s := range entries {
		dst := filepath.Join(dir, s.path)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
			return err
		}
		if err := os.WriteFile(dst, s.content, 0o644); err != nil {
			_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
			return err
		}
		written = append(written, dst)
	}
	buf, err := json.Marshal(map[string]any{"dir": dir, "installed": written})
	if err != nil {
		_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
		return err
	}
	return f.EmitSuccess(buf)
}
