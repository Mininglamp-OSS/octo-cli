package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Mininglamp-OSS/octo-cli/internal/permission"
)

func (s *Server) catalogPackage() *permission.VerifiedCatalog {
	if s.managed == nil {
		return nil
	}
	return s.managed.coordinator.CatalogPackage()
}
func (s *Server) catalogConfig() permission.CatalogConfig {
	mask := 0
	if s.trusted.SpaceID != "" {
		mask |= 1
	}
	if s.trusted.ChannelID != "" {
		mask |= 2
	}
	if s.trusted.ChannelType != "" {
		mask |= 4
	}
	facade := 0
	switch s.facade {
	case facadeTwo:
		facade = 1
	case facadeBoth:
		facade = 2
	}
	return permission.CatalogConfig{Resources: s.clientResources, ManagedMask: mask, Facade: facade}
}
func catalogReadError(err error) toolResult {
	return jsonToolResult(map[string]any{"error": map[string]string{"code": "PERMISSION_CATALOG_UNAVAILABLE", "message": "catalog content unavailable"}}, true)
}

func (s *Server) describeOpContext(ctx context.Context, op string) toolResult {
	if s.catalogPackage() == nil {
		return s.describeOp(op)
	}
	call := permission.BoundCall(ctx)
	if call == nil {
		return catalogReadError(permission.PolicyUnavailable())
	}
	raw, err := call.CatalogSchema(ctx, op, "describe_op", s.catalogConfig())
	if err != nil {
		return catalogReadError(err)
	}
	return rawToolResult(raw, false)
}
func (s *Server) describeSkillContext(ctx context.Context, op, depth string) toolResult {
	if s.catalogPackage() == nil || depth != "" && depth != "full" && depth != "summary" {
		return s.describeSkill(op, depth)
	}
	facade := "get_skill_full"
	if depth == "summary" {
		facade = "get_skill_summary"
	}
	call := permission.BoundCall(ctx)
	if call == nil {
		return catalogReadError(permission.PolicyUnavailable())
	}
	raw, err := call.CatalogSchema(ctx, op, facade, s.catalogConfig())
	if err != nil {
		return catalogReadError(err)
	}
	return rawToolResult(raw, false)
}

func (s *Server) catalogModuleMap(ctx context.Context) toolResult {
	call := permission.BoundCall(ctx)
	if call == nil {
		return catalogReadError(permission.PolicyUnavailable())
	}
	raw, err := call.CatalogStatic(ctx, "octo-cli/discovery/module-map", s.catalogConfig())
	if err != nil {
		return catalogReadError(err)
	}
	return rawToolResult(raw, false)
}
func (s *Server) catalogResourceList(ctx context.Context) (any, error) {
	call := permission.BoundCall(ctx)
	if call == nil {
		return nil, permission.PolicyUnavailable()
	}
	raw, err := call.CatalogStatic(ctx, "octo-cli/catalog/resource-list/skills", s.catalogConfig())
	if err != nil {
		return nil, err
	}
	return json.RawMessage(raw), nil
}
func (s *Server) readResourceContext(ctx context.Context, uri string) (map[string]any, error) {
	if s.catalogPackage() == nil {
		return s.readResource(uri)
	}
	uri, _, _ = strings.Cut(uri, "#")
	if !strings.HasPrefix(uri, skillURIScheme) {
		return nil, fmt.Errorf("unsupported resource uri")
	}
	entry := "octo-cli/skills/resource/" + strings.TrimPrefix(uri, skillURIScheme)
	call := permission.BoundCall(ctx)
	if call == nil {
		return nil, permission.PolicyUnavailable()
	}
	raw, err := call.CatalogStatic(ctx, entry, s.catalogConfig())
	if err != nil {
		return nil, err
	}
	return map[string]any{"contents": []resourceContent{{URI: uri, MimeType: "text/markdown", Text: string(raw)}}}, nil
}

func (s *Server) catalogSearch(ctx context.Context, domain, query, discoveryFacade string) toolResult {
	call := permission.BoundCall(ctx)
	if call == nil {
		return catalogReadError(permission.PolicyUnavailable())
	}
	rows, matches, err := call.CatalogDiscovery(ctx, domain, discoveryFacade, s.catalogConfig())
	if err != nil {
		return catalogReadError(err)
	}
	_, terms, queryTruncated := boundSearchQuery(query)
	out := []json.RawMessage{}
	truncated := queryTruncated
	for i, row := range rows {
		if ctx.Err() != nil {
			return jsonToolResult(map[string]any{"operations": []any{}, "error": map[string]any{"code": "SEARCH_CANCELLED", "message": ctx.Err().Error()}}, true)
		}
		match := true
		for _, term := range terms {
			if !strings.Contains(matches[i], term) {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		if len(out) >= searchOpsPageLimit {
			truncated = true
			break
		}
		out = append(out, json.RawMessage(row))
	}
	result := map[string]any{"operations": out}
	if truncated {
		note := ""
		if queryTruncated {
			note = fmt.Sprintf("query bounded to %d bytes / %d runes / %d terms; ", maxSearchQueryBytes, maxSearchQueryRunes, maxSearchTerms)
		}
		if len(out) >= searchOpsPageLimit {
			note += fmt.Sprintf("more than %d matches; narrow with a more specific query or a domain filter", searchOpsPageLimit)
		}
		result["truncated"] = true
		result["note"] = strings.TrimSpace(note)
	}
	return jsonToolResult(result, false)
}
