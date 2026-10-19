package service

import (
	"encoding/json"
	"net/url"
	"strings"

	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

func validateSheetReadSelection(rt *operationRuntime, query url.Values) *output.ExitError {
	if rt == nil || rt.detail == nil || rt.detail.ID != "docs.sheet.get" {
		return nil
	}
	if query.Has("sheetId") && query.Has("sheetName") {
		return output.ErrValidation("--sheet-id and --sheet-name are mutually exclusive", "use docs sheet list to discover worksheet IDs and names")
	}
	for _, key := range []string{"sheetId", "sheetName"} {
		if query.Has(key) && strings.TrimSpace(query.Get(key)) == "" {
			return output.ErrValidation("worksheet selector cannot be empty", "use docs sheet list to choose a worksheet")
		}
	}
	return nil
}

func validateSheetReadScope(body []byte, rt *operationRuntime, req *client.Request) *output.ExitError {
	if rt == nil || rt.detail == nil || rt.detail.ID != "docs.sheet.get" || (!req.Query.Has("sheetId") && !req.Query.Has("sheetName")) {
		return nil
	}
	var payload struct {
		SheetID string                     `json:"sheetId"`
		Cells   map[string]json.RawMessage `json:"sheetCells"`
	}
	if json.Unmarshal(body, &payload) != nil || payload.SheetID == "" || payload.Cells == nil {
		return output.ErrAPI("SHEET_READ_SCOPE_UNAVAILABLE", "the backend did not confirm a worksheet-scoped read", "deploy the worksheet-scoped read backend; do not treat a whole-workbook response as the selected worksheet")
	}
	if expected := req.Query.Get("sheetId"); expected != "" && payload.SheetID != expected {
		return output.ErrAPI("SHEET_READ_SCOPE_MISMATCH", "the backend returned a different worksheet", "stop reading and verify backend routing and worksheet selection")
	}
	for key := range payload.Cells {
		if !strings.HasPrefix(key, payload.SheetID+"!") {
			return output.ErrAPI("SHEET_READ_SCOPE_MISMATCH", "the backend returned cells outside the selected worksheet", "stop reading and verify backend worksheet filtering")
		}
	}
	return nil
}
