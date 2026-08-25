package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
)

type sceneSnapshot struct {
	Elements    []map[string]any `json:"elements"`
	Files       map[string]any   `json:"files"`
	AppState    map[string]any   `json:"appState"`
	BaseVersion string           `json:"baseVersion"`
}

func commandAt(root *cobra.Command, names ...string) *cobra.Command {
	current := root
	for _, name := range names {
		var next *cobra.Command
		for _, child := range current.Commands() {
			if child.Name() == name {
				next = child
				break
			}
		}
		if next == nil {
			return nil
		}
		current = next
	}
	return current
}

func getScene(cmd *cobra.Command, f *cmdutil.Factory, docID string) (*sceneSnapshot, error) {
	path, err := scenePath(docID)
	if err != nil {
		return nil, err
	}
	cfg, err := f.Config()
	if err != nil {
		return nil, err
	}
	cred, err := f.Credential()
	if err != nil {
		return nil, err
	}
	var options client.Options
	if f.Globals != nil {
		options.NoRetry = f.Globals.NoRetry
		options.Timeout = f.Globals.Timeout
		options.Verbose = f.Globals.Verbose
	}
	options.ErrOut = f.ErrOut()
	readClient := client.New(cfg, cred, options)
	raw, err := readClient.Do(cmd.Context(), &client.Request{
		Method:              http.MethodGet,
		Path:                path,
		SuppressSpaceHeader: true,
	})
	if err != nil {
		return nil, err
	}
	var snapshot sceneSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return nil, fmt.Errorf("decode scene: %w", err)
	}
	if snapshot.BaseVersion == "" {
		return nil, errors.New("scene response has no baseVersion")
	}
	if err := checkDuplicateLiveSceneIDs(snapshot.Elements); err != nil {
		return nil, err
	}
	if snapshot.Files == nil {
		snapshot.Files = map[string]any{}
	}
	if snapshot.AppState == nil {
		snapshot.AppState = map[string]any{}
	}
	return &snapshot, nil
}

func opaquePathSegment(value, name string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("%s must not be empty", name)
	}
	if value == "." || value == ".." {
		return "", fmt.Errorf("%s must not be a dot path segment", name)
	}
	return url.PathEscape(value), nil
}

func scenePath(docID string) (string, error) {
	segment, err := opaquePathSegment(docID, "docId")
	if err != nil {
		return "", err
	}
	return "/v1/bot/docs/" + segment + "/scene", nil
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func checkDuplicateLiveSceneIDs(elements []map[string]any) error {
	seen := make(map[string]bool, len(elements))
	for _, element := range elements {
		if element["isDeleted"] == true {
			continue
		}
		id, ok := element["id"].(string)
		if !ok || id == "" {
			return errors.New("scene contains a live element with an invalid id")
		}
		if seen[id] {
			return fmt.Errorf("scene contains duplicate live element id %q", id)
		}
		seen[id] = true
	}
	return nil
}
