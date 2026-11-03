package service

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"

	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

var marketplaceReleaseVersion = regexp.MustCompile(`^\d{1,9}\.\d{1,9}\.\d{1,9}$`)

// Draft operations never opt in. Connectors keep their existing wire contract;
// only the server can authoritatively identify a resource from its ID. Valid
// explicit labels take the ordinary single-write path. A failed type read must
// never fall back to guessing a type, choosing a label, or sending a write.
func validateMarketplaceReleaseVersion(ctx context.Context, f *cmdutil.Factory, rt *operationRuntime, req *client.Request) error {
	if !rt.detail.ValidateMarketplaceReleaseVersion || (f.Globals != nil && f.Globals.DryRun) {
		return nil
	}
	body, ok := req.Body.(map[string]any)
	if !ok {
		return nil // The normal schema validator handles malformed bodies.
	}
	version, _ := body["version"].(string)
	if marketplaceReleaseVersion.MatchString(strings.TrimSpace(version)) {
		return nil
	}
	id, _ := body["plugin_id"].(string)
	if strings.TrimSpace(id) == "" {
		return nil // Preserve the existing required-plugin-ID validation.
	}
	kind, err := marketplacePublicationType(ctx, f, req, id)
	if err != nil {
		return err
	}
	switch kind {
	case "connector":
		return nil
	case "skill", "expert", "expert_team":
		reason := "invalid"
		if strings.TrimSpace(version) == "" {
			reason = "required"
		}
		ee := output.ErrValidation("formal publication requires an explicit MAJOR.MINOR.PATCH version",
			"Pass --version <MAJOR.MINOR.PATCH> or top-level version in --data. Choose the formal release label when publishing; draft saves/imports may omit it. Upgrade octo-cli if your installed command cannot send version.")
		ee.Detail = json.RawMessage(`{"field":"version","reason":"` + reason + `"}`)
		return ee
	default:
		return output.ErrValidation("cannot determine the publication resource type", "Run marketplace plugin get for this ID and check the server/CLI API compatibility before retrying; no publication was sent.")
	}
}

func marketplacePublicationType(ctx context.Context, f *cmdutil.Factory, req *client.Request, id string) (string, error) {
	op, ok := f.Registry().GetOperation("plugin.get")
	if !ok {
		return "", output.ErrValidation("plugin detail operation is unavailable", "Upgrade octo-cli to a build with the Marketplace Plugin APIs; no publication was sent.")
	}
	cli, err := f.ClientForCredential(ctx, req.Credential)
	if err != nil {
		return "", err
	}
	raw, err := cli.Do(ctx, &client.Request{
		Service: req.Service, Credential: req.Credential, Method: op.Method,
		Path:    MarketplacePath(op.Service, op.Path),
		Query:   url.Values{"plugin_id": {id}, "include_relations": {"false"}},
		Headers: req.Headers, SuppressSpaceHeader: req.SuppressSpaceHeader,
	})
	if err != nil {
		return "", annotateMarketplaceVersionError(req.Service, err) // Never attempt the write.
	}
	var result struct {
		Data struct {
			Plugin struct {
				Type string `json:"plugin_type"`
			} `json:"plugin"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", output.ErrValidation("invalid plugin detail response", "Check the Marketplace server/CLI API compatibility; no publication was sent.")
	}
	return result.Data.Plugin.Type, nil
}

// The shared legacy parser replaces hints for known codes with generic advice.
// Keep actionable version/old-API hints on Marketplace errors without changing
// the code, taxonomy, status, detail payload or other services' hint contracts.
func annotateMarketplaceVersionError(service string, err error) error {
	if service != "marketplace" {
		return err
	}
	ee := output.AsExitError(err)
	if ee == nil {
		return err
	}
	var body struct {
		Error struct {
			Details struct {
				Field  string `json:"field"`
				Reason string `json:"reason"`
			} `json:"details"`
			Hint string `json:"hint"`
		} `json:"error"`
	}
	if json.Unmarshal(ee.Detail, &body) != nil || strings.TrimSpace(body.Error.Hint) == "" {
		return err
	}
	if body.Error.Details.Field != "version" && body.Error.Details.Reason != "versioned_api_required" {
		return err
	}
	annotated := *ee
	annotated.Hint = body.Error.Hint
	return &annotated
}
