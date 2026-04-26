package client

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dmwork-org/octo-cli/internal/config"
)

// Client is a REST client for the todo-service API.
type Client struct {
	baseURL    string
	botToken   string
	spaceID    string // extracted from JWT space_id claim
	httpClient *http.Client
}

// New creates a Client from config. It extracts space_id from the JWT token.
func New(cfg *config.Config) (*Client, error) {
	spaceID := extractSpaceIDFromJWT(cfg.BotToken)
	if spaceID == "" {
		return nil, fmt.Errorf("JWT does not contain space_id claim — ensure the token was issued with a space context")
	}
	return &Client{
		baseURL:  cfg.APIURL,
		botToken: cfg.BotToken,
		spaceID:  spaceID,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}, nil
}

// extractSpaceIDFromJWT extracts the space_id claim from a JWT without verification.
// Verification is done server-side; CLI just needs the claim for the X-Space-ID header.
func extractSpaceIDFromJWT(token string) string {
	parts := strings.SplitN(token, ".", 3)
	if len(parts) < 2 {
		return ""
	}
	// Decode payload (add padding if needed)
	payload := parts[1]
	if m := len(payload) % 4; m != 0 {
		payload += strings.Repeat("=", 4-m)
	}
	data, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return ""
	}
	var claims map[string]any
	if err := json.Unmarshal(data, &claims); err != nil {
		return ""
	}
	if spaceID, ok := claims["space_id"].(string); ok {
		return spaceID
	}
	return ""
}

func (c *Client) do(method, path string, body any) ([]byte, error) {
	u := c.baseURL + path

	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, u, reader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.botToken)
	req.Header.Set("X-Space-ID", c.spaceID)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// TodoList lists todos with optional filters.
func (c *Client) TodoList(goalID, status, assignee, cursor string, limit int) (json.RawMessage, error) {
	params := url.Values{}
	if goalID != "" {
		params.Set("goal_id", goalID)
	}
	if status != "" {
		params.Set("status", status)
	}
	if assignee != "" {
		params.Set("assignee", assignee)
	}
	if cursor != "" {
		params.Set("cursor", cursor)
	}
	if limit > 0 {
		params.Set("limit", fmt.Sprintf("%d", limit))
	}
	path := "/api/v1/todos"
	if len(params) > 0 {
		path += "?" + params.Encode()
	}
	data, err := c.do(http.MethodGet, path, nil)
	return json.RawMessage(data), err
}

// TodoGet retrieves a single todo.
func (c *Client) TodoGet(id string) (json.RawMessage, error) {
	data, err := c.do(http.MethodGet, "/api/v1/todos/"+id, nil)
	return json.RawMessage(data), err
}

// TodoCreate creates a new todo.
func (c *Client) TodoCreate(req map[string]any) (json.RawMessage, error) {
	data, err := c.do(http.MethodPost, "/api/v1/todos", req)
	return json.RawMessage(data), err
}

// TodoTransition changes todo status.
func (c *Client) TodoTransition(id, status string) (json.RawMessage, error) {
	data, err := c.do(http.MethodPut, "/api/v1/todos/"+id+"/status", map[string]string{"status": status})
	return json.RawMessage(data), err
}

// TodoUpdate updates a todo.
func (c *Client) TodoUpdate(id string, req map[string]any) (json.RawMessage, error) {
	data, err := c.do(http.MethodPut, "/api/v1/todos/"+id, req)
	return json.RawMessage(data), err
}

// TodoDelete deletes a todo.
func (c *Client) TodoDelete(id string) error {
	_, err := c.do(http.MethodDelete, "/api/v1/todos/"+id, nil)
	return err
}

// TodoAddAssignee adds an assignee to a todo.
func (c *Client) TodoAddAssignee(todoID, userID string) (json.RawMessage, error) {
	data, err := c.do(http.MethodPost, "/api/v1/todos/"+todoID+"/assignees", map[string]string{"user_id": userID})
	return json.RawMessage(data), err
}

// TodoRemoveAssignee removes an assignee from a todo.
func (c *Client) TodoRemoveAssignee(todoID, userID string) error {
	_, err := c.do(http.MethodDelete, "/api/v1/todos/"+todoID+"/assignees/"+userID, nil)
	return err
}

// TodoComment adds a comment to a todo.
func (c *Client) TodoComment(todoID, content string) (json.RawMessage, error) {
	data, err := c.do(http.MethodPost, "/api/v1/todos/"+todoID+"/comments", map[string]string{"content": content})
	return json.RawMessage(data), err
}

// GoalList lists goals.
func (c *Client) GoalList() (json.RawMessage, error) {
	data, err := c.do(http.MethodGet, "/api/v1/goals", nil)
	return json.RawMessage(data), err
}

// GoalGet retrieves a single goal (kanban view).
func (c *Client) GoalGet(id string) (json.RawMessage, error) {
	data, err := c.do(http.MethodGet, "/api/v1/goals/"+id, nil)
	return json.RawMessage(data), err
}

// GoalCreate creates a new goal.
func (c *Client) GoalCreate(req map[string]any) (json.RawMessage, error) {
	data, err := c.do(http.MethodPost, "/api/v1/goals", req)
	return json.RawMessage(data), err
}

// GoalArchive archives a goal.
func (c *Client) GoalArchive(id string) error {
	_, err := c.do(http.MethodDelete, "/api/v1/goals/"+id, nil)
	return err
}
