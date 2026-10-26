package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// version is overridden at build time via:
//
//	go build -ldflags "-X main.version=$(VERSION)" .
var version = "dev"

var (
	ntfyURL   = "https://ntfy.sh"
	ntfyTopic string
	ntfyToken string
)

// loadConfig reads configuration from environment variables and applies defaults.
// It is the single source of truth and is safe to call multiple times.
func loadConfig() error {
	ntfyURL = "https://ntfy.sh"
	if v := os.Getenv("NTFY_URL"); v != "" {
		ntfyURL = v
	}
	ntfyTopic = os.Getenv("NTFY_TOPIC")
	ntfyToken = os.Getenv("NTFY_TOKEN")
	if ntfyTopic == "" {
		return errors.New("NTFY_TOPIC environment variable is required")
	}
	return nil
}

// publishRequest mirrors the ntfy JSON publish API. All fields except Message
// are optional. Cache and Firebase use pointers so that an explicit `false`
// can be distinguished from "not set".
type publishRequest struct {
	Topic      string          `json:"topic"`
	Message    string          `json:"message"`
	Title      string          `json:"title,omitempty"`
	Priority   string          `json:"priority,omitempty"`
	Tags       []string        `json:"tags,omitempty"`
	Click      string          `json:"click,omitempty"`
	Icon       string          `json:"icon,omitempty"`
	Markdown   bool            `json:"markdown,omitempty"`
	Delay      string          `json:"delay,omitempty"`
	Attach     string          `json:"attach,omitempty"`
	Filename   string          `json:"filename,omitempty"`
	Email      string          `json:"email,omitempty"`
	Call       string          `json:"call,omitempty"`
	Cache      *bool           `json:"cache,omitempty"`
	Firebase   *bool           `json:"firebase,omitempty"`
	Actions    json.RawMessage `json:"actions,omitempty"`
	SequenceID string          `json:"sequence_id,omitempty"`
}

// newServer builds an MCP server with a single tool, send_notification.
func newServer() *server.MCPServer {
	s := server.NewMCPServer(
		"ntfy-mcp",
		version,
		server.WithToolCapabilities(true),
	)

	tool := mcp.NewTool("send_notification",
		mcp.WithDescription("Send or update a push notification via ntfy"),
		mcp.WithString("message", mcp.Required(), mcp.Description("Notification body text")),
		mcp.WithString("title", mcp.Description("Optional notification title")),
		mcp.WithString("priority", mcp.Description("Optional priority: min, low, default, high, urgent")),
		mcp.WithString("tags", mcp.Description("Optional comma-separated tags (e.g. warning,skull)")),
		mcp.WithString("click", mcp.Description("Optional URL opened when the notification is tapped")),
		mcp.WithString("icon", mcp.Description("Optional URL of a notification icon")),
		mcp.WithBoolean("markdown", mcp.Description("Render the message body as Markdown")),
		mcp.WithString("delay", mcp.Description("Optional delivery delay (e.g. 30m, 1h)")),
		mcp.WithString("attach", mcp.Description("Optional URL of a file to attach")),
		mcp.WithString("filename", mcp.Description("Optional filename for the attachment")),
		mcp.WithString("email", mcp.Description("Optional email address to forward the notification to")),
		mcp.WithString("call", mcp.Description("Optional phone number to call with the notification")),
		mcp.WithBoolean("cache", mcp.Description("Set to false to keep the message out of the server cache")),
		mcp.WithBoolean("firebase", mcp.Description("Set to false to skip forwarding via Firebase")),
		mcp.WithString("actions", mcp.Description("Optional JSON array of action buttons")),
		mcp.WithString("sequence_id", mcp.Description("Optional sequence ID to update an existing notification")),
	)

	s.AddTool(tool, sendNotificationHandler)
	return s
}

// run loads configuration and serves the MCP protocol over stdio.
func run() error {
	if err := loadConfig(); err != nil {
		return err
	}
	return server.ServeStdio(newServer())
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// sendNotificationHandler builds a JSON publish request from the tool arguments,
// POSTs it to ntfy, and reports the resulting message ID on success.
func sendNotificationHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	message, ok := args["message"].(string)
	if !ok || message == "" {
		return mcp.NewToolResultError("message is required and must be a non-empty string"), nil
	}

	pr := publishRequest{
		Topic:   ntfyTopic,
		Message: message,
	}

	if v, _ := args["title"].(string); v != "" {
		pr.Title = v
	}
	if v, _ := args["priority"].(string); v != "" {
		pr.Priority = v
	}
	if v, _ := args["tags"].(string); v != "" {
		pr.Tags = strings.Split(v, ",")
	}
	if v, _ := args["click"].(string); v != "" {
		pr.Click = v
	}
	if v, _ := args["icon"].(string); v != "" {
		pr.Icon = v
	}
	if v, _ := args["markdown"].(bool); v {
		pr.Markdown = true
	}
	if v, _ := args["delay"].(string); v != "" {
		pr.Delay = v
	}
	if v, _ := args["attach"].(string); v != "" {
		pr.Attach = v
	}
	if v, _ := args["filename"].(string); v != "" {
		pr.Filename = v
	}
	if v, _ := args["email"].(string); v != "" {
		pr.Email = v
	}
	if v, _ := args["call"].(string); v != "" {
		pr.Call = v
	}
	if v, ok := args["cache"].(bool); ok {
		pr.Cache = &v
	}
	if v, ok := args["firebase"].(bool); ok {
		pr.Firebase = &v
	}
	if v, _ := args["actions"].(string); v != "" {
		pr.Actions = json.RawMessage(v)
	}
	if v, _ := args["sequence_id"].(string); v != "" {
		pr.SequenceID = v
	}

	body, err := json.Marshal(pr)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal request: %v", err)), nil
	}

	url := strings.TrimRight(ntfyURL, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to create request: %v", err)), nil
	}

	req.Header.Set("Content-Type", "application/json")
	if ntfyToken != "" {
		req.Header.Set("Authorization", "Bearer "+ntfyToken)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("HTTP request failed: %v", err)), nil
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return mcp.NewToolResultError(fmt.Sprintf("ntfy returned %d: %s", resp.StatusCode, string(respBody))), nil
	}

	var result struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(respBody, &result)

	if result.ID != "" {
		return mcp.NewToolResultText(fmt.Sprintf("Notification sent (HTTP %d, id=%s)", resp.StatusCode, result.ID)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Notification sent (HTTP %d)", resp.StatusCode)), nil
}
