package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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
		gotPath, gotBody, gotCT, gotTitle, gotPriority, gotTags, gotAuth string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotPath = r.URL.Path
		gotBody = string(body)
		gotCT = r.Header.Get("Content-Type")
		gotTitle = r.Header.Get("Title")
		gotPriority = r.Header.Get("Priority")
		gotTags = r.Header.Get("Tags")
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"test"}`))
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

	if gotPath != "/alerts" {
		t.Errorf("path = %q, want %q", gotPath, "/alerts")
	}
	if gotBody != "hello" {
		t.Errorf("body = %q, want %q", gotBody, "hello")
	}
	if gotCT != "text/plain" {
		t.Errorf("Content-Type = %q, want %q", gotCT, "text/plain")
	}
	if gotTitle != "greeting" {
		t.Errorf("Title = %q, want %q", gotTitle, "greeting")
	}
	if gotPriority != "high" {
		t.Errorf("Priority = %q, want %q", gotPriority, "high")
	}
	if gotTags != "wave,smile" {
		t.Errorf("Tags = %q, want %q", gotTags, "wave,smile")
	}
	if gotAuth != "Bearer tk_test" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer tk_test")
	}
}

func TestSendNotification_TrailingSlashInURL(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	withNtfy(t, srv.URL+"/", "alerts", "")

	if _, err := sendNotificationHandler(context.Background(),
		newRequest(map[string]any{"message": "hi"})); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotPath != "/alerts" {
		t.Errorf("path = %q, want %q", gotPath, "/alerts")
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
