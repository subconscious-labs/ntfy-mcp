package main

import (
	"context"
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
//
// Default value is used when building without ldflags (e.g. `go run .`, `go test`).
var version = "dev"

var (
	ntfyURL   = "https://ntfy.sh"
	ntfyTopic string
	ntfyToken string
)

// loadConfig reads configuration from environment variables and applies
// defaults. It is the single source of truth: on every call it resets all
// global settings, so it is safe to call multiple times (e.g. from tests).
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

// newServer builds an MCP server with a single tool, send_notification,
// that POSTs a notification to the configured ntfy topic.
func newServer() *server.MCPServer {
	s := server.NewMCPServer(
		"ntfy-mcp",
		version,
		server.WithToolCapabilities(true),
	)

	tool := mcp.NewTool("send_notification",
		mcp.WithDescription("Send a push notification via ntfy"),
		mcp.WithString("message", mcp.Required(), mcp.Description("Notification body text")),
		mcp.WithString("title", mcp.Description("Optional notification title")),
		mcp.WithString("priority", mcp.Description("Optional priority: min, low, default, high, urgent")),
		mcp.WithString("tags", mcp.Description("Optional comma-separated tags (e.g. warning,skull)")),
	)

	s.AddTool(tool, sendNotificationHandler)
	return s
}

// run loads configuration and serves the MCP protocol over stdio.
// Kept separate from main so that main stays free of logic and run is testable.
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

// sendNotificationHandler handles the send_notification tool call: it builds
// an HTTP POST request to ntfy with the message as body and optional Title,
// Priority, Tags, and Authorization headers, then checks the response status.
func sendNotificationHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()

	message, ok := args["message"].(string)
	if !ok || message == "" {
		return mcp.NewToolResultError("message is required and must be a non-empty string"), nil
	}

	url := fmt.Sprintf("%s/%s", strings.TrimRight(ntfyURL, "/"), ntfyTopic)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(message))
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to create request: %v", err)), nil
	}

	req.Header.Set("Content-Type", "text/plain")
	if title, _ := args["title"].(string); title != "" {
		req.Header.Set("Title", title)
	}
	if priority, _ := args["priority"].(string); priority != "" {
		req.Header.Set("Priority", priority)
	}
	if tags, _ := args["tags"].(string); tags != "" {
		req.Header.Set("Tags", tags)
	}
	if ntfyToken != "" {
		req.Header.Set("Authorization", "Bearer "+ntfyToken)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("HTTP request failed: %v", err)), nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return mcp.NewToolResultError(fmt.Sprintf("ntfy returned %d: %s", resp.StatusCode, string(body))), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Notification sent (HTTP %d)", resp.StatusCode)), nil
}
