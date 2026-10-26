# ntfy-mcp

A minimal [MCP](https://modelcontextprotocol.io) server written in Go that exposes
a single tool - `send_notification` - for pushing notifications through
[ntfy](https://ntfy.sh). Designed to be plugged into LLM clients such as
Claude Desktop, Cursor, or Cline.

Transport is `stdio`. All configuration comes from environment variables.

## Features

- One tool: `send_notification`.
- Optional `title`, `priority`, and `tags` arguments.
- Full pass-through of the ntfy JSON publish API: `click`, `icon`, `markdown`, `delay`, `attach`, `filename`, `email`, `call`, `cache`, `firebase`, `actions`, and `sequence_id`.
- Optional Bearer-token authentication via `NTFY_TOKEN`.
- Version is injected at build time through `-ldflags -X main.version`.
- No dependencies other than `github.com/mark3labs/mcp-go` and the Go stdlib.

## Requirements

- Go 1.22+
- An ntfy server: the public `https://ntfy.sh`, or a self-hosted instance

## Build

```bash
git clone <repo> ntfy-mcp && cd ntfy-mcp
go mod init ntfy-mcp
go get github.com/mark3labs/mcp-go
go build -o ntfy-mcp .
```

If you use the bundled `Makefile`, `make build` handles everything for you and
injects the version derived from `git describe --tags --always --dirty=-dev`.

## Configuration

All parameters are read from the environment at startup:

| Variable     | Required | Default             | Description                                                     |
|--------------|:--------:|---------------------|-----------------------------------------------------------------|
| `NTFY_URL`   | no       | `https://ntfy.sh`   | Base URL of the ntfy server.                                    |
| `NTFY_TOPIC` | **yes**  | —                   | Topic to which notifications are published.                     |
| `NTFY_TOKEN` | no       | —                   | Bearer token for protected topics (`Authorization: Bearer …`).  |

If `NTFY_TOPIC` is missing, the server exits with a configuration error.

## Client integration

Example entry for `mcp.json` / `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "ntfy": {
      "command": "/absolute/path/to/ntfy-mcp",
      "env": {
        "NTFY_TOPIC": "my-alerts",
        "NTFY_URL": "https://ntfy.sh",
        "NTFY_TOKEN": "tk_..."
      }
    }
  }
}
```

`NTFY_TOKEN` may be omitted for public topics.

## Tool `send_notification`

| Argument      | Type    | Required | Description                                                        |
|---------------|---------|:--------:|--------------------------------------------------------------------|
| `message`     | string  | yes      | Notification body.                                                 |
| `title`       | string  | no       | Notification title.                                                |
| `priority`    | string  | no       | `min`, `low`, `default`, `high`, `urgent`.                         |
| `tags`        | string  | no       | Comma-separated tags, e.g. `warning,skull`.                        |
| `click`       | string  | no       | URL opened when the notification is tapped.                        |
| `icon`        | string  | no       | URL of a notification icon.                                        |
| `markdown`    | boolean | no       | Render the message body as Markdown.                               |
| `delay`       | string  | no       | Delivery delay, e.g. `30m` or `1h`.                                |
| `attach`      | string  | no       | URL of a file to attach (URL only — no upload).                    |
| `filename`    | string  | no       | Filename for the attachment.                                       |
| `email`       | string  | no       | Forward the notification to this email address.                    |
| `call`        | string  | no       | Call this phone number with the notification.                      |
| `cache`       | boolean | no       | Set `false` to keep the message out of the server cache.           |
| `firebase`    | boolean | no       | Set `false` to skip forwarding via Firebase.                       |
| `actions`     | string  | no       | JSON array of action buttons (`view`, `broadcast`, `http`, `copy`).|
| `sequence_id` | string  | no       | Update an existing notification with this sequence ID.             |

Publishing uses the ntfy **JSON publish API**: the tool POSTs a JSON body to
`NTFY_URL` and the topic is carried inside the body. This makes the flat set of
optional arguments above possible without inventing a second tool.

Example call from the LLM side:

> Call `send_notification` with:
> `message="Backup finished"`, `title="Backup"`, `priority="high"`,
> `tags="white_check_mark"`, `click="https://ci.example.com/run/42"`.

Successful result (the `id` can later be passed back as `sequence_id`):

```
Notification sent (HTTP 200, id=abc123)
```

On failure (invalid arguments, network error, non-2xx response from ntfy),
the tool returns `isError: true` with a textual description of the problem.

## Versioning

The binary embeds its version through a linker flag:

```bash
go build -ldflags "-X main.version=$(git describe --tags --always --dirty=-dev)" -o ntfy-mcp .
```

The default (when built without ldflags, e.g. `go run .` or `go test`) is `dev`.
The value is reported to MCP clients in the `initialize` response as
`serverInfo.version`, which makes it easy to verify the injected version
end-to-end:

```bash
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}' \
  | NTFY_TOPIC=alerts ./bin/ntfy-mcp
```

Since **1.1.0** the tool publishes via the ntfy JSON API and accepts the
optional publish fields listed above. The contract is backwards-compatible
with 1.0.0: the only required argument is still `message`.

## Testing

```bash
make test           # go test -v -race -coverprofile=coverage.out ./...
make check          # go fmt + go vet + go test
```

The tests require no network: ntfy is replaced by `httptest.Server`, so they
run entirely in-process. Cases covered:

- successful delivery and verification of the request line, body, and headers
  (`Content-Type`, `Title`, `Priority`, `Tags`, `Authorization`);
- trailing-slash normalisation in `NTFY_URL`;
- missing and empty `message` argument;
- non-2xx response from ntfy;
- missing `Authorization` header when `NTFY_TOKEN` is unset;
- malformed `NTFY_URL` rejected at request construction time;
- `loadConfig`: required `NTFY_TOPIC`, default `NTFY_URL`, overrides;
- `run`: config-error path returns before `ServeStdio`;
- `newServer`: constructor does not panic and returns a non-nil server;
- `version`: defaults to a non-empty string;
- extended publish fields (`click`, `icon`, `markdown`, `delay`, `attach`,
  `filename`, `email`, `call`, `cache=false`, `firebase=false`, `actions`,
  `sequence_id`) end up in the JSON body;
- malformed `actions` JSON is rejected before any network call;
- trailing-slash normalisation on `NTFY_URL` (path stays `/`, topic travels
  in the body).

Current coverage is around **93%** of statements, with `loadConfig` and
`newServer` at 100% and `sendNotificationHandler` at ~98%.

`main` and the stdio-serving branch of `run` are intentionally not unit-tested:
they require an actual process boundary (`os.Exit`, `os.Stdin`). If you ever need
end-to-end coverage there, the right home for such a test is a separate file
tagged `//go:build integration` that shells out to the built binary.

## Limitations

- `stdio` transport only (no HTTP/SSE).
- `http.DefaultClient` is used without a per-request timeout. That is fine for
  local MCP sessions, but consider adding `context.WithTimeout` in the handler
  if your network is unreliable.
- Attachments are supported by URL only (`attach` + `filename`). Uploading
  files would require multipart handling and temporary storage, which breaks
  the stateless proxy design.
- Templates and multi-topic publishing are intentionally not supported.

## License

MIT
