# Slack MCP Bidirectional Server

Fork of korotovsky/slack-mcp-server adding real-time bidirectional Slack communication.

## Key Files

- `slack-mcp-bidirectional-spec.md` — Complete design spec (READ FIRST)
- `claude-dev-prompt.md` — Implementation guidance and phases

## Guidelines

- **Read the spec before making changes** — all design decisions are documented there
- **Preserve backward compatibility** — existing tools must continue to work
- **Follow existing code patterns** — match the style of the upstream codebase
- **Test incrementally** — verify each tool works before moving to the next
- **Never commit secrets** — tokens and API keys stay in `.env` (gitignored)

## Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `SLACK_MCP_XOXB_TOKEN` | Yes | Bot token (xoxb-...) |
| `SLACK_MCP_APP_TOKEN` | For events | App token for Socket Mode (xapp-...) |
| `SLACK_MCP_ENABLE_EVENTS` | No | Set `true` to enable Socket Mode |
| `SLACK_MCP_HOST` | No | Bind address (default: `127.0.0.1`, use `0.0.0.0` for Docker) |
| `SLACK_MCP_PORT` | No | Port (default: `13080`) |
| `SLACK_MCP_API_KEY` | No | If set, requires Bearer auth |

## Quick Commands

```bash
# Build
go build -o slack-mcp-server ./cmd/slack-mcp-server

# Run
./slack-mcp-server --transport sse

# Test connection (default port is 13080)
claude mcp add slack-dev --transport sse http://localhost:13080/sse
```
