package service

import (
	"strings"

	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

func annotateSheetCleaningError(rt *operationRuntime, req *client.Request, err error) error {
	if rt.detail.ID != "docs.sheet.split" && rt.detail.ID != "docs.sheet.deduplicate" {
		return err
	}
	exit := output.AsExitError(err)
	if exit == nil || exit.HTTPStatus != 404 || exit.Code != "NOT_FOUND" || len(exit.Detail) != 0 {
		return err
	}
	annotated := *exit
	annotated.Hint = "verify the document with docs sheet get at the same API origin; if it is readable, check backend deployment and routing for sheet cleaning; never emulate cleaning with a whole-sheet rewrite"
	if strings.Contains(exit.Message, "Cannot "+req.Method+" "+req.Path) {
		annotated.Code = "SHEET_CLEANING_UNAVAILABLE"
		annotated.Message = "the sheet cleaning endpoint is unavailable at this API origin"
	}
	return &annotated
}
