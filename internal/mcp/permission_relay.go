package mcp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/permission"
)

// TurnRelayTrust carries verified private-CA, leaf-certificate digest, and SAN
// server-name constraints for private turn-relay and HTTP-worker TLS handoff.
// It never disables TLS certificate or hostname/IP SAN verification.
type TurnRelayTrust struct {
	CAFile           string `json:"ca_file,omitempty"`
	CAPEM            string `json:"ca_pem,omitempty"`
	CASHA256         string `json:"ca_sha256,omitempty"`
	PeerSHA256       string `json:"peer_sha256,omitempty"`
	CertSHA256       string `json:"cert_sha256,omitempty"`
	ServerName       string `json:"server_name,omitempty"`
	MaxResponseBytes int64  `json:"max_response_bytes,omitempty"`
}

func validRelayServerName(s string) bool {
	if s == "" || len(s) > 253 || strings.ContainsAny(s, "/:\\\r\n\t ") {
		return false
	}
	for _, ch := range s {
		if ch < 33 || ch > 126 {
			return false
		}
	}
	return true
}

func relayEndpointHostMatchesServerName(endpoint, serverName string) bool {
	u, err := url.Parse(endpoint)
	return err == nil && u.Hostname() != "" && strings.EqualFold(u.Hostname(), serverName)
}

// TurnRelayHandle is the endpoint e4601041 worker ABI. It contains only a
// relay address and one-turn bearer, never Bot/control credentials or paths.
func TurnRelayHandle(endpoint, token string) (string, error) {
	return TurnRelayHandleWithTrust(endpoint, token, TurnRelayTrust{})
}

// TurnRelayHandleWithTrust binds optional verified private-CA/SAN/cert-digest
// pins and response acquisition budgets into the one-turn stdio relay handle
// within the 512-char handle budget.
func TurnRelayHandleWithTrust(endpoint, token string, trust TurnRelayTrust) (string, error) {
	if trust.CAFile != "" && (!filepath.IsAbs(trust.CAFile) || strings.ContainsAny(trust.CAFile, "\r\n\x00")) {
		return "", errors.New("RELAY_HANDLE_UNAVAILABLE")
	}
	if trust.CASHA256 != "" && !controlHash(trust.CASHA256) {
		return "", errors.New("RELAY_HANDLE_UNAVAILABLE")
	}
	if trust.PeerSHA256 != "" && !controlHash(trust.PeerSHA256) {
		return "", errors.New("RELAY_HANDLE_UNAVAILABLE")
	}
	if trust.CertSHA256 != "" && !controlHash(trust.CertSHA256) {
		return "", errors.New("RELAY_HANDLE_UNAVAILABLE")
	}
	peerSHA256 := trust.PeerSHA256
	if peerSHA256 == "" && trust.CASHA256 != "" {
		peerSHA256 = trust.CertSHA256
	}
	if (trust.CASHA256 != "") != (peerSHA256 != "") {
		return "", errors.New("RELAY_HANDLE_UNAVAILABLE")
	}
	if trust.PeerSHA256 != "" && trust.CertSHA256 != "" && trust.PeerSHA256 != trust.CertSHA256 {
		return "", errors.New("RELAY_HANDLE_UNAVAILABLE")
	}
	if trust.ServerName != "" && !validRelayServerName(trust.ServerName) {
		return "", errors.New("RELAY_HANDLE_UNAVAILABLE")
	}
	if trust.MaxResponseBytes != 0 && (trust.MaxResponseBytes < 1 || trust.MaxResponseBytes > permission.MaxResponseBudgetBytes) {
		return "", errors.New("RELAY_HANDLE_UNAVAILABLE")
	}
	ticket := map[string]string{"endpoint": endpoint, "token": token}
	if trust.CASHA256 != "" && peerSHA256 != "" {
		ticket["ca_sha256"] = trust.CASHA256
		ticket["peer_sha256"] = peerSHA256
		if trust.ServerName != "" && !relayEndpointHostMatchesServerName(endpoint, trust.ServerName) {
			ticket["server_name"] = trust.ServerName
		}
	} else {
		if trust.CAFile != "" {
			ticket["ca_file"] = trust.CAFile
		}
		if trust.CertSHA256 != "" {
			ticket["cert_sha256"] = trust.CertSHA256
		}
		if trust.ServerName != "" && !relayEndpointHostMatchesServerName(endpoint, trust.ServerName) {
			ticket["server_name"] = trust.ServerName
		}
	}
	if trust.MaxResponseBytes > 0 {
		ticket["max_response_bytes"] = strconv.FormatInt(trust.MaxResponseBytes, 10)
	}
	b, err := json.Marshal(ticket)
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

func relayTLSClientConfig(caFile, caSHA256, certSHA256, serverName string) (*tls.Config, error) {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS13}
	if serverName != "" {
		if !validRelayServerName(serverName) {
			return nil, errors.New("RELAY_TICKET_INVALID")
		}
		tlsConfig.ServerName = serverName
	}
	if caSHA256 != "" && !controlHash(caSHA256) {
		return nil, errors.New("RELAY_TICKET_INVALID")
	}
	caPath := caFile
	if caPath != "" && (!filepath.IsAbs(caPath) || strings.ContainsAny(caPath, "\r\n\x00")) {
		return nil, errors.New("RELAY_TICKET_INVALID")
	}
	var pemBytes []byte
	if caPath != "" {
		b, err := os.ReadFile(caPath)
		if err == nil {
			pemBytes = b
		} else if caSHA256 == "" || !errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("RELAY_UNAVAILABLE")
		}
	}
	if len(pemBytes) == 0 {
		envPath := os.Getenv("SSL_CERT_FILE")
		if envPath == "" {
			envPath = os.Getenv("NODE_EXTRA_CA_CERTS")
		}
		if envPath == "" {
			if caPath != "" || caSHA256 != "" {
				return nil, errors.New("RELAY_UNAVAILABLE")
			}
		} else {
			if !filepath.IsAbs(envPath) || strings.ContainsAny(envPath, "\r\n\x00") {
				return nil, errors.New("RELAY_TICKET_INVALID")
			}
			b, err := os.ReadFile(envPath)
			if err != nil {
				return nil, errors.New("RELAY_UNAVAILABLE")
			}
			pemBytes = b
		}
	}
	if len(pemBytes) > 0 || caPath != "" || caSHA256 != "" {
		if len(pemBytes) == 0 || len(pemBytes) > 256*1024 {
			return nil, errors.New("RELAY_UNAVAILABLE")
		}
		if caSHA256 != "" {
			pemSum := sha256.Sum256(pemBytes)
			if hex.EncodeToString(pemSum[:]) != caSHA256 {
				return nil, errors.New("RELAY_TLS_PIN_MISMATCH")
			}
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pemBytes) {
			return nil, errors.New("RELAY_UNAVAILABLE")
		}
		tlsConfig.RootCAs = pool
	}
	if certSHA256 != "" {
		if !controlHash(certSHA256) {
			return nil, errors.New("RELAY_TICKET_INVALID")
		}
		tlsConfig.VerifyConnection = func(cs tls.ConnectionState) error {
			if cs.Version < tls.VersionTLS13 || len(cs.VerifiedChains) == 0 || len(cs.PeerCertificates) == 0 {
				return errors.New("RELAY_TLS_PEER_UNVERIFIED")
			}
			sum := sha256.Sum256(cs.PeerCertificates[0].Raw)
			if hex.EncodeToString(sum[:]) != certSHA256 {
				return errors.New("RELAY_TLS_PIN_MISMATCH")
			}
			return nil
		}
	}
	return tlsConfig, nil
}

// ServeTurnRelay bridges worker stdio to one private host-issued HTTP turn
// channel. The inherited ticket contains no Bot/owner/control credential. The
// endpoint launcher owns FD, process, filesystem and network containment.
func ServeTurnRelay(ctx context.Context, ticket, in io.Reader, out io.Writer) error { //nolint:gocyclo // closed fixed-program branches retain explicit checks in security order
	raw, err := io.ReadAll(io.LimitReader(ticket, 4097))
	if err != nil || len(raw) > 4096 {
		return errors.New("RELAY_TICKET_INVALID")
	}
	fields, err := strictObjectWithOptional(raw, []string{"endpoint", "token"}, "ca_file", "ca_sha256", "peer_sha256", "cert_sha256", "server_name", "max_response_bytes")
	if err != nil {
		return errors.New("RELAY_TICKET_INVALID")
	}
	var endpoint, token, caFile, caSHA256, peerSHA256, certSHA256, serverName string
	var ticketMaxBytes int64
	if json.Unmarshal(fields["endpoint"], &endpoint) != nil || json.Unmarshal(fields["token"], &token) != nil || !validRelayToken(token) {
		return errors.New("RELAY_TICKET_INVALID")
	}
	if rawCA, ok := fields["ca_file"]; ok && (json.Unmarshal(rawCA, &caFile) != nil || caFile == "") {
		return errors.New("RELAY_TICKET_INVALID")
	}
	if rawCASHA, ok := fields["ca_sha256"]; ok && (json.Unmarshal(rawCASHA, &caSHA256) != nil || caSHA256 == "") {
		return errors.New("RELAY_TICKET_INVALID")
	}
	if rawPeerSHA, ok := fields["peer_sha256"]; ok && (json.Unmarshal(rawPeerSHA, &peerSHA256) != nil || peerSHA256 == "") {
		return errors.New("RELAY_TICKET_INVALID")
	}
	if rawPin, ok := fields["cert_sha256"]; ok && (json.Unmarshal(rawPin, &certSHA256) != nil || certSHA256 == "") {
		return errors.New("RELAY_TICKET_INVALID")
	}
	if rawSNI, ok := fields["server_name"]; ok && (json.Unmarshal(rawSNI, &serverName) != nil || serverName == "") {
		return errors.New("RELAY_TICKET_INVALID")
	}
	if rawMax, ok := fields["max_response_bytes"]; ok {
		var validMax bool
		ticketMaxBytes, validMax = parseRelayTicketMaxBytes(rawMax)
		if !validMax {
			return errors.New("RELAY_TICKET_INVALID")
		}
	}
	if peerSHA256 != "" && certSHA256 != "" && peerSHA256 != certSHA256 {
		return errors.New("RELAY_TICKET_INVALID")
	}
	if certSHA256 == "" {
		certSHA256 = peerSHA256
	}
	if (caSHA256 != "" && certSHA256 == "") || (peerSHA256 != "" && caSHA256 == "") {
		return errors.New("RELAY_TICKET_INVALID")
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Host == "" || u.Scheme != "https" && (u.Scheme != "http" || u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") {
		return errors.New("RELAY_TICKET_INVALID")
	}
	tlsConfig, err := relayTLSClientConfig(caFile, caSHA256, certSHA256, serverName)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 120 * time.Second, Transport: &http.Transport{DisableKeepAlives: true, TLSClientConfig: tlsConfig}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
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
			resp, err := client.Do(req) //nolint:bodyclose // source response is bounded and closed under the existing request deadline
			if err != nil {
				if strings.Contains(err.Error(), "RELAY_TLS_PIN_MISMATCH") {
					return errors.New("RELAY_TLS_PIN_MISMATCH")
				}
				if strings.Contains(err.Error(), "RELAY_TLS_PEER_UNVERIFIED") {
					return errors.New("RELAY_TLS_PEER_UNVERIFIED")
				}
				return errors.New("RELAY_UNAVAILABLE")
			}
			limit, budgetErr := permission.RelayResponseBudgetBytes(ctx, endpoint, ticketMaxBytes, resp.Header.Get("X-Octo-Acquisition-Budget-Bytes"))
			if budgetErr != nil {
				closePermissionIO(resp.Body)
				return budgetErr
			}
			data, observed, overflow, bodyErr := permission.ReadBoundedHTTPResponseBody(resp, limit)
			closePermissionIO(resp.Body)
			if overflow {
				return &permission.ResponseBudgetError{Kind: "acquisition_bytes", Limit: limit, Observed: observed}
			}
			if bodyErr != nil || resp.StatusCode != 200 && resp.StatusCode != 202 {
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

func parseRelayTicketMaxBytes(raw json.RawMessage) (int64, bool) {
	trimmed := strings.TrimSpace(string(raw))
	if len(trimmed) >= 2 && trimmed[0] == '"' && trimmed[len(trimmed)-1] == '"' {
		trimmed = trimmed[1 : len(trimmed)-1]
	}
	if len(trimmed) == 0 || trimmed[0] < '1' || trimmed[0] > '9' {
		return 0, false
	}
	for i := 1; i < len(trimmed); i++ {
		if trimmed[i] < '0' || trimmed[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil || n < 1 || n > permission.MaxResponseBudgetBytes {
		return 0, false
	}
	return n, true
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
