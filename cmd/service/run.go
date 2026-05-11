package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// runOperation is the RunE body for every auto-registered operation.
// Builds the outbound request from flags/args, dispatches (with pagination
// if requested), and emits the envelope.
func runOperation(cobraCmd *cobra.Command, f *cmdutil.Factory, rt *operationRuntime, args []string) error {
	ctx := cobraCmd.Context()
	d := rt.detail

	// Path substitution. cobra.ExactArgs already ensured count.
	urlPath := d.Path
	for i, pname := range rt.pathParams {
		placeholder := "{" + pname + "}"
		urlPath = strings.ReplaceAll(urlPath, placeholder, url.PathEscape(args[i]))
	}

	// Query parameters (only emit flags explicitly set by the user so defaults
	// don't override backend defaults).
	q := url.Values{}
	for flagName, qf := range rt.queryFlags {
		if !cobraCmd.Flags().Changed(flagName) {
			continue
		}
		switch qf.kind {
		case kindInt:
			q.Set(qf.apiName, strconv.Itoa(*qf.intVal))
		case kindBool:
			q.Set(qf.apiName, strconv.FormatBool(*qf.boolVal))
		case kindStringSlice:
			for _, v := range *qf.strSlc {
				q.Add(qf.apiName, v)
			}
		default:
			q.Set(qf.apiName, *qf.strVal)
		}
	}

	// Body: start from --data (if any), then merge explicit flags on top.
	// Multipart ops take a separate path — they build a form body, not JSON.
	req := client.Request{
		Service:        serviceForBaseURL(d.BaseURLEnv),
		Method:         d.Method,
		Path:           urlPath,
		Query:          q,
		BinaryResponse: d.BinaryResponse,
	}
	if d.Multipart {
		raw, ct, err := buildMultipartBody(cobraCmd, rt)
		if err != nil {
			_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
			return err
		}
		req.RawBody = raw
		req.ContentType = ct
	} else {
		body, err := resolveBody(f, cobraCmd, rt)
		if err != nil {
			_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
			return err
		}
		req.Body = body
	}

	// Pagination loop (--page-all). Only for operations declaring pagination.
	if rt.pageAll != nil && *rt.pageAll && !(f.Globals != nil && f.Globals.DryRun) {
		return runPaginated(ctx, f, rt, &req)
	}

	return emitOnce(ctx, f, &req)
}

// resolveBody constructs the JSON body. Empty when the op has neither --data
// nor any promoted fields. Explicit flags override --data fields.
func resolveBody(f *cmdutil.Factory, cobraCmd *cobra.Command, rt *operationRuntime) (any, error) {
	if rt.bodyData == nil && len(rt.bodyFlags) == 0 {
		return nil, nil
	}

	base := map[string]any{}

	if rt.bodyData != nil && *rt.bodyData != "" {
		raw, err := cmdutil.ParseInput(f, *rt.bodyData)
		if err != nil {
			return nil, output.ErrValidation(fmt.Sprintf("--data: %v", err), "pass inline JSON, @file, or @-")
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &base); err != nil {
				return nil, output.ErrValidation(fmt.Sprintf("--data is not a JSON object: %v", err), "expected a JSON object for this operation")
			}
		}
	}

	for flagName, bf := range rt.bodyFlags {
		if !cobraCmd.Flags().Changed(flagName) {
			continue
		}
		switch bf.kind {
		case kindInt:
			base[bf.apiName] = *bf.intVal
		case kindBool:
			base[bf.apiName] = *bf.boolVal
		case kindStringSlice:
			base[bf.apiName] = *bf.strSlc
		default:
			base[bf.apiName] = *bf.strVal
		}
	}

	if len(base) == 0 {
		return nil, nil
	}
	return base, nil
}

// emitOnce runs one request and emits the envelope. Returns the same error
// value so cobra sets a non-zero exit code.
func emitOnce(ctx context.Context, f *cmdutil.Factory, req *client.Request) error {
	cli, err := f.Client()
	if err != nil {
		_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
		return err
	}
	body, err := cli.Do(ctx, req)
	if err != nil {
		_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
		return err
	}
	return f.EmitSuccess(body)
}

// --- pagination ---

// runPaginated walks pages until has_more is false, --page-limit is hit, or
// the context is cancelled. The merged result is a flat array of all data
// items — the caller gets a single envelope with no _pagination block
// (architecture §4.4).
func runPaginated(ctx context.Context, f *cmdutil.Factory, rt *operationRuntime, firstReq *client.Request) error {
	cli, err := f.Client()
	if err != nil {
		_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
		return err
	}
	pag := rt.detail.Pagination
	cursorParam := pag.CursorParam
	if cursorParam == "" {
		cursorParam = "cursor"
	}
	limit := 10
	if rt.pageLimit != nil && *rt.pageLimit > 0 {
		limit = *rt.pageLimit
	}

	merged := make([]json.RawMessage, 0, 64)
	req := *firstReq
	for page := 0; page < limit; page++ {
		body, err := cli.Do(ctx, &req)
		if err != nil {
			_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
			return err
		}
		data, nextCursor, hasMore, perr := parsePage(body)
		if perr != nil {
			_ = f.EmitError(perr) //nolint:errcheck // best-effort emit before returning err
			return perr
		}
		merged = append(merged, data...)
		if !hasMore || nextCursor == "" {
			break
		}
		// Prepare next request. Clone Query so we don't mutate the previous.
		nextQ := url.Values{}
		for k, vs := range req.Query {
			nextQ[k] = append([]string(nil), vs...)
		}
		nextQ.Set(cursorParam, nextCursor)
		req = client.Request{
			Service: req.Service,
			Method:  req.Method,
			Path:    req.Path,
			Query:   nextQ,
			Body:    req.Body,
			Headers: req.Headers,
		}
	}

	out, err := json.Marshal(merged)
	if err != nil {
		return err
	}
	return f.EmitSuccess(out)
}

// parsePage extracts {data:[], pagination:{has_more, next_cursor}} from a
// backend response. Tolerant: missing fields → empty data, no more pages.
func parsePage(body []byte) (items []json.RawMessage, cursor string, hasMore bool, exitErr *output.ExitError) {
	if len(body) == 0 {
		return nil, "", false, nil
	}
	var page struct {
		Data       []json.RawMessage `json:"data"`
		Pagination struct {
			HasMore    bool   `json:"has_more"`
			NextCursor string `json:"next_cursor"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, "", false, output.ErrWithHint("internal", "PAGINATION_PARSE", err.Error(), "response did not match expected pagination shape")
	}
	return page.Data, page.Pagination.NextCursor, page.Pagination.HasMore, nil
}
