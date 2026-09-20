package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
	"github.com/Mininglamp-OSS/octo-cli/skills"
)

// Recommendation levels for Skill navigation (design §3.4.2). Three states,
// not a boolean: read operations benefit from a Skill but a confident caller
// may skip it; write operations with process semantics should load it first;
// pure supplementary material never blocks a call.
const (
	levelRecommended         = "recommended"
	levelRequiredBeforeWrite = "required-before-write"
	levelOptional            = "optional"
)

// Hard length caps so a short summary can never grow into a second copy of the
// Skill (design §3.6(e) check 6).
const (
	maxSummaryShort = 160
	maxAdvice       = 200
)

// manifestFile is the on-disk shape of skills/manifest.json.
type manifestFile struct {
	Version int                      `json:"version"`
	Skills  map[string]manifestSkill `json:"skills"`
}

type manifestSkill struct {
	SummaryShort string                       `json:"summary_short"`
	References   map[string]manifestReference `json:"references"`
	Operations   map[string]manifestOp        `json:"operations"`
}

type manifestReference struct {
	Topics []string `json:"topics"`
}

type manifestOp struct {
	Level   string `json:"level"`
	Advice  string `json:"advice"`
	Section string `json:"section"`
}

// skillMeta is the merged, validated view of one Skill.
type skillMeta struct {
	Name         string
	SummaryShort string
	Services     []string
	Requires     []string
	Version      string
	Disabled     bool
	References   map[string][]string // reference file -> topic keywords
	Etag         string              // sha256(SKILL.md)[:12]
	SizeBytes    int
}

// opMeta is the operation-level Skill override merged from the manifest.
type opMeta struct {
	Level   string
	Advice  string
	Section string // "<file>#<anchor>" or "" — anchor validated at build
}

// Mapping is the authoritative, in-memory service/operation -> Skill view. It
// is built once at server start (or in CI) from frontmatter + manifest and
// validated fail-fast, so runtime handlers read a merged view with zero
// hard-coded scattered mappings.
type Mapping struct {
	serviceToSkill map[string]string     // service -> skill name (enabled only)
	skills         map[string]*skillMeta // skill name -> meta
	ops            map[string]*opMeta    // operation_id -> override
	mappingEtag    string                // sha256 over the merged table
}

// BuildMapping parses every embedded SKILL.md frontmatter and the manifest,
// merges them, and validates the result. A non-nil error lists every conflict
// found (fail-fast): the server refuses to start rather than serve a drifted
// or self-inconsistent navigation table.
func BuildMapping(reg *registry.Registry) (*Mapping, error) {
	metas, err := loadSkillMetas()
	if err != nil {
		return nil, err
	}
	var mf manifestFile
	if err := json.Unmarshal(skills.Manifest, &mf); err != nil {
		return nil, fmt.Errorf("mcp: parse skills/manifest.json: %w", err)
	}

	m := &Mapping{
		serviceToSkill: map[string]string{},
		skills:         map[string]*skillMeta{},
		ops:            map[string]*opMeta{},
	}

	// Merge manifest metadata onto each skill.
	for name, meta := range metas {
		if ms, ok := mf.Skills[name]; ok {
			meta.SummaryShort = ms.SummaryShort
			meta.References = map[string][]string{}
			for ref, r := range ms.References {
				meta.References[ref] = append([]string(nil), r.Topics...)
			}
		}
		m.skills[name] = meta
		// Only enabled skills contribute service navigation.
		if meta.Disabled {
			continue
		}
		for _, svc := range meta.Services {
			m.serviceToSkill[svc] = name
		}
	}

	// Operation-level overrides.
	for name, ms := range mf.Skills {
		for opID, op := range ms.Operations {
			level := op.Level
			if level == "" {
				level = levelRecommended
			}
			m.ops[opID] = &opMeta{Level: level, Advice: op.Advice, Section: op.Section}
			_ = name
		}
	}

	if problems := m.validate(reg, &mf, metas); len(problems) > 0 {
		return nil, fmt.Errorf("mcp: skill navigation mapping is invalid:\n  - %s", strings.Join(problems, "\n  - "))
	}
	m.mappingEtag = m.computeEtag()
	return m, nil
}

// validate runs the six drift checks (design §3.6(e)). It collects every
// problem rather than returning the first, so a maintainer sees the whole set.
func (m *Mapping) validate(reg *registry.Registry, mf *manifestFile, metas map[string]*skillMeta) []string {
	var problems []string

	enabledServices := map[string]bool{}
	for _, s := range reg.EnabledServices() {
		enabledServices[s] = true
	}
	enabledOps := map[string]bool{}
	for _, op := range reg.EnabledOperations() {
		enabledOps[op.ID] = true
	}
	allServices := map[string]bool{}
	for _, s := range reg.ListServices() {
		allServices[s] = true
	}

	// Check 1: every frontmatter/manifest service reference exists in the
	// registry; every manifest operation exists in the enabled registry.
	// Check 6 (part): only one enabled skill may claim a given service.
	claimedBy := map[string]string{}
	for name, meta := range metas {
		for _, svc := range meta.Services {
			if !allServices[svc] {
				problems = append(problems, fmt.Sprintf("skill %q declares services: %q which is not a registry service", name, svc))
				continue
			}
			if meta.Disabled {
				continue
			}
			if prev, ok := claimedBy[svc]; ok {
				problems = append(problems, fmt.Sprintf("service %q is claimed by two enabled skills (%q and %q)", svc, prev, name))
			}
			claimedBy[svc] = name
		}
	}
	for name, ms := range mf.Skills {
		if _, ok := metas[name]; !ok {
			problems = append(problems, fmt.Sprintf("manifest references unknown skill %q", name))
		}
		for opID := range ms.Operations {
			if !enabledOps[opID] {
				problems = append(problems, fmt.Sprintf("manifest skill %q references operation %q which is not an enabled registry operation", name, opID))
			}
		}
	}

	// Check 2: every enabled service is covered by >=1 enabled skill (shared is
	// exempt — it is horizontal and declares no services).
	for svc := range enabledServices {
		if _, ok := m.serviceToSkill[svc]; !ok {
			problems = append(problems, fmt.Sprintf("enabled service %q has no Skill navigation (no enabled skill declares it in services:)", svc))
		}
	}

	// Check 4: manifest reference files must exist; section anchors must exist.
	for name, ms := range mf.Skills {
		for ref := range ms.References {
			if _, err := skills.FS.ReadFile(name + "/" + ref); err != nil {
				problems = append(problems, fmt.Sprintf("skill %q registers reference file %q which is not embedded", name, ref))
			}
		}
		for opID, op := range ms.Operations {
			if op.Section == "" {
				continue
			}
			file, anchor, ok := strings.Cut(op.Section, "#")
			if !ok || file == "" {
				problems = append(problems, fmt.Sprintf("operation %q section %q must be of the form <file>#<anchor>", opID, op.Section))
				continue
			}
			content, err := skills.FS.ReadFile(name + "/" + file)
			if err != nil {
				problems = append(problems, fmt.Sprintf("operation %q section points at %q which is not embedded", opID, file))
				continue
			}
			if anchor != "" && !headingAnchorExists(content, anchor) {
				problems = append(problems, fmt.Sprintf("operation %q section anchor %q is not a heading in %s/%s", opID, anchor, name, file))
			}
		}
	}

	// Check 5: disabled service <-> disabled skill sync; a disabled skill may
	// not be referenced by any enabled service mapping.
	for _, svc := range reg.ListServices() {
		if !reg.ServiceDisabled(svc) {
			continue
		}
		for name, meta := range metas {
			if containsStr(meta.Services, svc) && !meta.Disabled {
				problems = append(problems, fmt.Sprintf("service %q is disabled but skill %q claiming it is enabled", svc, name))
			}
		}
	}

	// Check 6: length caps and level validity. summary_short and advice must
	// stay within their caps so a short summary cannot grow into a second copy
	// of the Skill, and every declared level must be one of the three known
	// values. (Only-raise-never-lower is not modeled here: the manifest holds
	// the sole per-op level, so there is no separate baseline to lower from.)
	for name, meta := range metas {
		if len([]rune(meta.SummaryShort)) > maxSummaryShort {
			problems = append(problems, fmt.Sprintf("skill %q summary_short exceeds %d chars", name, maxSummaryShort))
		}
	}
	for opID, op := range m.ops {
		if len([]rune(op.Advice)) > maxAdvice {
			problems = append(problems, fmt.Sprintf("operation %q advice exceeds %d chars", opID, maxAdvice))
		}
		switch op.Level {
		case levelRecommended, levelRequiredBeforeWrite, levelOptional:
		default:
			problems = append(problems, fmt.Sprintf("operation %q has invalid level %q", opID, op.Level))
		}
	}

	sort.Strings(problems)
	return problems
}

// SkillFor returns the merged Skill metadata for an operation and whether a
// mapping exists. A missing mapping is not an error — the caller omits the
// skill block and the operation stays callable (design §3.4.4).
func (m *Mapping) SkillFor(op registry.OperationInfo) (*skillMeta, *opMeta, bool) {
	name, ok := m.serviceToSkill[op.Service]
	if !ok {
		return nil, nil, false
	}
	meta := m.skills[name]
	if meta == nil || meta.Disabled {
		return nil, nil, false
	}
	return meta, m.ops[op.ID], true
}

// enabledSkillMetas returns every enabled skill's metadata, sorted by name.
func (m *Mapping) enabledSkillMetas() []*skillMeta {
	out := make([]*skillMeta, 0, len(m.skills))
	for _, meta := range m.skills {
		if !meta.Disabled {
			out = append(out, meta)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (m *Mapping) computeEtag() string {
	h := sha256.New()
	h.Write(skills.Manifest)
	svcs := make([]string, 0, len(m.serviceToSkill))
	for s := range m.serviceToSkill {
		svcs = append(svcs, s)
	}
	sort.Strings(svcs)
	for _, s := range svcs {
		fmt.Fprintf(h, "%s=%s;", s, m.serviceToSkill[s])
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// loadSkillMetas parses every embedded SKILL.md frontmatter. It uses the same
// single-line, no-YAML scan as cmd/skills.go so the parser can't disagree with
// the CLI on what a frontmatter line means.
func loadSkillMetas() (map[string]*skillMeta, error) {
	paths, err := fs.Glob(skills.FS, "*/SKILL.md")
	if err != nil {
		return nil, err
	}
	out := make(map[string]*skillMeta, len(paths))
	for _, p := range paths {
		b, err := skills.FS.ReadFile(p)
		if err != nil {
			return nil, err
		}
		name := strings.SplitN(p, "/", 2)[0]
		sum := sha256.Sum256(b)
		out[name] = &skillMeta{
			Name:      name,
			Services:  parseFrontmatterList(b, "services:"),
			Requires:  parseRequiresSkills(b),
			Version:   parseFrontmatterScalar(b, "version:"),
			Disabled:  parseFrontmatterScalar(b, "disabled:") == "true",
			Etag:      hex.EncodeToString(sum[:])[:12],
			SizeBytes: len(b),
		}
	}
	return out, nil
}

// frontmatterLines yields the lines inside the leading `---` fence.
func frontmatterLines(b []byte) []string {
	var lines []string
	in := false
	for _, ln := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(ln)
		if t == "---" {
			if in {
				break
			}
			in = true
			continue
		}
		if in {
			lines = append(lines, ln)
		}
	}
	return lines
}

// parseFrontmatterScalar returns the single-line value for a top-level key.
func parseFrontmatterScalar(b []byte, key string) string {
	for _, ln := range frontmatterLines(b) {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, key) {
			return strings.TrimSpace(strings.TrimPrefix(t, key))
		}
	}
	return ""
}

// parseFrontmatterList parses a single-line inline JSON array value, e.g.
// `services: ["message", "group"]`. Matches the requires.bins convention so no
// YAML dependency is introduced.
func parseFrontmatterList(b []byte, key string) []string {
	raw := parseFrontmatterScalar(b, key)
	return parseInlineJSONArray(raw)
}

// parseRequiresSkills best-effort reads `skills: [...]` under metadata.requires.
func parseRequiresSkills(b []byte) []string {
	for _, ln := range frontmatterLines(b) {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "skills:") {
			return parseInlineJSONArray(strings.TrimSpace(strings.TrimPrefix(t, "skills:")))
		}
	}
	return nil
}

func parseInlineJSONArray(raw string) []string {
	start := strings.IndexByte(raw, '[')
	end := strings.LastIndexByte(raw, ']')
	if start < 0 || end <= start {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw[start:end+1]), &out); err != nil {
		return nil
	}
	return out
}

// headingAnchorExists reports whether any markdown heading in content slugifies
// to anchor (GitHub-style).
func headingAnchorExists(content []byte, anchor string) bool {
	for _, ln := range strings.Split(string(content), "\n") {
		t := strings.TrimSpace(ln)
		if !strings.HasPrefix(t, "#") {
			continue
		}
		heading := strings.TrimLeft(t, "#")
		if slugify(heading) == anchor {
			return true
		}
	}
	return false
}

// slugify renders a GitHub-style heading anchor: lowercase, spaces to hyphens,
// drop characters that are not alphanumeric or hyphen, and trim/collapse the
// hyphens so a leading "## " does not produce a leading dash.
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteRune('-')
		}
	}
	out := b.String()
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	return strings.Trim(out, "-")
}

// sha256Sum returns the hex-encoded sha256 of b.
func sha256Sum(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
