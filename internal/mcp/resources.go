package mcp

import (
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/Mininglamp-OSS/octo-cli/skills"
)

// MCP resources expose each enabled Skill's markdown for progressive, on-demand
// reading. URIs are octo://skills/<name>/<file>.md, one namespace
// shared with `octo-cli skills` and --install. Disabled skills are skipped.
// resources/read returns whole files; clients locate #anchors themselves.

const skillURIScheme = "octo://skills/"

type resourceEntry struct {
	URI         string              `json:"uri"`
	Name        string              `json:"name"`
	MimeType    string              `json:"mimeType"`
	Annotations resourceAnnotations `json:"annotations,omitempty"`
}

type resourceAnnotations struct {
	Skill     string   `json:"skill"`
	Kind      string   `json:"kind"`
	Version   string   `json:"version,omitempty"`
	Etag      string   `json:"etag,omitempty"`
	SizeBytes int      `json:"size_bytes,omitempty"`
	Services  []string `json:"services,omitempty"`
	Requires  []string `json:"requires,omitempty"`
}

// listResources enumerates every enabled skill's SKILL.md plus its reference
// files, with the minimal per-item metadata.
func (s *Server) listResources() map[string]any {
	var out []resourceEntry
	for _, meta := range s.mapping.enabledSkillMetas() {
		mainBytes, readErr := skills.FS.ReadFile(meta.Name + "/SKILL.md")
		if readErr != nil {
			continue
		}
		mainBytes = mcpSkillBytes(mainBytes)
		out = append(out, resourceEntry{
			URI:      skillResourceURI(meta.Name),
			Name:     meta.Name + "/SKILL.md",
			MimeType: "text/markdown",
			Annotations: resourceAnnotations{
				Skill: meta.Name, Kind: "SKILL.md", Version: meta.Version,
				Etag: etag12(mainBytes), SizeBytes: len(mainBytes),
				Services: meta.Services, Requires: meta.Requires,
			},
		})
		refs, _ := fs.Glob(skills.FS, meta.Name+"/*.md") //nolint:errcheck // pattern uses validated embedded skill names and a fixed suffix
		sort.Strings(refs)
		for _, p := range refs {
			base := p[strings.LastIndexByte(p, '/')+1:]
			if base == "SKILL.md" {
				continue
			}
			b, err := skills.FS.ReadFile(p)
			if err != nil {
				continue
			}
			b = mcpSkillBytes(b)
			out = append(out, resourceEntry{
				URI:      refResourceURI(meta.Name, base),
				Name:     meta.Name + "/" + base,
				MimeType: "text/markdown",
				Annotations: resourceAnnotations{
					Skill: meta.Name, Kind: "reference", Version: meta.Version,
					Etag: etag12(b), SizeBytes: len(b),
				},
			})
		}
	}
	return map[string]any{"resources": out}
}

type resourceContent struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType"`
	Text     string `json:"text"`
}

// readResource returns one skill markdown file's contents. It refuses any URI
// outside the octo://skills/ namespace and any disabled skill. A trailing
// #fragment (a section_uri anchor from search/describe) is stripped before the
// file lookup — MCP resources have no fragment semantics, so the whole file is
// returned and the client locates the anchor itself.
func (s *Server) readResource(uri string) (map[string]any, error) {
	uri, _, _ = strings.Cut(uri, "#")
	if !strings.HasPrefix(uri, skillURIScheme) {
		return nil, fmt.Errorf("unsupported resource uri %q", uri)
	}
	rel := strings.TrimPrefix(uri, skillURIScheme)
	name, file, ok := strings.Cut(rel, "/")
	if !ok || name == "" || file == "" || strings.Contains(file, "/") {
		return nil, fmt.Errorf("malformed skill resource uri %q", uri)
	}
	meta := s.mapping.skills[name]
	if meta == nil || meta.Disabled {
		return nil, fmt.Errorf("unknown or unavailable skill %q", name)
	}
	b, err := skills.FS.ReadFile(name + "/" + file)
	if err != nil {
		return nil, fmt.Errorf("unknown skill resource %q", uri)
	}
	b = mcpSkillBytes(b)
	return map[string]any{
		"contents": []resourceContent{{URI: uri, MimeType: "text/markdown", Text: string(b)}},
	}, nil
}

// Ordinary CLI skills can describe delegation features. MCP navigation must
// not advertise those calls as available in its Bot-only execution surface.
func mcpSkillBytes(raw []byte) []byte {
	lines := strings.Split(string(raw), "\n")
	out := lines[:0]
	for _, line := range lines {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "obo-grant") || strings.Contains(lower, "on_behalf_of") {
			continue
		}
		out = append(out, line)
	}
	return []byte(strings.Join(out, "\n"))
}

func etag12(b []byte) string {
	sum := sha256Sum(b)
	return sum[:12]
}
