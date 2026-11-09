package mcp

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type catalogSlot struct {
	Domain    string `json:"domain"`
	ID        string `json:"id"`
	MaxLength int    `json:"max_length"`
	Pointer   string `json:"pointer"`
}
type schemaTemplate struct {
	Kind     string        `json:"kind"`
	SHA      string        `json:"sha256"`
	Media    string        `json:"media_type"`
	Renderer string        `json:"renderer"`
	Value    string        `json:"value_base64"`
	Slots    []catalogSlot `json:"slots"`
}
type templateSelection struct {
	OperationID string `json:"operation_id"`
	Facade      string `json:"facade"`
	Resources   bool   `json:"resources"`
	Managed     bool   `json:"managed_fields_present"`
	Truth       string `json:"parameter_truth_hash"`
	Template    string `json:"template_hash"`
}

func replaceTemplateField(raw []byte, path []string, replacement []byte) ([]byte, error) {
	var out bytes.Buffer
	out.WriteByte('{')
	first := true
	found := false
	err := visitObjectMembers(raw, func(key string, value json.RawMessage) error {
		if !first {
			out.WriteByte(',')
		}
		first = false
		k := catalogJSON(key)
		out.Write(k)
		out.WriteByte(':')
		if key == path[0] {
			found = true
			if len(path) == 1 {
				out.Write(replacement)
			} else {
				nested, err := replaceTemplateField(value, path[1:], replacement)
				if err != nil {
					return err
				}
				out.Write(nested)
			}
		} else {
			out.Write(value)
		}
		return nil
	})
	out.WriteByte('}')
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("missing template field")
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, out.Bytes()); err != nil {
		return nil, err
	}
	return compact.Bytes(), nil
}
func templateFor(raw []byte) (schemaTemplate, error) {
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return schemaTemplate{}, err
	}
	raw = compact.Bytes()
	root, err := strictObject(raw)
	if err != nil {
		return schemaTemplate{}, err
	}
	slots := []catalogSlot{}
	if _, ok := root["server_managed_arguments"]; ok {
		raw, err = replaceTemplateField(raw, []string{"server_managed_arguments"}, []byte(`{"$octo_slot":"managed_arguments"}`))
		if err != nil {
			return schemaTemplate{}, err
		}
		slots = append(slots, catalogSlot{"server_managed_argument_names", "managed_arguments", 65536, "/server_managed_arguments"})
	}
	if skill, err := strictObject(root["skill"]); err == nil {
		if _, ok := skill["hint"]; ok {
			raw, err = replaceTemplateField(raw, []string{"skill", "hint"}, []byte(`{"$octo_slot":"resource_hint"}`))
			if err != nil {
				return schemaTemplate{}, err
			}
			slots = append(slots, catalogSlot{"facade_hint", "resource_hint", 65536, "/skill/hint"})
		}
	}
	return schemaTemplate{"template", sha256Sum(raw), "application/json", "go-json-marshalindent-2sp-html-escape-v1", base64.StdEncoding.EncodeToString(raw), slots}, nil
}
func renderTemplate(template *schemaTemplate, s *Server, operationID string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(template.Value)
	if err != nil || sha256Sum(b) != template.SHA {
		return nil, fmt.Errorf("template digest mismatch")
	}
	for _, slot := range template.Slots {
		var value any
		switch slot.Domain {
		case "server_managed_argument_names":
			names := []string{}
			for name := range s.trusted.serverManaged(operationID) {
				names = append(names, name)
			}
			sort.Strings(names)
			value = names
		case "facade_hint":
			root, parseErr := strictObject(b)
			if parseErr != nil {
				return nil, parseErr
			}
			meta, parseErr := strictObject(root["skill"])
			if parseErr != nil {
				return nil, parseErr
			}
			var name string
			if err := json.Unmarshal(meta["name"], &name); err != nil {
				return nil, err
			}
			value = s.resourceDegradeHint(name)
		default:
			return nil, fmt.Errorf("unsupported content slot")
		}
		replacement, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		if len(replacement) > slot.MaxLength {
			return nil, fmt.Errorf("slot limit")
		}
		b, err = replaceTemplateField(b, strings.Split(strings.TrimPrefix(slot.Pointer, "/"), "/"), replacement)
		if err != nil {
			return nil, err
		}
	}
	return json.MarshalIndent(json.RawMessage(b), "", "  ")
}

// MaterializePermissionContent closes dynamic schema templates using whole
// value slots and both resource negotiation shapes. Every mask/facade/depth is
// reconciled with the actual served code before a template is emitted.
func MaterializePermissionContent() ([]byte, error) { //nolint:gocyclo // closed fixed-program branches retain explicit checks in security order
	s, err := NewServer(nil)
	if err != nil {
		return nil, err
	}
	var index sourceIndex
	if err := json.Unmarshal(catalogSourceIndex, &index); err != nil {
		return nil, err
	}
	templates := map[string]schemaTemplate{}
	selections := map[string]templateSelection{}
	truths := map[string]json.RawMessage{}
	observations := 0
	for ei := range index.Entries {
		entry := &index.Entries[ei]
		if entry.Kind != "schema" {
			continue
		}
		var target []string
		if err = json.Unmarshal(entry.Target, &target); err != nil || len(target) != 2 {
			return nil, fmt.Errorf("invalid schema target")
		}
		op := target[1]
		detail, ok := s.reg.GetOperation(op)
		if !ok {
			return nil, fmt.Errorf("missing operation %s", op)
		}
		truth := catalogJSON(detail)
		truthHash := sha256Sum(truth)
		truths[truthHash] = truth
		for _, resources := range []bool{false, true} {
			for mask := 0; mask < 8; mask++ {
				for _, facade := range []facadeMode{facadeThree, facadeTwo, facadeBoth} {
					s.clientResources = resources
					s.facade = facade
					s.trusted = TrustedContext{}
					if mask&1 != 0 {
						s.trusted.SpaceID = "bound-space"
					}
					if mask&2 != 0 {
						s.trusted.ChannelID = "bound-channel"
					}
					if mask&4 != 0 {
						s.trusted.ChannelType = "2"
					}
					for _, variant := range []string{"describe_op", "get_skill_full", "get_skill_summary"} {
						var result toolResult
						switch variant {
						case "describe_op":
							result = s.describeOp(op)
						case "get_skill_full":
							result = s.describeSkill(op, "full")
						case "get_skill_summary":
							result = s.describeSkill(op, "summary")
						}
						template, err := templateFor([]byte(result.Content[0].Text))
						if err != nil {
							return nil, err
						}
						rendered, err := renderTemplate(&template, s, op)
						if err != nil || !bytes.Equal(rendered, []byte(result.Content[0].Text)) {
							return nil, fmt.Errorf("template mismatch for %s/%s/resources=%t/mask=%d/facade=%s", op, variant, resources, mask, facade)
						}
						templates[template.SHA] = template
						managed := len(s.trusted.serverManaged(op)) > 0
						key := fmt.Sprintf("%s/%s/%t/%t", op, variant, resources, managed)
						if prior, ok := selections[key]; ok && prior.Template != template.SHA {
							return nil, fmt.Errorf("unclassified dynamic schema value %s", key)
						}
						selections[key] = templateSelection{op, variant, resources, managed, truthHash, template.SHA}
						observations++
					}
				}
			}
		}
	}
	ordered := make([]schemaTemplate, 0, len(templates))
	for _, t := range templates {
		ordered = append(ordered, t)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].SHA < ordered[j].SHA })
	variants := make([]templateSelection, 0, len(selections))
	for _, s := range selections {
		variants = append(variants, s)
	}
	sort.Slice(variants, func(i, j int) bool {
		a, b := variants[i], variants[j]
		if a.OperationID != b.OperationID {
			return a.OperationID < b.OperationID
		}
		if a.Facade != b.Facade {
			return a.Facade < b.Facade
		}
		if a.Resources != b.Resources {
			return !a.Resources
		}
		return !a.Managed && b.Managed
	})
	// Discovery remains the source's fixed lowercase/Fields AND matcher and
	// ordered/truncated projection. Rows are finite; query is not a text slot.
	s.trusted = TrustedContext{}
	discovery := []map[string]any{}
	for _, resources := range []bool{false, true} {
		for _, facade := range []facadeMode{facadeThree, facadeTwo, facadeBoth} {
			s.facade = facade
			s.clientResources = resources
			for _, service := range append([]string{""}, s.reg.EnabledServices()...) {
				rows := []map[string]any{}
				ops := s.reg.ListOperations(service)
				if service == "" {
					ops = s.reg.EnabledOperations()
				}
				for _, op := range ops {
					if !mcpExposed(op.ID) {
						continue
					}
					if _, div := divergentMCPOps[op.ID]; div {
						continue
					}
					row := catalogJSON(opSearchResult{OperationInfo: op, Skill: s.navFor(op, false)})
					rows = append(rows, map[string]any{"operation_id": op.ID, "match_text": strings.ToLower(op.ID + " " + op.Summary + " " + op.Path), "row_base64": base64.StdEncoding.EncodeToString(row)})
				}
				discovery = append(discovery, map[string]any{"scope": service, "rows": rows, "resources_negotiated": resources, "server_facade": facade, "page_limit": searchOpsPageLimit, "query_limits": map[string]int{"bytes": maxSearchQueryBytes, "runes": maxSearchQueryRunes, "terms": maxSearchTerms}, "renderer": "discovery-search-render-v1"})
			}
		}
	}

	return json.Marshal(map[string]any{"format": "permission-content-materialization-v2", "status": "CONTENT_VARIANTS_MATERIALIZED_NOT_A_SUPPORTED_RELEASE", "catalog_version": index.CatalogVersion, "catalog_digest": nil, "parameter_truths": truths, "templates": ordered, "schema_variants": variants, "discovery_indices": discovery, "conformance": map[string]int{"operations": len(truths), "base_facade_variants": 1179, "served_configuration_observations": observations}, "source_entry_index_sha256": sha256Sum(catalogSourceIndex)})
}
