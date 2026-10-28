package mcp

import (
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// exposureJSON is the MCP exposure classification: every registry operation
// the MCP facades may advertise, describe or execute. It is default-deny — an
// enabled registry operation that is not listed is withheld from discovery and
// refused by describe/execute, so a spec change cannot silently widen what a
// connected agent can reach. "baseline" holds the operations exposed before
// the XIN-2424 Bot API coverage work; "approved" holds each operation that work
// added or exposed, keyed to the approved inventory rows it covers (see
// docs/mcp-bot-api-coverage.json). "withheld" records inherited CLI operations
// without authorizing MCP exposure. TestExposure_* keeps the table, the registry
// and the coverage manifest in sync.
//
//go:embed exposure.json
var exposureJSON []byte

type exposureFile struct {
	Version  int                         `json:"version"`
	Baseline []string                    `json:"baseline"`
	Approved map[string][]string         `json:"approved"`
	Withheld map[string]withheldExposure `json:"withheld"`
}

type withheldExposure struct {
	Reason         string `json:"reason"`
	UpstreamCommit string `json:"upstream_commit"`
	UpstreamPR     string `json:"upstream_pr"`
}

// exposedOps is the parsed classification; a parse failure leaves it empty,
// which denies every operation rather than allowing them.
var exposedOps = loadExposure(exposureJSON)

func loadExposure(raw []byte) map[string]bool {
	var f exposureFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return map[string]bool{}
	}
	out := make(map[string]bool, len(f.Baseline)+len(f.Approved))
	for _, id := range f.Baseline {
		out[id] = true
	}
	for id := range f.Approved {
		out[id] = true
	}
	// A withheld entry is always denied, even if a malformed table also
	// lists it in an allowed class. The census rejects such overlaps.
	for id := range f.Withheld {
		delete(out, id)
	}
	return out
}

// mcpExposed reports whether operationID is classified for MCP exposure.
func mcpExposed(operationID string) bool { return exposedOps[operationID] }

// unclassifiedOpError reports an operation absent from the MCP table's allowed
// classes. The CLI may still reach it; MCP refuses until exposure is approved.
func unclassifiedOpError(operationID string) error {
	return output.ErrValidation(
		fmt.Sprintf("operation %q is not classified for MCP exposure", operationID),
		"the MCP server only executes operations listed in its exposure table; use the CLI or ask the operator to classify it")
}
