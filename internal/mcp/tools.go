package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// searchOpsPageLimit caps a keyword/domain result page so the per-op skill
// navigation increment has a hard upper bound.
const searchOpsPageLimit = 20

// tool metadata for tools/list. The three inputSchemas together are < 1 KB;
// skill navigation lives in return values, never here, so tools/list stays
// resident-cheap.
func toolDefinitions() []toolDef {
	return []toolDef{
		{
			Name:        "search_ops",
			Description: "List callable Octo operations. Filter by domain and/or keyword. Returns operation ids + one-line summaries + skill navigation metadata (which business Skill to load), NOT full parameter schemas and NOT Skill bodies. Call with no arguments for a service<->skill module map.",
			InputSchema: rawSchema(`{"type":"object","properties":{"domain":{"type":"string","description":"service name, e.g. docs, message, loop"},"query":{"type":"string","description":"keyword to match against operation id / summary / path"}}}`),
		},
		{
			Name:        "describe_op",
			Description: "Return the full input schema for one operation id, plus the operation's Skill reference and pre-call advice. Call before call_op when unsure of arguments. Parameter truth lives here (zero-drift, from the embedded spec); the Skill carries semantics, not a second copy of the schema.",
			InputSchema: rawSchema(`{"type":"object","required":["operation_id"],"properties":{"operation_id":{"type":"string"}}}`),
		},
		{
			Name:        "call_op",
			Description: "Invoke one operation. arguments is a flat object keyed by the operation's declared params / body fields (see describe_op). path params, query params, and body fields all go in arguments by their wire name. Security-sensitive fields forced by the server connection are ignored if supplied.",
			InputSchema: rawSchema(`{"type":"object","required":["operation_id"],"properties":{"operation_id":{"type":"string"},"arguments":{"type":"object"}}}`),
		},
	}
}

type toolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

func rawSchema(s string) json.RawMessage { return json.RawMessage(s) }

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolResult struct {
	Content []toolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

func jsonToolResult(payload any, isErr bool) toolResult {
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		b = []byte(fmt.Sprintf("{\"ok\":false,\"error\":{\"type\":\"internal\",\"code\":\"MARSHAL\",\"message\":%q}}", err.Error()))
		isErr = true
	}
	return toolResult{Content: []toolContent{{Type: "text", Text: string(b)}}, IsError: isErr}
}

func rawToolResult(raw []byte, isErr bool) toolResult {
	return toolResult{Content: []toolContent{{Type: "text", Text: string(raw)}}, IsError: isErr}
}

// skillNav is the navigation block attached to search_ops / describe_op results.
type skillNav struct {
	Name         string `json:"name"`
	ResourceURI  string `json:"resource_uri"`
	Summary      string `json:"summary,omitempty"`
	Section      string `json:"section,omitempty"`
	SectionURI   string `json:"section_uri,omitempty"`
	ReferenceURI string `json:"reference_uri,omitempty"`
	Level        string `json:"level"`
	LoadBefore   bool   `json:"load_before_call,omitempty"`
	Advice       string `json:"advice,omitempty"`
	Etag         string `json:"etag,omitempty"`
	Hint         string `json:"hint,omitempty"`
}

func skillResourceURI(name string) string     { return "octo://skills/" + name + "/SKILL.md" }
func refResourceURI(name, file string) string { return "octo://skills/" + name + "/" + file }

// searchOps handles the search_ops tool.
func (s *Server) searchOps(domain, query string) toolResult {
	if domain == "" && query == "" {
		return jsonToolResult(s.moduleMap(), false)
	}
	if domain != "" && s.reg.ServiceDisabled(domain) {
		return jsonToolResult(map[string]any{
			"operations": []any{},
			"note":       fmt.Sprintf("service %q is not available", domain),
		}, false)
	}

	var candidates []registry.OperationInfo
	if domain != "" {
		for _, op := range s.reg.ListOperations(domain) {
			candidates = append(candidates, op)
		}
		if len(candidates) == 0 && s.reg.GetSpec(domain) == nil {
			return jsonToolResult(map[string]any{
				"operations": []any{},
				"note":       fmt.Sprintf("unknown domain %q; call search_ops with no arguments for the module map", domain),
			}, false)
		}
	} else {
		candidates = s.reg.EnabledOperations()
	}

	q := strings.ToLower(query)
	terms := strings.Fields(q)
	ops := make([]opSearchResult, 0, len(candidates))
	truncated := false
	for _, op := range candidates {
		if _, div := divergentMCPOps[op.ID]; div {
			continue // not advertised via MCP; generated schema is uncallable
		}
		if len(terms) > 0 && !matchOp(op, terms) {
			continue
		}
		if len(ops) >= searchOpsPageLimit {
			truncated = true
			break
		}
		ops = append(ops, opSearchResult{OperationInfo: op, Skill: s.navFor(op, false)})
	}

	res := map[string]any{"operations": ops}
	if truncated {
		res["truncated"] = true
		res["note"] = fmt.Sprintf("more than %d matches; narrow with a more specific query or a domain filter", searchOpsPageLimit)
	}
	return jsonToolResult(res, false)
}

type opSearchResult struct {
	registry.OperationInfo
	Skill *skillNav `json:"skill,omitempty"`
}

// matchOp reports whether every query term appears in the operation's id,
// summary, or path (case-insensitive AND match), so "send message" finds
// message.send.
func matchOp(op registry.OperationInfo, terms []string) bool {
	hay := strings.ToLower(op.ID + " " + op.Summary + " " + op.Path)
	for _, t := range terms {
		if !strings.Contains(hay, t) {
			return false
		}
	}
	return true
}

// moduleMap returns a service<->skill map for search_ops() with no arguments: a
// live map (never a static snapshot), disabled services excluded via
// EnabledServices.
func (s *Server) moduleMap() map[string]any {
	type svcEntry struct {
		Service        string    `json:"service"`
		OperationCount int       `json:"operation_count"`
		Skill          *skillNav `json:"skill,omitempty"`
		SampleOps      []string  `json:"sample_ops,omitempty"`
	}
	var entries []svcEntry
	for _, svc := range s.reg.EnabledServices() {
		ops := s.reg.ListOperations(svc)
		samples := make([]string, 0, 3)
		for i := 0; i < len(ops) && len(samples) < 3; i++ {
			if _, div := divergentMCPOps[ops[i].ID]; div {
				continue // don't sample an op that MCP does not advertise
			}
			samples = append(samples, ops[i].ID)
		}
		e := svcEntry{Service: svc, OperationCount: len(ops), SampleOps: samples}
		if name, ok := s.mapping.serviceToSkill[svc]; ok {
			if meta := s.mapping.skills[name]; meta != nil {
				e.Skill = &skillNav{
					Name:        name,
					ResourceURI: skillResourceURI(name),
					Summary:     meta.SummaryShort,
					Level:       levelRecommended,
					Etag:        meta.Etag,
				}
				s.applyResourceHint(e.Skill)
			}
		}
		entries = append(entries, e)
	}
	return map[string]any{"services": entries, "mapping_etag": s.mapping.mappingEtag}
}

// navFor builds the skill navigation block for one operation, or nil when no
// mapping covers it. forDescribe includes the per-op advice + section anchor.
func (s *Server) navFor(op registry.OperationInfo, forDescribe bool) *skillNav {
	meta, om, ok := s.mapping.SkillFor(op)
	if !ok {
		return nil
	}
	level := levelRecommended
	if om != nil && om.Level != "" {
		level = om.Level
	}
	nav := &skillNav{
		Name:        meta.Name,
		ResourceURI: skillResourceURI(meta.Name),
		Summary:     meta.SummaryShort,
		Level:       level,
		LoadBefore:  level == levelRequiredBeforeWrite,
		Etag:        meta.Etag,
	}
	// Reference / section: explicit manifest section wins; else topic match.
	if om != nil && om.Section != "" {
		file, _, _ := strings.Cut(om.Section, "#")
		nav.Section = om.Section
		nav.SectionURI = "octo://skills/" + meta.Name + "/" + strings.ReplaceAll(om.Section, "#", "#")
		if file != "" && file != "SKILL.md" {
			nav.ReferenceURI = refResourceURI(meta.Name, file)
		}
	} else if ref := matchReference(meta, op); ref != "" {
		nav.Section = ref
		nav.ReferenceURI = refResourceURI(meta.Name, ref)
	}
	if forDescribe && om != nil {
		nav.Advice = om.Advice
	}
	s.applyResourceHint(nav)
	return nav
}

// applyResourceHint adds the degradation hint when the connected client did not
// negotiate MCP resources support: the uri strings still return (plain
// identifiers, no side effect) with a readable fallback.
func (s *Server) applyResourceHint(nav *skillNav) {
	if s.clientResources {
		return
	}
	nav.Hint = "client has no MCP resources support; run `octo-cli skills " + nav.Name + "` if a shell is available, or proceed with describe_op alone"
}

// matchReference picks the reference file whose registered topics best match
// the operation's id/summary tokens. A miss returns "" (SKILL.md level). This
// is a navigation heuristic only — parameter truth is always in describe_op.
func matchReference(meta *skillMeta, op registry.OperationInfo) string {
	if len(meta.References) == 0 {
		return ""
	}
	hay := strings.ToLower(op.ID + " " + op.Summary + " " + op.Path)
	best, bestScore := "", 0
	files := make([]string, 0, len(meta.References))
	for f := range meta.References {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		score := 0
		for _, topic := range meta.References[f] {
			if strings.Contains(hay, strings.ToLower(topic)) {
				score++
			}
		}
		if score > bestScore {
			best, bestScore = f, score
		}
	}
	return best
}

// describeOp handles the describe_op tool.
func (s *Server) describeOp(operationID string) toolResult {
	detail, ok := s.reg.GetOperation(operationID)
	if !ok {
		return jsonToolResult(unknownOperationPayload(s.reg, operationID), true)
	}
	// Disabled services must not be describable: GetOperation resolves them
	// for introspection, but the MCP surface withholds them like search/call.
	if s.reg.ServiceDisabled(detail.Service) {
		return jsonToolResult(unknownOperationPayload(s.reg, operationID), true)
	}
	// A divergent op's generated schema does not describe its callable shape, so
	// do not advertise it; point at the CLI instead.
	if cli, div := divergentMCPOps[operationID]; div {
		return rawToolResult(synthErrorEnvelope(divergentOpError(operationID, cli)), true)
	}
	// Marshal the OperationDetail (zero-drift parameter truth) and splice the
	// skill block + server-managed markers into the same object.
	raw, err := json.Marshal(detail)
	if err != nil {
		return jsonToolResult(map[string]any{"ok": false, "error": err.Error()}, true)
	}
	var obj map[string]any
	_ = json.Unmarshal(raw, &obj)
	if nav := s.navFor(detail.OperationInfo, true); nav != nil {
		obj["skill"] = nav
	}
	if managed := s.trusted.serverManaged(operationID); len(managed) > 0 {
		names := make([]string, 0, len(managed))
		for k := range managed {
			names = append(names, k)
		}
		sort.Strings(names)
		obj["server_managed_arguments"] = names
		obj["server_managed_note"] = "these arguments are forced by the server connection (over-privilege protection); do not supply them"
	}
	return jsonToolResult(obj, false)
}

// callOp handles the call_op tool.
func (s *Server) callOp(ctx context.Context, operationID string, arguments map[string]any) toolResult {
	detail, ok := s.reg.GetOperation(operationID)
	if !ok {
		return jsonToolResult(unknownOperationPayload(s.reg, operationID), true)
	}
	if s.reg.ServiceDisabled(detail.Service) {
		return rawToolResult(synthErrorEnvelope(disabledOpError(operationID)), true)
	}
	if cli, div := divergentMCPOps[operationID]; div {
		return rawToolResult(synthErrorEnvelope(divergentOpError(operationID, cli)), true)
	}
	if arguments == nil {
		arguments = map[string]any{}
	}
	// Over-privilege protection: force the connection's trusted values over
	// whatever the model supplied, before assembly. The returned set is the
	// audit trail surfaced in the envelope below.
	forced := s.trusted.apply(operationID, arguments)

	fn := s.factoryFn
	if fn == nil {
		fn = s.makeFactory
	}
	pol := execPolicy{httpMode: s.httpMode, uploadRoot: s.uploadRoot}
	f, outBuf, errBuf := fn(s.trusted)
	env, okRun := executeOperation(ctx, s.build, f, detail, arguments, pol, outBuf, errBuf)
	env = spliceForcedArguments(env, forced)
	return rawToolResult(env, !okRun)
}

// spliceForcedArguments records which arguments the connection forced on this
// call by adding an underscore-prefixed `_forced_arguments` member to the
// envelope (the same "_"-prefix convention the CLI uses for _pagination /
// _rate_limit). It is a per-call audit trail — describe_op's
// server_managed_arguments is static; this reflects what actually happened.
// A non-object or unparseable envelope is returned unchanged.
func spliceForcedArguments(env []byte, forced map[string]bool) []byte {
	if len(forced) == 0 {
		return env
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(env, &obj); err != nil {
		return env
	}
	names := make([]string, 0, len(forced))
	for k := range forced {
		names = append(names, k)
	}
	sort.Strings(names)
	raw, err := json.Marshal(names)
	if err != nil {
		return env
	}
	obj["_forced_arguments"] = raw
	out, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return env
	}
	return append(out, '\n')
}

// unknownOperationPayload closes the failure loop: it returns
// near-miss candidate ids and points at search_ops, instead of a bare error.
func unknownOperationPayload(reg *registry.Registry, operationID string) map[string]any {
	return map[string]any{
		"ok": false,
		"error": map[string]any{
			"type":    "validation",
			"code":    "UNKNOWN_OPERATION",
			"message": fmt.Sprintf("unknown operation %q", operationID),
			"hint":    "call search_ops to discover operation ids",
		},
		"candidates": nearestOperations(reg, operationID),
	}
}

// nearestOperations returns up to five enabled operation ids closest to the
// query, ranked by edit distance (with a substring bonus) so a small typo like
// "message.snd" surfaces "message.send" ahead of longer prefix-sharing ids.
func nearestOperations(reg *registry.Registry, query string) []string {
	type scored struct {
		id   string
		dist int
	}
	q := strings.ToLower(query)
	var all []scored
	for _, op := range reg.EnabledOperations() {
		id := strings.ToLower(op.ID)
		d := levenshtein(id, q)
		if strings.Contains(id, q) || strings.Contains(q, id) {
			d -= 5 // strong bonus for containment
		}
		all = append(all, scored{op.ID, d})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].dist != all[j].dist {
			return all[i].dist < all[j].dist
		}
		return all[i].id < all[j].id
	})
	out := make([]string, 0, 5)
	for i := 0; i < len(all) && i < 5; i++ {
		out = append(out, all[i].id)
	}
	return out
}

// levenshtein is the classic edit distance, used only to rank near-miss
// operation ids for the failure-mode closure.
func levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		cur := make([]int, lb+1)
		cur[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[lb]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}
