package service

import (
	"encoding/json"
	"io"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/output"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// Reuse the generated operations so aliases retain workspace scope, validation,
// risk handling and public API routing.
func attachLoopProjectAliases(loop *cobra.Command) {
	project := findChild(loop, "project")
	if project == nil {
		return
	}
	resources := findChild(project, "resource")
	if resources == nil {
		return
	}
	for _, pair := range [][2]string{{"create", "add"}, {"delete", "remove"}} {
		if command := findChild(resources, pair[0]); command != nil {
			command.Aliases = []string{pair[1]}
		}
	}
}

func registerLoopProjectFlags(cmd *cobra.Command, d *registry.OperationDetail) {
	if d.Service != "loop" {
		return
	}
	switch d.ID {
	case "task.create", "task.update", "task.quick_create", "autopilot.create", "autopilot.update", "task.list", "task.group":
		description := "Loop project UUID within --workspace-id; alias for --project-id (not a workspace selector)"
		if d.ID == "task.update" || d.ID == "autopilot.update" {
			description += "; an empty value clears the association"
		}
		cmd.Flags().String("project", "", description)
	case "project.resource.create", "project.resource.update":
		cmd.Flags().String("ref", "", "Complete resource_ref JSON object; do not combine with resource_ref in --data")
	}
}

func applyLoopProjectQuery(cmd *cobra.Command, rt *operationRuntime, query url.Values) *output.ExitError {
	if rt.detail.Service != "loop" || (rt.detail.ID != "task.list" && rt.detail.ID != "task.group") {
		return nil
	}
	if cmd.Flags().Changed("project") {
		if query.Has("project_id") || query.Has("project_ids") {
			return output.ErrValidation("--project cannot be combined with --project-id or --project-ids", "use one project selector")
		}
		query.Set("project_id", cmd.Flags().Lookup("project").Value.String())
	}
	if query.Has("project_id") && query.Has("project_ids") {
		return output.ErrValidation("--project-id and --project-ids are mutually exclusive", "use --project-ids for multiple projects")
	}
	for _, key := range []string{"project_id", "project_ids"} {
		if query.Has(key) && strings.TrimSpace(query.Get(key)) == "" {
			return output.ErrValidation("project selector is empty", "use --include-no-project to select tasks without a project")
		}
	}
	return nil
}

func applyLoopProjectBody(cmd *cobra.Command, rt *operationRuntime, body map[string]any) error {
	if rt.detail.Service != "loop" {
		return nil
	}
	switch rt.detail.ID {
	case "task.create", "task.update", "task.quick_create", "autopilot.create", "autopilot.update":
		return applyProjectAssociation(cmd, rt.detail.ID, body)
	case "project.create", "project.update":
		_, hasID := body["lead_id"]
		_, hasUID := body["lead_octo_uid"]
		if hasID && hasUID {
			return output.ErrValidation("lead_id and lead_octo_uid are mutually exclusive", "use one lead identity field")
		}
	case "project.resource.create", "project.resource.update":
		return applyProjectResourceRef(cmd, body)
	}
	return nil
}

func applyProjectAssociation(cmd *cobra.Command, operation string, body map[string]any) error {
	if !cmd.Flags().Changed("project") {
		return nil
	}
	if _, exists := body["project_id"]; exists {
		return output.ErrValidation("--project cannot be combined with project_id", "use --project, --project-id or project_id in --data")
	}
	value := cmd.Flags().Lookup("project").Value.String()
	if value != "" {
		body["project_id"] = value
		return nil
	}
	if operation != "task.update" && operation != "autopilot.update" {
		return output.ErrValidation("--project is empty", "omit --project when creating without a project")
	}
	body["project_id"] = nil
	return nil
}

func applyProjectResourceRef(cmd *cobra.Command, body map[string]any) error {
	if !cmd.Flags().Changed("ref") {
		return nil
	}
	if _, exists := body["resource_ref"]; exists {
		return output.ErrValidation("--ref cannot be combined with resource_ref in --data", "use one resource reference input")
	}
	var ref map[string]any
	decoder := json.NewDecoder(strings.NewReader(cmd.Flags().Lookup("ref").Value.String()))
	decoder.UseNumber()
	if err := decoder.Decode(&ref); err != nil || ref == nil {
		return output.ErrValidation("--ref must be a JSON object", "use --ref '{\"url\":\"https://github.com/org/repo\"}' or --data")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return output.ErrValidation("--ref must contain one JSON object", "remove trailing JSON or text")
	}
	body["resource_ref"] = ref
	return nil
}
