# Implementation Tasks — Bidirectional Slack MCP Server

Reference: `slack-mcp-bidirectional-spec.md` for full design, `CLAUDE.md` for guidelines.

## Status Key

- [ ] Not started
- [x] Completed

---

## Completed

- [x] Update spec env vars to match upstream (`SLACK_MCP_HOST`/`SLACK_MCP_PORT`, defaults `127.0.0.1`/`13080`)
- [x] Update spec to note `reactions_add`/`reactions_remove` already exist upstream
- [x] Update `CLAUDE.md` env var table to match upstream
- [x] Create `.devcontainer/devcontainer.json` (Go 1.24, port 13080)

### Task 5: Verify Build — DONE

- [x] Install Go 1.24+ locally (Windows, `C:\Program Files\Go\bin\go.exe`)
- [x] Run `go build -o slack-mcp-server.exe ./cmd/slack-mcp-server` — compiles clean
- [x] Tests have pre-existing Windows failures (syscall.Setpgid/Kill) — not related to our changes

### Task 6: Socket Mode Client — DONE

- [x] Created `pkg/events/socketmode.go`
- [x] `SocketModeClient` struct wrapping `socketmode.Client`
- [x] `Start(ctx)` method: connects, listens for EventTypeEventsAPI, acknowledges, routes events
- [x] Fixed: `slack.OptionAppLevelToken()` goes on `slack.Client`, not `socketmode.Client`
- [x] Fixed: Files extracted from raw JSON payload (not `ev.Files` which doesn't exist on `MessageEvent`)
- [x] Socket Mode connects successfully with app token

### Task 7: Subscriber/Event Routing System — DONE

- [x] Created `pkg/events/router.go`
- [x] `SlackEvent`, `SlackFile`, `Subscriber`, `EventRouter` structs
- [x] `Subscribe()`, `Unsubscribe()`, `RouteEvent()`, `matches()` methods

### Task 8: Wire Socket Mode into main.go — DONE

- [x] Check `SLACK_MCP_ENABLE_EVENTS` and `SLACK_MCP_APP_TOKEN` env vars
- [x] Create `EventRouter` and `SocketModeClient` when enabled
- [x] Start Socket Mode client in goroutine
- [x] Pass `EventRouter` to MCP server
- [x] Fixed: `SLACK_MCP_XOXB_TOKEN` required when events enabled (for Socket Mode client)

### Task 9: `slack_wait_for_event` Tool — DONE

- [x] Created `pkg/handler/events.go` with `EventsHandler`
- [x] `WaitForEventHandler` parses channels, include_reactions, timeout_seconds
- [x] Subscribes, waits with select on ResultChan/timeout/ctx.Done()
- [x] Returns JSON matching spec output schema
- [x] Fixed: uses `request.GetArguments()` instead of `.Params.Arguments` indexing
- [x] Registered in `server.go`

### Task 10: `slack_create_channel` Tool — DONE

- [x] Created `pkg/handler/channels_manage.go` with `CreateChannelHandler`
- [x] Idempotent: handles `name_taken` error by returning existing channel
- [x] Supports `is_private` and `description` params
- [x] Registered in `server.go` with destructive hint

### Task 11: `slack_upload_file` Tool — DONE

- [x] Created `pkg/handler/files.go` with `UploadFileHandler`
- [x] Uses `slack.UploadFileV2` API
- [x] Supports channel_id, filename, content, content_type, title, initial_comment, thread_ts
- [x] Registered in `server.go` with destructive hint

### Task 12: `slack_download_file` Tool — DONE

- [x] Created `pkg/handler/files.go` with `DownloadFileHandler`
- [x] Gets file info, fetches content via authenticated HTTP
- [x] Returns content inline (text as string) or saves to disk
- [x] Fixed: uses `bytes.Buffer` as `io.Writer` (not `*[]byte`)
- [x] Registered in `server.go` with read-only hint

### Task 13: Register All New Tools in server.go — DONE

- [x] All 4 new tools registered with proper schemas and hint annotations
- [x] `slack_wait_for_event` conditionally registered when Socket Mode enabled

### Task 14: Build and Smoke Test — DONE

- [x] `go build` compiles clean
- [x] Socket Mode connects with app token
- [x] `slack_wait_for_event` — blocks, receives message, returns correctly
- [x] `slack_create_channel` — creates channel, idempotent on retry (returns `already_existed: true`)
- [x] `slack_upload_file` — uploads file to channel
- [x] `slack_download_file` — downloads file by ID, returns content
- [x] `channels_list` — lists public channels
- [x] `conversations_history` — reads channel history
- [x] `conversations_replies` — reads thread replies
- [x] `conversations_add_message` — posts messages and threaded replies
- [x] `reactions_add` / `reactions_remove` — add/remove emoji reactions

### Task 15: `/slack-listen` Skill — DONE

- [x] Created `.claude/skills/slack-listen/SKILL.md`
- [x] Created `.claude/skills/slack-listen/instruction.md`
- [x] Supports event mode (foreground, Socket Mode) and poll mode (background-compatible)
- [x] Tested foreground event listener — works end-to-end
- [x] Background poll mode limited by Claude Code agent infrastructure (agents don't loop reliably)

---

## Remaining / Future Work

- [ ] **Token rotation** — Slack tokens were exposed in conversation logs, need to be regenerated
- [ ] **Graceful shutdown on Windows** — Ctrl+C doesn't cleanly stop the Go server
- [ ] **`conversations_invite` tool** — missing, needed to invite users to created channels
- [ ] **`.slack-listener.json` default config** — create project-level config for `/slack-listen`
- [ ] **Unit tests** — add tests for new handlers and event routing
- [ ] **Integration tests** — automated tests with Slack API (requires tokens)

---

## Notes

- **Existing code patterns:** All handlers take `*provider.ApiProvider` + `*zap.Logger`. Follow the same constructor pattern as `NewConversationsHandler()` and `NewChannelsHandler()`.
- **Channel resolution:** The provider has channel name → ID resolution in its cache. Use `provider.ResolveChannelID()` or check how `ConversationsHistoryHandler` resolves `channel_id` params that start with `#`.
- **Error handling:** Return `mcp.CallToolResult` with `IsError: true` for Slack API errors. See existing handlers for the pattern.
- **Slack client access:** Use `provider.Slack()` to get the `*slack.Client`. The `slack-go/slack` package has all needed methods.
- **Socket Mode:** `slack-go/slack/socketmode` package handles WebSocket connection, reconnection, and heartbeats. We just need to wire up event handling.
- **Env var safety guards:** `SLACK_MCP_ADD_MESSAGE_TOOL` and `SLACK_MCP_REACTION_TOOL` are upstream safety features. Set to `true` or comma-separated channel IDs.
