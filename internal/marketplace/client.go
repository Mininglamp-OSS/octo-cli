package marketplace

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"

	apiClient "github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

const MaxArchiveBytes int64 = 64 << 20
const maxErrorBodyBytes int64 = 4 << 10

type APIClient interface {
	Do(context.Context, *apiClient.Request) ([]byte, error)
}

type Skill struct {
	ID         string `json:"skill_id"`
	Name       string `json:"name"`
	FileSHA256 string `json:"file_sha256"`
}

type Client struct {
	api  APIClient
	http *http.Client
}

func NewClient(api APIClient, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{api: api, http: httpClient}
}

func (c *Client) GetSkill(ctx context.Context, id string) (Skill, error) {
	body, err := c.api.Do(ctx, &apiClient.Request{
		Service: "marketplace",
		Method:  http.MethodGet,
		Path:    "/skills/" + url.PathEscape(id),
	})
	if err != nil {
		return Skill{}, err
	}
	var envelope struct {
		Data Skill `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Data.ID == "" || envelope.Data.Name == "" || envelope.Data.FileSHA256 == "" {
		return Skill{}, output.ErrAPI("INVALID_RESPONSE", "marketplace skill metadata is incomplete", "retry or contact the marketplace operator")
	}
	return envelope.Data, nil
}

func (c *Client) DownloadSkill(ctx context.Context, id string) ([]byte, error) {
	body, err := c.api.Do(ctx, &apiClient.Request{
		Service:        "marketplace",
		Method:         http.MethodGet,
		Path:           "/skills/" + url.PathEscape(id) + "/download",
		BinaryResponse: true,
	})
	if err != nil {
		return nil, err
	}
	var redirect struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &redirect); err != nil || redirect.URL == "" {
		return nil, output.ErrAPI("INVALID_RESPONSE", "marketplace download response is missing a URL", "retry or contact the marketplace operator")
	}
	if err := validateDownloadURL(ctx, redirect.URL); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, redirect.URL, nil)
	if err != nil {
		return nil, output.ErrAPI("INVALID_RESPONSE", "marketplace returned an invalid download URL", "contact the marketplace operator")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, output.ErrNetwork(fmt.Sprintf("download skill archive: %v", err), "retry the download")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errorBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		if readErr != nil {
			return nil, output.ErrNetwork(fmt.Sprintf("read skill download error response: %v", readErr), "retry the download")
		}
		return nil, output.ParseBackendError(resp.StatusCode, errorBody)
	}
	body, err = io.ReadAll(io.LimitReader(resp.Body, MaxArchiveBytes+1))
	if err != nil {
		return nil, output.ErrNetwork(fmt.Sprintf("read skill archive: %v", err), "retry the download")
	}
	if int64(len(body)) > MaxArchiveBytes {
		return nil, output.ErrValidation("skill archive exceeds download limit", "reduce the skill archive below 64 MiB")
	}
	return body, nil
}

func validateDownloadURL(ctx context.Context, rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
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
		if address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsUnspecified() {
			return output.ErrValidation("marketplace download URL resolves to a private or local address", "contact the marketplace operator")
		}
	}
	return nil
}
