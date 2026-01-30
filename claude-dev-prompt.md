# Slack MCP Bidirectional Server - Development Prompt

You are helping me develop a bidirectional Slack MCP server by forking and extending korotovsky/slack-mcp-server.

## Project Context

I have cloned korotovsky/slack-mcp-server and placed a detailed specification at `slack-mcp-bidirectional-spec.md` in the project root. Read this spec thoroughly before starting - it contains all the design decisions, tool schemas, and implementation details.

## Development Environment

We're using VS Code Dev Containers. First priority is setting up the dev environment:

1. Create `.devcontainer/devcontainer.json` with:
   - Go 1.22+ base image
   - Node.js (for claude-code CLI)
   - Git
   - Mount SSH keys from host for git operations
   - Forward port 3000
   - Pass through ANTHROPIC_API_KEY from host environment

2. Verify the existing codebase builds and runs before making changes

## Implementation Phases

### Phase 1: Foundation (do these first)

1. **Understand the codebase** - Explore the existing structure, find where:
   - SSE server is configured (fix host binding to respect SLACK_MCP_SSE_HOST)
   - Tools are registered (we'll add new ones here)
   - Slack API calls are made (understand the patterns)

2. **Fix host binding** - Make server respect `SLACK_MCP_SSE_HOST` and `SLACK_MCP_SSE_PORT` env vars, defaulting to `0.0.0.0:3000`

3. **Add API key auth** - If `SLACK_MCP_API_KEY` is set, require `Authorization: Bearer <key>` header on all requests

### Phase 2: Socket Mode Integration

4. **Add Socket Mode client** - Using `github.com/slack-go/slack/socketmode`:
   - Connect when `SLACK_MCP_APP_TOKEN` is provided and `SLACK_MCP_ENABLE_EVENTS=true`
   - Subscribe to: `message.channels`, `message.groups`, `message.im`, `reaction_added`
   - Acknowledge all events properly

5. **Implement event routing** - Create subscriber system:
   - Track waiting `slack_wait_for_event` tool calls per connection
   - Route incoming Slack events to matching subscribers based on channel filter
   - Handle connection cleanup when SSE connections drop

### Phase 3: New Tools

6. **`slack_wait_for_event`** - Blocking tool that:
   - Accepts: channels (array), include_reactions (bool), timeout_seconds (int)
   - Blocks until matching event or timeout
   - Returns: event_type, channel info, user info, text, files array

7. **`slack_create_channel`** - Create channels:
   - Accepts: name, is_private, description
   - Idempotent (return existing channel if exists)
   - Uses `conversations.create` API

8. **`slack_upload_file`** - Upload files:
   - Accepts: channel_id, filename, content, content_type, title, initial_comment, thread_ts
   - Uses `files.uploadV2` API

9. **`attachment_get_data`** - Download file content:
   - Accepts: file_id
   - Returns structured JSON: file_id, filename, mimetype, size, encoding, content
   - Text files returned as plain text, binary as base64 (5MB limit)

10. **`slack_add_reaction`** - Add emoji reactions:
    - Accepts: channel_id, message_ts, emoji
    - Uses `reactions.add` API

## Testing Strategy

- Test each tool individually with Claude Code after implementing
- Test Socket Mode connection with a real Slack workspace
- Test multi-connection scenarios (multiple Claude instances)
- Verify event routing to correct subscribers

## Key Files to Create/Modify

Based on exploring the codebase, identify and list the files that need changes. Typical structure might include:
- `cmd/*/main.go` - Entry point, server setup
- `internal/server/` - SSE server, connection handling
- `internal/tools/` - MCP tool implementations
- `internal/slack/` - Slack API client wrappers

## Important Notes

- Keep backward compatibility with existing tools
- Follow existing code patterns and style
- Add proper error handling and logging
- Document new environment variables
- The spec has detailed JSON schemas for all tool inputs/outputs - follow them exactly

## Getting Started

1. First, read `slack-mcp-bidirectional-spec.md` completely
2. Explore the existing codebase structure
3. Set up the dev container
4. Build and run the existing code to verify it works
5. Then start with Phase 1 changes

Ask me questions if anything in the spec is unclear or if you need to make design decisions not covered by the spec.
