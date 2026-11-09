package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"
)

// TurnRelayHandle is the endpoint e4601041 worker ABI. It contains only a
// relay address and one-turn bearer, never Bot/control credentials or paths.
func TurnRelayHandle(endpoint, token string) (string, error) {
	b, err := json.Marshal(map[string]string{"endpoint": endpoint, "token": token})
	if err != nil {
		return "", err
	}
	handle := base64.RawURLEncoding.EncodeToString(b)
	if len(handle) > 512 {
		return "", errors.New("RELAY_HANDLE_UNAVAILABLE")
	}
	return handle, nil
}

// ServeTurnRelayHandle serves the pinned executable's --turn-relay entry point.
func ServeTurnRelayHandle(ctx context.Context, handle string, in io.Reader, out io.Writer) error {
	if len(handle) < 1 || len(handle) > 512 {
		return errors.New("RELAY_HANDLE_INVALID")
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(handle)
	if err != nil {
		return errors.New("RELAY_HANDLE_INVALID")
	}
	defer clear(b)
	return ServeTurnRelay(ctx, bytes.NewReader(b), in, out)
}

// ServeTurnRelay bridges worker stdio to one private host-issued HTTP turn
// channel. The inherited ticket contains no Bot/owner/control credential. The
// endpoint launcher owns FD, process, filesystem and network containment.
func ServeTurnRelay(ctx context.Context, ticket, in io.Reader, out io.Writer) error { //nolint:gocyclo // closed fixed-program branches retain explicit checks in security order
	raw, err := io.ReadAll(io.LimitReader(ticket, 4097))
	if err != nil || len(raw) > 4096 {
		return errors.New("RELAY_TICKET_INVALID")
	}
	fields, err := strictObject(raw, "endpoint", "token")
	if err != nil {
		return errors.New("RELAY_TICKET_INVALID")
	}
	var endpoint, token string
	if json.Unmarshal(fields["endpoint"], &endpoint) != nil || json.Unmarshal(fields["token"], &token) != nil || !validRelayToken(token) {
		return errors.New("RELAY_TICKET_INVALID")
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Host == "" || u.Scheme != "https" && (u.Scheme != "http" || u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") {
		return errors.New("RELAY_TICKET_INVALID")
	}
	client := &http.Client{Timeout: 120 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	reader := bufio.NewReaderSize(in, 64*1024)
	for {
		line, oversized, readErr := readStdioMessage(reader)
		if oversized {
			return errors.New("RELAY_REQUEST_TOO_LARGE")
		}
		line = bytes.TrimSpace(line)
		if len(line) > 0 {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(line))
			if err != nil {
				return errors.New("RELAY_UNAVAILABLE")
			}
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			resp, err := client.Do(req) //nolint:bodyclose // source response is fully acquired and closed under the existing request deadline
			if err != nil {
				return errors.New("RELAY_UNAVAILABLE")
			}
			// maxHTTPBodyBytes is the existing incoming-request bound. Ordinary
			// MCP responses have their own source semantics and may exceed it.
			data, readErr := io.ReadAll(resp.Body)
			closePermissionIO(resp.Body)
			if readErr != nil || resp.StatusCode != 200 && resp.StatusCode != 202 {
				return errors.New("RELAY_UNAVAILABLE")
			}
			if len(bytes.TrimSpace(data)) > 0 {
				if !json.Valid(data) {
					return errors.New("RELAY_RESPONSE_INVALID")
				}
				if _, err = out.Write(append(bytes.TrimSpace(data), '\n')); err != nil {
					return err
				}
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return readErr
		}
	}
}

func relayNegotiation(req *rpcRequest) error {
	if req.Method != "initialize" {
		return nil
	}
	params, err := strictObject(req.Params)
	if err != nil {
		return err
	}
	capabilities := map[string]json.RawMessage{}
	if raw, ok := params["capabilities"]; ok {
		capabilities, err = strictObject(raw)
		if err != nil {
			return err
		}
	}
	experimental := map[string]json.RawMessage{}
	if raw, ok := capabilities["experimental"]; ok {
		experimental, err = strictObject(raw)
		if err != nil {
			return err
		}
	}
	if raw, ok := experimental["octo_permission_binding"]; ok && !managedSelection(raw) {
		return errors.New("RELAY_NEGOTIATION_INVALID")
	}
	experimental["octo_permission_binding"] = json.RawMessage(`{"selected_binding":2,"selected_metadata":2,"selected_identity":2}`)
	capabilities["experimental"], err = json.Marshal(experimental)
	if err != nil {
		return err
	}
	params["capabilities"], err = json.Marshal(capabilities)
	if err != nil {
		return err
	}
	req.Params, err = json.Marshal(params)
	return err
}

func validRelayToken(token string) bool {
	return controlHash(token)
}
