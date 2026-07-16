package marketplace

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	apiClient "github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

const MaxArchiveBytes int64 = 64 << 20
const maxErrorBodyBytes int64 = 4 << 10

type APIClient interface {
	Do(context.Context, *apiClient.Request) ([]byte, error)
}

type Skill struct {
	ID   string
	Name string
}

type Archive struct {
	Body   []byte
	SHA256 string
}

type Options struct {
	AllowInsecureLocalhost bool
}

type Client struct {
	api  APIClient
	http *http.Client
	reg  *registry.Registry
	opts Options
}

func NewClient(api APIClient, httpClient *http.Client, options ...Options) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	var opts Options
	if len(options) > 0 {
		opts = options[0]
	}
	return &Client{api: api, http: httpClient, reg: registry.MustNew(), opts: opts}
}

func (c *Client) GetSkill(ctx context.Context, id string) (Skill, error) {
	op, err := c.operation("skill.get")
	if err != nil {
		return Skill{}, err
	}
	body, err := c.api.Do(ctx, &apiClient.Request{
		Service: op.Service,
		Method:  op.Method,
		Path:    operationPath(op.Path, "skill_id", id),
	})
	if err != nil {
		return Skill{}, err
	}
	var envelope struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return Skill{}, output.ErrAPI("INVALID_RESPONSE", "marketplace skill metadata is incomplete", "retry or contact the marketplace operator")
	}
	skill := Skill{
		ID:   jsonString(envelope.Data["skill_id"]),
		Name: jsonString(envelope.Data["name"]),
	}
	if skill.ID == "" || skill.Name == "" {
		return Skill{}, output.ErrAPI("INVALID_RESPONSE", "marketplace skill metadata is incomplete", "retry or contact the marketplace operator")
	}
	return skill, nil
}

func (c *Client) DownloadSkill(ctx context.Context, id string) (Archive, error) {
	op, err := c.operation("skill.download")
	if err != nil {
		return Archive{}, err
	}
	body, err := c.api.Do(ctx, &apiClient.Request{
		Service: op.Service,
		Method:  op.Method,
		Path:    operationPath(op.Path, "skill_id", id),
		Query:   url.Values{"format": []string{"json"}},
	})
	if err != nil {
		return Archive{}, err
	}
	var envelope struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return Archive{}, output.ErrAPI("INVALID_RESPONSE", "marketplace download response is missing artifact metadata", "retry or contact the marketplace operator")
	}
	downloadURL := jsonString(envelope.Data["download_url"])
	fileSHA256 := jsonString(envelope.Data["file_sha256"])
	if downloadURL == "" || fileSHA256 == "" {
		return Archive{}, output.ErrAPI("INVALID_RESPONSE", "marketplace download response is missing artifact metadata", "retry or contact the marketplace operator")
	}
	if err := validateDownloadURL(ctx, downloadURL, c.opts.AllowInsecureLocalhost); err != nil {
		return Archive{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return Archive{}, output.ErrAPI("INVALID_RESPONSE", "marketplace returned an invalid download URL", "contact the marketplace operator")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Archive{}, output.ErrNetwork(fmt.Sprintf("download skill archive: %v", err), "retry the download")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errorBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		if readErr != nil {
			return Archive{}, output.ErrNetwork(fmt.Sprintf("read skill download error response: %v", readErr), "retry the download")
		}
		return Archive{}, output.ParseBackendError(resp.StatusCode, errorBody)
	}
	body, err = io.ReadAll(io.LimitReader(resp.Body, MaxArchiveBytes+1))
	if err != nil {
		return Archive{}, output.ErrNetwork(fmt.Sprintf("read skill archive: %v", err), "retry the download")
	}
	if int64(len(body)) > MaxArchiveBytes {
		return Archive{}, output.ErrValidation("skill archive exceeds download limit", "reduce the skill archive below 64 MiB")
	}
	return Archive{Body: body, SHA256: fileSHA256}, nil
}

func (c *Client) operation(operationID string) (*registry.OperationDetail, error) {
	op, ok := c.reg.GetOperation(operationID)
	if !ok || op.Service != "marketplace" {
		return nil, output.ErrAPI("INVALID_RESPONSE", fmt.Sprintf("marketplace OpenAPI operation %q is unavailable", operationID), "update the embedded Marketplace OpenAPI document")
	}
	return op, nil
}

func operationPath(path, param, value string) string {
	return strings.ReplaceAll(path, "{"+param+"}", url.PathEscape(value))
}

func jsonString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}

func validateDownloadURL(ctx context.Context, rawURL string, allowInsecureLocalhost bool) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" {
		return output.ErrValidation("marketplace download URL must use https", "contact the marketplace operator")
	}
	if parsed.Scheme != "https" && !(allowInsecureLocalhost && parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname())) {
		return output.ErrValidation("marketplace download URL must use https", "contact the marketplace operator")
	}

	var addresses []net.IP
	if literal := net.ParseIP(parsed.Hostname()); literal != nil {
		addresses = []net.IP{literal}
	} else {
		resolved, err := net.DefaultResolver.LookupIPAddr(ctx, parsed.Hostname())
		if err != nil {
			return output.ErrNetwork(fmt.Sprintf("resolve marketplace download host: %v", err), "retry the download")
		}
		for _, address := range resolved {
			addresses = append(addresses, address.IP)
		}
	}
	for _, address := range addresses {
		if allowInsecureLocalhost && address.IsLoopback() {
			continue
		}
		if address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsUnspecified() {
			return output.ErrValidation("marketplace download URL resolves to a private or local address", "contact the marketplace operator")
		}
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
