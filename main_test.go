package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestVersionDefaultIsSet(t *testing.T) {
	if version == "" {
		t.Fatal("version must not be empty")
	}
}

// newRequest builds a CallToolRequest carrying the given arguments.
func newRequest(args map[string]any) mcp.CallToolRequest {
	var req mcp.CallToolRequest
	req.Params.Arguments = args
	return req
}

// resultText extracts the text payload from a tool result (used in assertions).
func resultText(t *testing.T, r *mcp.CallToolResult) string {
	t.Helper()
	if len(r.Content) == 0 {
		return ""
	}
	tc, ok := r.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", r.Content[0])
	}
	return tc.Text
}

// withNtfy swaps the global settings for the duration of a test and
// restores them on cleanup.
func withNtfy(t *testing.T, url, topic, token string) {
	t.Helper()
	oldURL, oldTopic, oldToken := ntfyURL, ntfyTopic, ntfyToken
	ntfyURL, ntfyTopic, ntfyToken = url, topic, token
	t.Cleanup(func() { ntfyURL, ntfyTopic, ntfyToken = oldURL, oldTopic, oldToken })
}

func TestSendNotification_Success(t *testing.T) {
	var (
		gotPath string
		gotReq  publishRequest
		gotCT   string
		gotAuth string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotPath = r.URL.Path
		gotCT = r.Header.Get("Content-Type")
		gotAuth = r.Header.Get("Authorization")
		_ = json.Unmarshal(body, &gotReq)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"abc123"}`))
	}))
	defer srv.Close()

	withNtfy(t, srv.URL, "alerts", "tk_test")

	res, err := sendNotificationHandler(context.Background(), newRequest(map[string]any{
		"message":  "hello",
		"title":    "greeting",
		"priority": "high",
		"tags":     "wave,smile",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", resultText(t, res))
	}

	if gotPath != "/" {
		t.Errorf("path = %q, want %q", gotPath, "/")
	}
	if gotCT != "application/json" {
		t.Errorf("Content-Type = %q, want %q", gotCT, "application/json")
	}
	if gotAuth != "Bearer tk_test" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer tk_test")
	}
	if gotReq.Topic != "alerts" {
		t.Errorf("topic = %q, want %q", gotReq.Topic, "alerts")
	}
	if gotReq.Message != "hello" {
		t.Errorf("message = %q, want %q", gotReq.Message, "hello")
	}
	if gotReq.Title != "greeting" {
		t.Errorf("title = %q, want %q", gotReq.Title, "greeting")
	}
	if gotReq.Priority != "high" {
		t.Errorf("priority = %q, want %q", gotReq.Priority, "high")
	}
	if len(gotReq.Tags) != 2 || gotReq.Tags[0] != "wave" || gotReq.Tags[1] != "smile" {
		t.Errorf("tags = %v, want [wave smile]", gotReq.Tags)
	}

	// The response body should mention the returned message ID.
	if txt := resultText(t, res); !strings.Contains(txt, "abc123") {
		t.Errorf("result text = %q, want it to contain id=abc123", txt)
	}
}

func TestSendNotification_TrailingSlashInURL(t *testing.T) {
	var (
		gotPath string
		gotReq  publishRequest
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotPath = r.URL.Path
		_ = json.Unmarshal(body, &gotReq)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"x"}`))
	}))
	defer srv.Close()

	withNtfy(t, srv.URL+"/", "alerts", "")

	if _, err := sendNotificationHandler(context.Background(),
		newRequest(map[string]any{"message": "hi"})); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// With the JSON publish API, the topic travels in the body and the POST
	// goes to the root of NTFY_URL — regardless of any trailing slash.
	if gotPath != "/" {
		t.Errorf("path = %q, want %q", gotPath, "/")
	}
	if gotReq.Topic != "alerts" {
		t.Errorf("topic = %q, want %q", gotReq.Topic, "alerts")
	}
	if gotReq.Message != "hi" {
		t.Errorf("message = %q, want %q", gotReq.Message, "hi")
	}
}

func TestSendNotification_MissingMessage(t *testing.T) {
	res, err := sendNotificationHandler(context.Background(), newRequest(map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected tool error, got success: %s", resultText(t, res))
	}
}

func TestSendNotification_EmptyMessage(t *testing.T) {
	res, err := sendNotificationHandler(context.Background(),
		newRequest(map[string]any{"message": ""}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected tool error, got success")
	}
}

func TestSendNotification_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	withNtfy(t, srv.URL, "alerts", "")

	res, err := sendNotificationHandler(context.Background(),
		newRequest(map[string]any{"message": "hi"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected tool error, got success")
	}
}

func TestSendNotification_NoAuthHeaderWithoutToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	withNtfy(t, srv.URL, "alerts", "")

	if _, err := sendNotificationHandler(context.Background(),
		newRequest(map[string]any{"message": "hi"})); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAuth != "" {
		t.Errorf("Authorization = %q, want empty", gotAuth)
	}
}

func TestLoadConfig_RequiresTopic(t *testing.T) {
	t.Setenv("NTFY_TOPIC", "")
	t.Setenv("NTFY_URL", "")
	t.Setenv("NTFY_TOKEN", "")

	oldURL, oldTopic, oldToken := ntfyURL, ntfyTopic, ntfyToken
	ntfyURL, ntfyTopic, ntfyToken = "", "", ""
	t.Cleanup(func() { ntfyURL, ntfyTopic, ntfyToken = oldURL, oldTopic, oldToken })

	if err := loadConfig(); err == nil {
		t.Fatalf("expected error when NTFY_TOPIC is unset")
	}
}

func TestLoadConfig_Defaults(t *testing.T) {
	t.Setenv("NTFY_TOPIC", "alerts")
	t.Setenv("NTFY_URL", "")
	t.Setenv("NTFY_TOKEN", "")

	oldURL, oldTopic, oldToken := ntfyURL, ntfyTopic, ntfyToken
	ntfyURL, ntfyTopic, ntfyToken = "", "", ""
	t.Cleanup(func() { ntfyURL, ntfyTopic, ntfyToken = oldURL, oldTopic, oldToken })

	if err := loadConfig(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ntfyURL != "https://ntfy.sh" {
		t.Errorf("default NTFY_URL = %q, want %q", ntfyURL, "https://ntfy.sh")
	}
	if ntfyTopic != "alerts" {
		t.Errorf("NTFY_TOPIC = %q, want %q", ntfyTopic, "alerts")
	}
}

func TestLoadConfig_Overrides(t *testing.T) {
	t.Setenv("NTFY_TOPIC", "prod")
	t.Setenv("NTFY_URL", "https://ntfy.example.com/")
	t.Setenv("NTFY_TOKEN", "tk_abc")

	oldURL, oldTopic, oldToken := ntfyURL, ntfyTopic, ntfyToken
	ntfyURL, ntfyTopic, ntfyToken = "", "", ""
	t.Cleanup(func() { ntfyURL, ntfyTopic, ntfyToken = oldURL, oldTopic, oldToken })

	if err := loadConfig(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ntfyURL != "https://ntfy.example.com/" || ntfyTopic != "prod" || ntfyToken != "tk_abc" {
		t.Errorf("unexpected config: %q %q %q", ntfyURL, ntfyTopic, ntfyToken)
	}
}

func TestRun_ConfigError(t *testing.T) {
	t.Setenv("NTFY_TOPIC", "")
	t.Setenv("NTFY_URL", "")
	t.Setenv("NTFY_TOKEN", "")

	oldURL, oldTopic, oldToken := ntfyURL, ntfyTopic, ntfyToken
	t.Cleanup(func() { ntfyURL, ntfyTopic, ntfyToken = oldURL, oldTopic, oldToken })

	if err := run(); err == nil {
		t.Fatal("expected error when NTFY_TOPIC is unset")
	}
}

func TestNewServer_RegistersTool(t *testing.T) {
	s := newServer()
	if s == nil {
		t.Fatal("newServer returned nil")
	}
	// mcp-go keeps tools internal; if your version exposes ListTools()/GetTool(),
	// additionally assert that "send_notification" is present.
	// Without such an API the nil check above is the meaningful part:
	// a smoke test that catches panics in the constructor.
}

func TestSendNotification_BadURL(t *testing.T) {
	// "http://[::1" has an unterminated IPv6 bracket; url.Parse is guaranteed
	// to fail with "missing ']' in host" before any network call is made.
	withNtfy(t, "http://[::1", "alerts", "")

	res, err := sendNotificationHandler(context.Background(),
		newRequest(map[string]any{"message": "hi"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected tool error for malformed URL")
	}
}

func TestSendNotification_ExtendedFields(t *testing.T) {
	var got publishRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"x"}`))
	}))
	defer srv.Close()

	withNtfy(t, srv.URL, "alerts", "")

	_, err := sendNotificationHandler(context.Background(), newRequest(map[string]any{
		"message":     "hi",
		"click":       "https://example.com",
		"icon":        "https://example.com/icon.png",
		"markdown":    true,
		"delay":       "30m",
		"attach":      "https://example.com/file.pdf",
		"filename":    "report.pdf",
		"email":       "ops@example.com",
		"call":        "+15551234567",
		"cache":       false,
		"firebase":    false,
		"actions":     `[{"action":"view","label":"Open","url":"https://example.com"}]`,
		"sequence_id": "seq-1",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.Click != "https://example.com" {
		t.Errorf("click = %q", got.Click)
	}
	if got.Icon != "https://example.com/icon.png" {
		t.Errorf("icon = %q", got.Icon)
	}
	if !got.Markdown {
		t.Error("markdown = false, want true")
	}
	if got.Delay != "30m" {
		t.Errorf("delay = %q", got.Delay)
	}
	if got.Attach != "https://example.com/file.pdf" {
		t.Errorf("attach = %q", got.Attach)
	}
	if got.Filename != "report.pdf" {
		t.Errorf("filename = %q", got.Filename)
	}
	if got.Email != "ops@example.com" {
		t.Errorf("email = %q", got.Email)
	}
	if got.Call != "+15551234567" {
		t.Errorf("call = %q", got.Call)
	}
	if got.Cache == nil || *got.Cache != false {
		t.Errorf("cache = %v, want pointer to false", got.Cache)
	}
	if got.Firebase == nil || *got.Firebase != false {
		t.Errorf("firebase = %v, want pointer to false", got.Firebase)
	}
	if string(got.Actions) != `[{"action":"view","label":"Open","url":"https://example.com"}]` {
		t.Errorf("actions = %s", string(got.Actions))
	}
	if got.SequenceID != "seq-1" {
		t.Errorf("sequence_id = %q", got.SequenceID)
	}
}

func TestSendNotification_InvalidActionsJSON(t *testing.T) {
	withNtfy(t, "http://127.0.0.1:1", "alerts", "")

	res, err := sendNotificationHandler(context.Background(), newRequest(map[string]any{
		"message": "hi",
		"actions": `not-json`,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected tool error for invalid actions JSON")
	}
}
