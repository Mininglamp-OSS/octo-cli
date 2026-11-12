package permission

import (
	"context"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

// ReadCatalogPackage retrieves only the configured self authority's explicit v2
// manifest and its listed files. Every GET has independent server eligibility;
// this private staging result grants neither support nor a policy/native lease.
func (r *SelfPolicyReader) ReadCatalogPackage(ctx context.Context, key Key, pin CatalogPin, baseline CatalogBaseline, slots CatalogSlotValues) (*VerifiedCatalog, error) { //nolint:gocyclo // scope and per-read transport checks precede private whole-package validation
	if key.Origin != r.origin || key.BotUID != r.botUID || !key.Valid() || !validHex(pin.Digest, 64) || !asciiToken(pin.Version, 64, false) {
		return nil, ErrUnavailable
	}
	if _, ok := ctx.Deadline(); !ok {
		return nil, ErrUnavailable
	}
	fetch := func(path string, size int) ([]byte, error) {
		query := url.Values{"space_id": {key.SpaceID}, "format": {CatalogPackageFormat}, "catalog_version": {pin.Version}, "catalog_digest": {pin.Digest}}
		suffix := "/catalog"
		if path != "" {
			query.Set("path", path)
			suffix += "/file"
		}
		target := r.origin + "/v1/bot/permission-bundle/v1" + suffix + "?" + query.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
		if err != nil {
			return nil, ErrUnavailable
		}
		req.Header.Set("Authorization", "Bearer "+r.token)
		req.Header.Set("X-Space-ID", key.SpaceID)
		req.Header.Set("Cache-Control", "no-store")
		req.Header.Set("Accept", "application/json")
		if strings.HasPrefix(path, "blobs/") {
			req.Header.Set("Accept", "application/octet-stream")
		}
		resp, err := r.client.Do(req)
		if err != nil {
			return nil, ErrUnavailable
		}
		defer closeCatalogFile(resp.Body)
		if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Encoding") != "" || len(resp.Header.Values("Content-Type")) != 1 {
			return nil, ErrUnavailable
		}
		media, params, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
		if err != nil || media != req.Header.Get("Accept") {
			return nil, ErrUnavailable
		}
		for name, value := range params {
			if name != "charset" || !strings.EqualFold(value, "utf-8") || media != "application/json" {
				return nil, ErrUnavailable
			}
		}
		maximum := size
		if path == "" {
			maximum = 65536
		}
		if resp.ContentLength > int64(maximum) || path != "" && resp.ContentLength >= 0 && resp.ContentLength != int64(size) {
			return nil, ErrUnavailable
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, int64(maximum)+1))
		if err != nil || len(raw) > maximum || path != "" && len(raw) != size {
			return nil, ErrUnavailable
		}
		return raw, nil
	}
	manifest, err := fetch("", 0)
	if err != nil {
		return nil, err
	}
	if catalogHash(manifest) != pin.Digest || ValidateCatalogDefinition("Manifest", manifest) != nil {
		return nil, ErrUnavailable
	}
	value, _, err := catalogJSONValue(manifest, true)
	if err != nil {
		return nil, err
	}
	m := object(value)
	if m["catalog_version"] != pin.Version {
		return nil, ErrUnavailable
	}
	files := map[string][]byte{"manifest.json": manifest}
	total := len(manifest)
	previous := ""
	for _, item := range catalogArray(m["files"]) {
		file := object(item)
		path, size := text(file["path"]), catalogNumber(file["bytes"])
		if path <= previous || size > 8388608-total {
			return nil, ErrUnavailable
		}
		previous = path
		total += size
		raw, err := fetch(path, size)
		if err != nil || catalogHash(raw) != file["sha256"] {
			return nil, ErrUnavailable
		}
		files[path] = raw
	}
	return VerifyCatalogPackage(ctx, files, pin, baseline, slots)
}
