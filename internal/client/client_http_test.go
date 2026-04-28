package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- helpers ---

func newTestClient(srv *httptest.Server) *Client {
	return &Client{
		baseURL:    srv.URL,
		botToken:   "robot1/key123",
		httpClient: srv.Client(),
	}
}

// --- do() HTTP behavior ---

func TestDo_SetsAuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	c.botToken = "mybot/mykey"
	c.do(http.MethodGet, "/test", nil)

	if gotAuth != "Bearer mybot/mykey" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer mybot/mykey")
	}
}



func TestDo_SetsContentTypeForPOST(t *testing.T) {
	var gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		w.WriteHeader(200)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	c.do(http.MethodPost, "/test", map[string]string{"key": "val"})

	if gotCT != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotCT)
	}
}

func TestDo_NoContentTypeForGET(t *testing.T) {
	var gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		w.WriteHeader(200)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	c.do(http.MethodGet, "/test", nil)

	if gotCT != "" {
		t.Errorf("Content-Type should be empty for GET, got %q", gotCT)
	}
}

func TestDo_ErrorParsesEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte(`{"error":{"code":"TODO_NOT_FOUND","message":"todo not found"}}`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.do(http.MethodGet, "/test", nil)

	if err == nil {
		t.Fatal("expected error for 404")
	}
	if !strings.Contains(err.Error(), "TODO_NOT_FOUND") || !strings.Contains(err.Error(), "todo not found") {
		t.Errorf("error should contain parsed envelope, got: %v", err)
	}
}

func TestDo_ErrorFallsBackToRawBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(`plain error text`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.do(http.MethodGet, "/test", nil)

	if err == nil {
		t.Fatal("expected error for 500")
	}
	if !strings.Contains(err.Error(), "plain error text") {
		t.Errorf("error should contain raw body, got: %v", err)
	}
}

// --- URL escaping ---

func TestTodoGet_EscapesID(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RawPath
		if gotPath == "" {
			gotPath = r.URL.Path
		}
		w.WriteHeader(200)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	c.TodoGet("id/with/slashes")

	if !strings.Contains(gotPath, "id%2Fwith%2Fslashes") {
		t.Errorf("path should escape slashes, got %q", gotPath)
	}
}

func TestTodoRemoveAssignee_EscapesBothSegments(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RawPath
		if gotPath == "" {
			gotPath = r.URL.Path
		}
		w.WriteHeader(204)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	c.TodoRemoveAssignee("t/1", "u/2")

	if !strings.Contains(gotPath, "t%2F1") || !strings.Contains(gotPath, "u%2F2") {
		t.Errorf("both path segments should be escaped, got %q", gotPath)
	}
}

// --- API method routing ---

func TestTodoList_GETWithParams(t *testing.T) {
	var gotMethod, gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.WriteHeader(200)
		w.Write([]byte(`{"data":[],"pagination":{"has_more":false}}`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	c.TodoList("g1", "open", "u1", "", 10)

	if gotMethod != "GET" {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if gotPath != "/api/v1/todos" {
		t.Errorf("path = %q, want /api/v1/todos", gotPath)
	}
	if !strings.Contains(gotQuery, "goal_id=g1") || !strings.Contains(gotQuery, "status=open") || !strings.Contains(gotQuery, "assignee_id=u1") || !strings.Contains(gotQuery, "limit=10") {
		t.Errorf("query params incomplete: %q", gotQuery)
	}
}

func TestTodoCreate_POST(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(201)
		w.Write([]byte(`{"id":"new-1"}`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	c.TodoCreate(map[string]any{"title": "Test"})

	if gotMethod != "POST" {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/api/v1/todos" {
		t.Errorf("path = %q, want /api/v1/todos", gotPath)
	}
	if gotBody["title"] != "Test" {
		t.Errorf("body title = %v, want Test", gotBody["title"])
	}
}

func TestTodoTransition_PUT(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(200)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	c.TodoTransition("t1", "closed")

	if gotMethod != "PUT" {
		t.Errorf("method = %q, want PUT", gotMethod)
	}
	if gotPath != "/api/v1/todos/t1/status" {
		t.Errorf("path = %q, want /api/v1/todos/t1/status", gotPath)
	}
	if gotBody["status"] != "closed" {
		t.Errorf("body status = %q, want closed", gotBody["status"])
	}
}

func TestTodoDelete_DELETE(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(204)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	c.TodoDelete("t1")

	if gotMethod != "DELETE" {
		t.Errorf("method = %q, want DELETE", gotMethod)
	}
	if gotPath != "/api/v1/todos/t1" {
		t.Errorf("path = %q, want /api/v1/todos/t1", gotPath)
	}
}

func TestGoalList_GET(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(200)
		w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	c.GoalList("")

	if gotMethod != "GET" {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if gotPath != "/api/v1/goals" {
		t.Errorf("path = %q, want /api/v1/goals", gotPath)
	}
}

func TestTodoListComments_GET(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(200)
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	c.TodoListComments("t1")

	if gotMethod != "GET" {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if gotPath != "/api/v1/todos/t1/comments" {
		t.Errorf("path = %q, want /api/v1/todos/t1/comments", gotPath)
	}
}

func TestTodoAddAttachment_POST(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(201)
		w.Write([]byte(`{"id":"a1"}`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	c.TodoAddAttachment("t1", map[string]any{"file_url": "https://example.com/f.pdf"})

	if gotMethod != "POST" {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/api/v1/todos/t1/attachments" {
		t.Errorf("path = %q, want /api/v1/todos/t1/attachments", gotPath)
	}
}
