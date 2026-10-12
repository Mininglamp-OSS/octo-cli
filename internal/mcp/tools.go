package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// searchOpsPageLimit caps a keyword/domain result page so the per-op skill
// navigation increment has a hard upper bound.
const searchOpsPageLimit = 20

// search_ops is bearer-free, so its query is attacker-controlled input on both
// transports. The scan cost is terms × operations, so the query is bounded
// BEFORE tokenising and the scan checks ctx, or a single unauthenticated POST
// can pin a core (the per-request HTTP timeout cannot interrupt a loop that
// never consults it).
const (
	// maxSearchQueryRunes caps the query after truncation (a real query is a few
	// words; 512 runes is generous). Deterministic: the prefix is kept.
	maxSearchQueryRunes = 512
	// maxSearchQueryBytes caps the query BEFORE any rune decode, so a
	// multi-megabyte multi-byte blob is dropped without scanning it.
	maxSearchQueryBytes = 8 * 1024
	// maxSearchTerms caps tokenised terms; excess terms are dropped (a query
	// that broad is already meaningless — every term must match).
	maxSearchTerms = 16
)

// boundSearchQuery applies the search_ops query bounds deterministically: an
// oversized input keeps its leading bytes/runes, an excessive term count keeps
// its leading terms. returnedTruncated tells the caller to annotate the result.
func boundSearchQuery(query string) (q string, terms []string, truncated bool) {
	if len(query) > maxSearchQueryBytes {
		end := maxSearchQueryBytes
		for end > 0 && !utf8.RuneStart(query[end]) {
			end--
		}
		query = query[:end]
		truncated = true
	}
	if r := []rune(query); len(r) > maxSearchQueryRunes {
		query = string(r[:maxSearchQueryRunes])
		truncated = true
	}
	q = strings.ToLower(query)
	terms = strings.Fields(q)
	if len(terms) > maxSearchTerms {
		terms = terms[:maxSearchTerms]
		truncated = true
	}
	return q, terms, truncated
}

// tool metadata for tools/list. The three inputSchemas together are < 1 KB;
// skill navigation lives in return values, never here, so tools/list stays
// resident-cheap.
func toolDefinitions() []toolDef {
	return []toolDef{
		{
			Name:        "search_ops",
			Description: "List callable Octo operations. Filter by domain and/or keyword. Returns operation ids + one-line summaries + skill navigation metadata (which business Skill to load), NOT full parameter schemas and NOT Skill bodies. Call with no arguments for a service<->skill module map.",
			InputSchema: rawSchema(`{"type":"object","properties":{"domain":{"type":"string","description":"service name, e.g. docs, message, loop"},"query":{"type":"string","description":"keyword AND match against operation id / summary / path; keeps first 8192 bytes (whole UTF-8 characters), 512 runes and 16 terms; result reports truncation"}}}`),
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

// searchOps handles the search_ops tool. The query is bounded before
// tokenising and the scan consults ctx (HTTP request timeout / client
// cancellation), because search_ops is reachable without a bearer and the scan
// cost is terms × operations.
func (s *Server) searchOps(ctx context.Context, domain, query string) toolResult { //nolint:gocyclo // bounded discovery handles domain filtering, cancellation and truncation
	cancelled := func(err error) toolResult {
		return jsonToolResult(map[string]any{
			"operations": []any{},
			"error":      map[string]any{"code": "SEARCH_CANCELLED", "message": err.Error()},
		}, true)
	}
	if err := ctx.Err(); err != nil {
		return cancelled(err)
	}
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
		candidates = s.reg.ListOperations(domain)
		if len(candidates) == 0 && s.reg.GetSpec(domain) == nil {
			return jsonToolResult(map[string]any{
				"operations": []any{},
				"note":       fmt.Sprintf("unknown domain %q; call search_ops with no arguments for the module map", domain),
			}, false)
		}
	} else {
		candidates = s.reg.EnabledOperations()
	}

	_, terms, queryTruncated := boundSearchQuery(query)
	ops := make([]opSearchResult, 0, len(candidates))
	truncated := queryTruncated
	for _, op := range candidates {
		if err := ctx.Err(); err != nil {
			return cancelled(err)
		}
		if _, div := divergentMCPOps[op.ID]; div {
			continue // not advertised via MCP; generated schema is uncallable
		}
		if len(terms) > 0 {
			matched, err := matchOp(ctx, op, terms)
			if err != nil {
				return cancelled(err)
			}
			if !matched {
				continue
			}
		}
		if len(ops) >= searchOpsPageLimit {
			truncated = true
			break
		}
		ops = append(ops, opSearchResult{OperationInfo: op, Skill: s.navFor(op, false)})
	}

	if err := ctx.Err(); err != nil {
		return cancelled(err)
	}
	res := map[string]any{"operations": ops}
	if truncated {
		note := ""
		if queryTruncated {
			note = fmt.Sprintf("query bounded to %d bytes / %d runes / %d terms; ", maxSearchQueryBytes, maxSearchQueryRunes, maxSearchTerms)
		}
		if len(ops) >= searchOpsPageLimit {
			note += fmt.Sprintf("more than %d matches; narrow with a more specific query or a domain filter", searchOpsPageLimit)
		}
		res["truncated"] = true
		res["note"] = strings.TrimSpace(note)
	}
	return jsonToolResult(res, false)
}

type opSearchResult struct {
	registry.OperationInfo
	Skill *skillNav `json:"skill,omitempty"`
}

// matchOp reports whether every query term appears in the operation's id,
// summary, or path (case-insensitive AND match), so "send message" finds
// message.send. It checks ctx between terms so a cancelled request stops
// promptly; cancellation is distinct from a non-match, including the last op.
func matchOp(ctx context.Context, op registry.OperationInfo, terms []string) (bool, error) { //nolint:gocritic // read-only operation metadata snapshot
	hay := strings.ToLower(op.ID + " " + op.Summary + " " + op.Path)
	for _, t := range terms {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if !strings.Contains(hay, t) {
			return false, nil
		}
	}
	return true, ctx.Err()
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
		advertised := 0
		for i := range ops {
			if _, div := divergentMCPOps[ops[i].ID]; div {
				continue // withheld from MCP: exclude from both the count and the samples
			}
			advertised++
			if len(samples) < 3 {
				samples = append(samples, ops[i].ID)
			}
		}
		e := svcEntry{Service: svc, OperationCount: advertised, SampleOps: samples}
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
func (s *Server) navFor(op registry.OperationInfo, forDescribe bool) *skillNav { //nolint:gocritic // read-only operation metadata snapshot
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
		nav.SectionURI = "octo://skills/" + meta.Name + "/" + om.Section
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
func matchReference(meta *skillMeta, op registry.OperationInfo) string { //nolint:gocritic // read-only operation metadata snapshot
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
func (s *Server) describeOp(ctx context.Context, operationID string) toolResult {
	detail, ok := s.reg.GetOperation(operationID)
	if !ok {
		return unknownOperationResult(ctx, s.reg, operationID)
	}
	// Disabled services must not be describable: GetOperation resolves them
	// for introspection, but the MCP surface withholds them like search/call.
	if s.reg.ServiceDisabled(detail.Service) {
		return unknownOperationResult(ctx, s.reg, operationID)
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
	_ = json.Unmarshal(raw, &obj) //nolint:errcheck // raw was just marshaled from an operation object
	if nav := s.navFor(detail.OperationInfo, true); nav != nil {
		obj["skill"] = nav
	}
	managed := s.trusted.serverManaged(operationID)
	note := "these arguments are forced by the server connection (over-privilege protection); do not supply them"
	if binary := multipartBinaryProperties(detail); len(binary) > 0 {
		spliceUploadArgument(obj, detail, binary, s.execPolicy())
		for _, b := range binary {
			managed[b] = true
		}
		note = "these arguments are set by the server (connection-forced scope values, or the multipart payload read from file_path); do not supply them"
	}
	if len(managed) > 0 {
		names := make([]string, 0, len(managed))
		for k := range managed {
			names = append(names, k)
		}
		sort.Strings(names)
		obj["server_managed_arguments"] = names
		obj["server_managed_note"] = note
	}
	return jsonToolResult(obj, false)
}

// spliceUploadArgument rewrites a multipart operation's describe_op object to
// its callable shape: the binary body property maps to the reserved --file
// flag and is always refused by call_op, so it is marked server-managed and
// dropped from request_body.required, and the MCP-only file_path argument that
// actually carries the upload is advertised under mcp_arguments.
func spliceUploadArgument(obj map[string]any, d *registry.OperationDetail, binary []string, pol execPolicy) {
	if rb, ok := obj["request_body"].(map[string]any); ok {
		if props, ok := rb["properties"].(map[string]any); ok {
			for _, b := range binary {
				if prop, ok := props[b].(map[string]any); ok {
					prop["server_managed"] = true
					prop["description"] = "multipart payload; not settable — supply " + uploadArgument + " instead"
				}
			}
		}
		if req, ok := rb["required"].([]any); ok {
			kept := make([]any, 0, len(req))
			for _, r := range req {
				if name, _ := r.(string); !slices.Contains(binary, name) {
					kept = append(kept, r)
				}
			}
			if len(kept) == 0 {
				delete(rb, "required")
			} else {
				rb["required"] = kept
			}
		}
	}
	obj["mcp_arguments"] = map[string]any{uploadArgument: uploadArgumentSchema(d, pol)}
}

// execPolicy is the connection's upload/transport posture, shared by
// describe_op (advertised file_path semantics) and call_op (enforcement).
func (s *Server) execPolicy() execPolicy {
	return execPolicy{httpMode: s.httpMode, uploadRoot: s.uploadRoot, allowLocalUpload: s.allowLocalUpload}
}

// callOp handles the call_op tool.
func (s *Server) callOp(ctx context.Context, operationID string, arguments map[string]any) toolResult {
	detail, ok := s.reg.GetOperation(operationID)
	if !ok {
		return unknownOperationResult(ctx, s.reg, operationID)
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
	f, outBuf, errBuf := fn(s.trusted)
	env, okRun := executeOperation(ctx, s.build, f, detail, arguments, s.execPolicy(), outBuf, errBuf)
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

// opLookupCancelled is the structured result when the request context ends
// while an unknown operation_id is being ranked. No candidates are returned: a
// partial ranking would be misleading.
func opLookupCancelled(err error) toolResult {
	return jsonToolResult(map[string]any{
		"ok": false,
		"error": map[string]any{
			"type":    "cancelled",
			"code":    "REQUEST_CANCELLED",
			"message": err.Error(),
		},
	}, true)
}

// unknownOperationResult renders the unknown-operation failure for describe_op /
// call_op, or the cancellation result if ctx ends during ranking.
func unknownOperationResult(ctx context.Context, reg *registry.Registry, operationID string) toolResult {
	payload, err := unknownOperationPayload(ctx, reg, operationID)
	if err != nil {
		return opLookupCancelled(err)
	}
	return jsonToolResult(payload, true)
}

// unknownOperationPayload closes the failure loop: it returns
// near-miss candidate ids and points at search_ops, instead of a bare error.
// operation_id is attacker-controlled on the bearer-free HTTP surface, so it is
// byte-bounded once, before any lowercase or rune decode.
func unknownOperationPayload(ctx context.Context, reg *registry.Registry, operationID string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bounded, _ := boundOperationID(operationID)
	candidates, err := nearestOperations(ctx, reg, bounded)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"ok": false,
		"error": map[string]any{
			"type":    "validation",
			"code":    "UNKNOWN_OPERATION",
			"message": fmt.Sprintf("unknown operation %q", truncateRunes(operationID, maxEchoedOperationIDRunes)),
			"hint":    "call search_ops to discover operation ids",
		},
		"candidates": candidates,
	}, nil
}

const (
	// maxOperationIDBytes caps an unknown operation_id BEFORE any allocation
	// proportional to the input (lowercase, []rune). It covers the echo and
	// ranking caps below even for 4-byte runes.
	maxOperationIDBytes = 1024
	// maxEchoedOperationIDRunes caps the id reflected in the error message.
	maxEchoedOperationIDRunes = 200
	// maxRankQueryRunes bounds the operation_id fed into the edit-distance
	// ranking (a real operation_id is far shorter).
	maxRankQueryRunes = 128
)

// boundOperationID keeps at most maxOperationIDBytes leading bytes of s, cut on
// a UTF-8 rune boundary, mirroring boundSearchQuery. It slices without copying,
// so every later lowercase / []rune conversion is proportional to the bound,
// not to the request.
func boundOperationID(s string) (string, bool) {
	if len(s) <= maxOperationIDBytes {
		return s, false
	}
	end := maxOperationIDBytes
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end], true
}

// truncateRunes caps a possibly-attacker-sized string before it is echoed back
// in an error message, so a multi-megabyte operation_id is not reflected whole.
// The byte bound runs first, so the rune decode only ever sees a bounded prefix.
func truncateRunes(s string, n int) string {
	s, cut := boundOperationID(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	if cut {
		return s + "…"
	}
	return s
}

// nearestOperations returns up to five enabled operation ids closest to the
// query, ranked by edit distance (with a substring bonus) so a small typo like
// "message.snd" surfaces "message.send" ahead of longer prefix-sharing ids.
// describe_op / call_op are reachable without a bearer, so the query is bounded
// to maxRankQueryRunes before it is lowercased and ranking checks ctx per
// operation; a cancelled ctx returns its error and no partial ranking.
func nearestOperations(ctx context.Context, reg *registry.Registry, query string) ([]string, error) {
	type scored struct {
		id   string
		dist int
	}
	query, _ = boundOperationID(query)
	if r := []rune(query); len(r) > maxRankQueryRunes {
		query = string(r[:maxRankQueryRunes])
	}
	q := strings.ToLower(query)
	ops := reg.EnabledOperations()
	all := make([]scored, 0, len(ops))
	for _, op := range ops {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		id := strings.ToLower(op.ID)
		d := levenshtein(id, q)
		if strings.Contains(id, q) || strings.Contains(q, id) {
			d -= 5 // strong bonus for containment
		}
		all = append(all, scored{op.ID, d})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
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
	return out, nil
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
	// Two rows, swapped per iteration, so ranking allocates O(lb) per
	// operation rather than O(la*lb).
	prev, cur := make([]int, lb+1), make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		cur[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
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
