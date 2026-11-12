package permission

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// SelfPolicyReader can only read the complete exact-Bot self projection at the
// configured origin. It has no owner mutation, catalog administration, generic
// URL or credential fallback surface and never stores a positive policy.
type SelfPolicyReader struct {
	origin, botUID, token string
	client                *http.Client
}

// NewSelfPolicyReader requires a private host's independently verified v2 Bot
// identity. The constructor does not authenticate that identity itself. TLS
// roots are an in-process composition option, never a model argument.
func NewSelfPolicyReader(origin, botUID, token string, conf *tls.Config, allowedOrigins ...string) (*SelfPolicyReader, error) { //nolint:gocyclo // closed fixed-program branches retain explicit checks in security order
	if !ValidOrigin(origin) || !ValidNativeID(botUID) || !strings.HasPrefix(token, "bf_") || strings.ContainsAny(token, "\r\n\t ") {
		return nil, ErrInvalid
	}
	allowed := false
	for _, configured := range allowedOrigins {
		if origin == configured {
			allowed = true
		}
	}
	if !allowed {
		return nil, ErrInvalid
	}
	if conf == nil {
		conf = &tls.Config{MinVersion: tls.VersionTLS12}
	} else {
		conf = conf.Clone()
	}
	u, err := url.Parse(origin)
	if err != nil {
		return nil, ErrInvalid
	}
	if conf.InsecureSkipVerify || conf.ServerName != "" && conf.ServerName != u.Hostname() || conf.MaxVersion != 0 && conf.MaxVersion < tls.VersionTLS12 {
		return nil, ErrInvalid
	}
	if conf.MinVersion < tls.VersionTLS12 {
		conf.MinVersion = tls.VersionTLS12
	}
	conf.ServerName = u.Hostname()
	transport := &http.Transport{TLSClientConfig: conf, DisableCompression: true, DisableKeepAlives: true}
	return &SelfPolicyReader{origin, botUID, token, &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// ReadBundle initiates a new complete read after the caller's fact boundary.
// No ETag/304, redirect, compressed response, partial body or stale-cache value
// can replace P. D14 semantic errors are unavailable, even if wire HTTP is 200.
func (r *SelfPolicyReader) ReadBundle(ctx context.Context, key Key) ([]byte, error) {
	if key.Origin != r.origin || key.BotUID != r.botUID || !key.Valid() {
		return nil, ErrUnavailable
	}
	if _, ok := ctx.Deadline(); !ok {
		return nil, ErrUnavailable
	}
	target := r.origin + "/v1/bot/permission-bundle/v1?" + url.Values{"space_id": {key.SpaceID}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
	if err != nil {
		return nil, ErrUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	req.Header.Set("X-Space-ID", key.SpaceID)
	req.Header.Set("Cache-Control", "no-store")
	req.Header.Set("Accept", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer resp.Body.Close() //nolint:errcheck // response close cannot grant a policy
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Encoding") != "" {
		return nil, ErrUnavailable
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxBundleBytes+1))
	if err != nil || len(b) > MaxBundleBytes {
		return nil, ErrUnavailable
	}
	// Validate before returning any bytes to the coordinator. The coordinator
	// independently validates and accepts its revision under the shared lock.
	p, err := ParseBundle(strings.NewReader(string(b)))
	if err != nil || p.Key != key {
		return nil, ErrUnavailable
	}
	return b, nil
}
