package output

import (
	"net/http"
	"strings"
	"testing"
)

func TestSheetCleaningErrorHints(test *testing.T) {
	for code, instruction := range map[string]string{
		"cleaning_overwrite_required":          "only after approval",
		"cleaning_filtered_sheet":              "another user's filter",
		"cleaning_complex_sheet":               "plain worksheet",
		"cleaning_formula_unsupported":         "as values",
		"cleaning_merged_range":                "unmerged",
		"cleaning_delimiter_not_found":         "stored values",
		"cleaning_single_column_required":      "one source column",
		"cleaning_range_out_of_bounds":         "sheetList",
		"cleaning_invalid_delimiter":           "16 characters",
		"sheet_conditional_format_unsupported": "never delete rules automatically",
	} {
		err := ParseBackendError(http.StatusUnprocessableEntity, []byte(`{"error":"`+code+`"}`))
		if err.Code != code || err.Type != "validation" || !strings.Contains(err.Hint, instruction) {
			test.Errorf("%s: %+v", code, err)
		}
	}
}

func TestConditionalFormatStructuralRefusalHint(test *testing.T) {
	err := ParseBackendError(http.StatusConflict, []byte(`{"error":"sheet_conditional_format_unsupported"}`))
	if err.Code != "sheet_conditional_format_unsupported" || err.Type != "validation" || !strings.Contains(err.Hint, "never delete rules automatically") {
		test.Fatalf("unsafe structural refusal hint: %+v", err)
	}
}
