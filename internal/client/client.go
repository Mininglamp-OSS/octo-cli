package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/dmwork-org/octo-cli/internal/config"
)

// Client is a REST client for the todo-service API.
type Client struct {
	baseURL    string
	botToken   string // robot_id/app_key format
	spaceID    string
	httpClient *http.Client
}

// esc escapes a path segment for safe URL concatenation.
func esc(s string) string { return url.PathEscape(s) }

// New creates a Client from config.
func New(cfg *config.Config) *Client {
	return &Client{
		baseURL:  cfg.APIURL,
		botToken: cfg.BotToken,
		spaceID:  cfg.SpaceID,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
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

	// Bot auth: "Bot <robot_id>/<app_key>"
	req.Header.Set("Authorization", "Bot "+c.botToken)
	if c.spaceID != "" {
		req.Header.Set("X-Space-ID", c.spaceID)
	}
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
		// Try to parse error envelope {"error":{"code":"...","message":"..."}}
		var envelope struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(respBody, &envelope) == nil && envelope.Error.Message != "" {
			return nil, fmt.Errorf("%s: %s", envelope.Error.Code, envelope.Error.Message)
		}
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
		params.Set("assignee_id", assignee)
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
	data, err := c.do(http.MethodGet, "/api/v1/todos/"+esc(id), nil)
	return json.RawMessage(data), err
}

// TodoCreate creates a new todo.
func (c *Client) TodoCreate(req map[string]any) (json.RawMessage, error) {
	data, err := c.do(http.MethodPost, "/api/v1/todos", req)
	return json.RawMessage(data), err
}

// TodoTransition changes todo status.
func (c *Client) TodoTransition(id, status string) (json.RawMessage, error) {
	data, err := c.do(http.MethodPut, "/api/v1/todos/"+esc(id)+"/status", map[string]string{"status": status})
	return json.RawMessage(data), err
}

// TodoUpdate updates a todo.
func (c *Client) TodoUpdate(id string, req map[string]any) (json.RawMessage, error) {
	data, err := c.do(http.MethodPut, "/api/v1/todos/"+esc(id), req)
	return json.RawMessage(data), err
}

// TodoDelete deletes a todo.
func (c *Client) TodoDelete(id string) error {
	_, err := c.do(http.MethodDelete, "/api/v1/todos/"+esc(id), nil)
	return err
}

// TodoAddAssignee adds an assignee to a todo.
func (c *Client) TodoAddAssignee(todoID, userID string) (json.RawMessage, error) {
	data, err := c.do(http.MethodPost, "/api/v1/todos/"+esc(todoID)+"/assignees", map[string]string{"user_id": userID})
	return json.RawMessage(data), err
}

// TodoRemoveAssignee removes an assignee from a todo.
func (c *Client) TodoRemoveAssignee(todoID, userID string) error {
	_, err := c.do(http.MethodDelete, "/api/v1/todos/"+esc(todoID)+"/assignees/"+esc(userID), nil)
	return err
}

// TodoComment adds a comment to a todo.
func (c *Client) TodoComment(todoID, content string) (json.RawMessage, error) {
	data, err := c.do(http.MethodPost, "/api/v1/todos/"+esc(todoID)+"/comments", map[string]string{"content": content})
	return json.RawMessage(data), err
}

// GoalList lists goals.
func (c *Client) GoalList() (json.RawMessage, error) {
	data, err := c.do(http.MethodGet, "/api/v1/goals", nil)
	return json.RawMessage(data), err
}

// GoalGet retrieves a single goal.
func (c *Client) GoalGet(id string) (json.RawMessage, error) {
	data, err := c.do(http.MethodGet, "/api/v1/goals/"+esc(id), nil)
	return json.RawMessage(data), err
}

// GoalCreate creates a new goal.
func (c *Client) GoalCreate(req map[string]any) (json.RawMessage, error) {
	data, err := c.do(http.MethodPost, "/api/v1/goals", req)
	return json.RawMessage(data), err
}

// GoalArchive archives a goal.
func (c *Client) GoalArchive(id string) error {
	_, err := c.do(http.MethodDelete, "/api/v1/goals/"+esc(id), nil)
	return err
}

// GoalUpdate updates a goal.
func (c *Client) GoalUpdate(id string, req map[string]any) (json.RawMessage, error) {
	data, err := c.do(http.MethodPut, "/api/v1/goals/"+esc(id), req)
	return json.RawMessage(data), err
}

// GoalAddAssignee adds an assignee to a goal.
func (c *Client) GoalAddAssignee(goalID, userID string) (json.RawMessage, error) {
	data, err := c.do(http.MethodPost, "/api/v1/goals/"+esc(goalID)+"/assignees", map[string]string{"user_id": userID})
	return json.RawMessage(data), err
}

// GoalRemoveAssignee removes an assignee from a goal.
func (c *Client) GoalRemoveAssignee(goalID, userID string) error {
	_, err := c.do(http.MethodDelete, "/api/v1/goals/"+esc(goalID)+"/assignees/"+esc(userID), nil)
	return err
}

// TodoListComments lists comments on a todo.
func (c *Client) TodoListComments(todoID string) (json.RawMessage, error) {
	data, err := c.do(http.MethodGet, "/api/v1/todos/"+esc(todoID)+"/comments", nil)
	return json.RawMessage(data), err
}

// TodoDeleteComment deletes a comment.
func (c *Client) TodoDeleteComment(todoID, commentID string) error {
	_, err := c.do(http.MethodDelete, "/api/v1/todos/"+esc(todoID)+"/comments/"+esc(commentID), nil)
	return err
}

// TodoListAttachments lists attachments on a todo.
func (c *Client) TodoListAttachments(todoID string) (json.RawMessage, error) {
	data, err := c.do(http.MethodGet, "/api/v1/todos/"+esc(todoID)+"/attachments", nil)
	return json.RawMessage(data), err
}

// TodoAddAttachment adds an attachment to a todo.
func (c *Client) TodoAddAttachment(todoID string, req map[string]any) (json.RawMessage, error) {
	data, err := c.do(http.MethodPost, "/api/v1/todos/"+esc(todoID)+"/attachments", req)
	return json.RawMessage(data), err
}

// TodoDeleteAttachment deletes an attachment.
func (c *Client) TodoDeleteAttachment(todoID, attachmentID string) error {
	_, err := c.do(http.MethodDelete, "/api/v1/todos/"+esc(todoID)+"/attachments/"+esc(attachmentID), nil)
	return err
}
