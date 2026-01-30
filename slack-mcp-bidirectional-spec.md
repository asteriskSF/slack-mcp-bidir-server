# Bidirectional Slack MCP Server

**Fork of:** [korotovsky/slack-mcp-server](https://github.com/korotovsky/slack-mcp-server)

A self-hosted MCP server that provides bidirectional communication between Claude Code and Slack. Claude can read/write Slack, and Slack events trigger Claude responses in real-time.

---

## Problem Statement

Current Slack MCP solutions are unidirectional:
- **korotovsky/slack-mcp-server** — Claude can call Slack (read/write), but can't receive notifications
- **mpociot/claude-code-slack-bot** — Slack can trigger Claude, but runs Claude locally (not remotely)

There's no self-hosted solution that:
1. Lets Claude Code read/write Slack via MCP
2. Receives messages in real-time via Socket Mode
3. Supports multiple Claude instances monitoring different channels
4. Works with remote Claude Code instances (e.g., WSL, SSH)

---

## Goals

1. **Bidirectional communication** — Claude → Slack and Slack → Claude
2. **Real-time events** — No polling, instant message notifications via Socket Mode
3. **Channel-based routing** — Each Claude instance monitors its own channel(s)
4. **Multi-connection support** — Multiple Claude instances can connect simultaneously
5. **Self-hosted** — Runs on home lab infrastructure (Unraid, Docker, etc.)
6. **Simple model** — Monitor channels, respond to messages. No complex thread tracking.

---

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│  Unraid / Docker Host                                           │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │  Bidirectional Slack MCP Server                           │  │
│  │                                                           │  │
│  │  ┌─────────────────┐     ┌─────────────────────────────┐  │  │
│  │  │  MCP Server     │     │  Socket Mode Listener       │  │  │
│  │  │  (SSE/HTTP)     │     │  (Slack WebSocket)          │  │  │
│  │  │                 │     │                             │  │  │
│  │  │  Connections:   │     │  Receives ALL events from   │  │  │
│  │  │  - Cala WSL     │     │  channels bot is in         │  │  │
│  │  │  - CIYE WSL     │     │                             │  │  │
│  │  │  - ...          │     │                             │  │  │
│  │  │                 │     │                             │  │  │
│  │  │  Each connection│◄────┤  Routes events to waiting   │  │  │
│  │  │  has its own    │     │  connections based on their │  │  │
│  │  │  channel filter │     │  channel filters            │  │  │
│  │  └────────┬────────┘     └─────────────────────────────┘  │  │
│  │           │                                               │  │
│  │           │ :3000 (0.0.0.0)                               │  │
│  └───────────┼───────────────────────────────────────────────┘  │
└──────────────┼──────────────────────────────────────────────────┘
               │
               │ SSE/HTTP + Auth Header
               │
    ┌──────────┴──────────┐
    │                     │
    ▼                     ▼
┌─────────────┐     ┌─────────────┐
│ WSL: Cala   │     │ WSL: CIYE   │
│ Claude Code │     │ Claude Code │
│             │     │             │
│ Monitors:   │     │ Monitors:   │
│ #cala-dev   │     │ #ciye-dev   │
└─────────────┘     └─────────────┘
```

---

## Core Concept: Channel-Based Monitoring

**Simple model:** Claude monitors specific channels. Any message in those channels triggers a notification.

- No thread tracking
- No message ownership tracking  
- Claude's skill logic decides what to respond to
- Server just routes messages to connections based on channel filters

```
Message in #cala-dev
    │
    ├── Connection 1 monitoring ["#cala-dev"] → deliver ✓
    ├── Connection 2 monitoring ["#ciye-dev"] → skip
    └── Connection 3 monitoring ["#cala-dev", "#general"] → deliver ✓
```

Same message delivered to ALL connections monitoring that channel.

---

## Features

### Phase 1: Core (MVP)

#### 1.1 Host Binding & API Key Authentication — Already Implemented Upstream

The upstream codebase already supports configurable host/port binding and API key authentication. No changes needed.

| Variable | Default | Description |
|----------|---------|-------------|
| `SLACK_MCP_HOST` | `127.0.0.1` | Interface to bind (set to `0.0.0.0` for Docker/remote access) |
| `SLACK_MCP_PORT` | `13080` | Port to listen on |
| `SLACK_MCP_API_KEY` | (none) | If set, requires `Authorization: Bearer <key>` header (constant-time comparison) |

> **Note for Docker/Unraid:** You must set `SLACK_MCP_HOST=0.0.0.0` in your container environment,
> otherwise the server only listens on loopback and is unreachable from outside the container.

```bash
claude mcp add slack --transport sse http://server:13080/sse \
  --header "Authorization: Bearer your-secret-key"
```

#### 1.3 Socket Mode Integration

| Variable | Default | Description |
|----------|---------|-------------|
| `SLACK_MCP_APP_TOKEN` | (none) | Slack app token (xapp-...) for Socket Mode |
| `SLACK_MCP_ENABLE_EVENTS` | `false` | Enable Socket Mode event listener |

**Events subscribed:**
- `message.channels` — Messages in public channels
- `message.groups` — Messages in private channels
- `message.im` — Direct messages
- `reaction_added` — Emoji reactions (for approval workflows)

#### 1.4 Connection Management

**Multiple simultaneous connections supported.** Each connection can have active `slack_wait_for_event` calls with different channel filters.

**Connection lifecycle:**
- Subscriptions scoped to connection lifetime
- When connection drops, all waiting tool calls for that connection are canceled
- On reconnect, agent must re-establish subscriptions (no persistence)
- Multiple active `slack_wait_for_event` calls per connection allowed

**Server state model:**
```go
type Connection struct {
    ID            string
    WaitingCalls  []*WaitingCall
}

type WaitingCall struct {
    ID          string
    Channels    []string      // Channel filter
    IncludeReactions bool
    ResultChan  chan Event    // Delivers matching events
    CreatedAt   time.Time
}

// On event from Slack:
// - Iterate all connections, all waiting calls
// - Deliver to each call where event.channel matches call.Channels
// - Same event can be delivered to multiple calls/connections
```

#### 1.5 New Tool: `slack_wait_for_event`
> **Note:** `reactions_add` and `reactions_remove` tools already exist in the upstream codebase (see section 1.8).

**The core bidirectional feature.** Blocks until a message arrives in monitored channels.

**Input schema:**
```json
{
  "type": "object",
  "properties": {
    "channels": {
      "type": "array",
      "items": { "type": "string" },
      "description": "Channel IDs or names to monitor (e.g., ['#cala-dev'])"
    },
    "include_reactions": {
      "type": "boolean",
      "default": false,
      "description": "Also notify on reaction events"
    },
    "timeout_seconds": {
      "type": "integer",
      "default": 300,
      "description": "Max time to wait. 0 = no timeout."
    }
  },
  "required": ["channels"]
}
```

**Output schema:**
```json
{
  "type": "object",
  "properties": {
    "event_type": {
      "type": "string",
      "enum": ["message", "reaction", "timeout"]
    },
    "channel_id": { "type": "string" },
    "channel_name": { "type": "string" },
    "message_ts": { "type": "string" },
    "thread_ts": { 
      "type": "string",
      "description": "Parent thread ts, null if not in a thread"
    },
    "user_id": { "type": "string" },
    "user_name": { "type": "string" },
    "text": { "type": "string" },
    "is_thread_reply": { "type": "boolean" },
    "is_bot_message": { "type": "boolean" },
    "reaction": {
      "type": "string", 
      "description": "Emoji name, only for reaction events"
    },
    "files": {
      "type": "array",
      "description": "File attachments, if any",
      "items": {
        "type": "object",
        "properties": {
          "file_id": { "type": "string" },
          "filename": { "type": "string" },
          "filetype": { "type": "string" },
          "mimetype": { "type": "string" },
          "size_bytes": { "type": "integer" }
        }
      }
    }
  }
}
```

**Example call:**
```json
{
  "channels": ["#cala-dev"],
  "include_reactions": false,
  "timeout_seconds": 300
}
```

**Example response:**
```json
{
  "event_type": "message",
  "channel_id": "C0123456",
  "channel_name": "#cala-dev",
  "message_ts": "1234567890.123456",
  "thread_ts": "1234567890.100000",
  "user_id": "U789",
  "user_name": "anthony",
  "text": "The LED is still flickering after the fix",
  "is_thread_reply": true,
  "is_bot_message": false
}
```

#### 1.6 New Tool: `slack_create_channel`

Create channels dynamically. Enables agents to spin up dedicated channels.

**Slack API:** `conversations.create`

**Required scopes:** `channels:manage` (public), `groups:write` (private)

**Input schema:**
```json
{
  "type": "object",
  "properties": {
    "name": {
      "type": "string",
      "description": "Channel name (without #, lowercase, no spaces, max 80 chars)"
    },
    "is_private": {
      "type": "boolean",
      "default": false
    },
    "description": {
      "type": "string",
      "description": "Channel purpose/description"
    }
  },
  "required": ["name"]
}
```

**Output schema:**
```json
{
  "type": "object",
  "properties": {
    "ok": { "type": "boolean" },
    "channel_id": { "type": "string" },
    "channel_name": { "type": "string" },
    "already_existed": { 
      "type": "boolean",
      "description": "True if channel existed and was returned instead of created"
    }
  }
}
```

**Behavior:**
- If channel already exists, return it (idempotent)
- Bot automatically joins the created channel

#### 1.7 New Tool: `slack_upload_file`

Upload files (code, logs, images) to channels.

**Slack API:** `files.uploadV2`

**Required scope:** `files:write`

**Input schema:**
```json
{
  "type": "object",
  "properties": {
    "channel_id": { "type": "string" },
    "filename": { "type": "string" },
    "content": { 
      "type": "string", 
      "description": "Text content or base64-encoded binary" 
    },
    "content_type": { 
      "type": "string", 
      "default": "text/plain" 
    },
    "title": { "type": "string" },
    "initial_comment": { "type": "string" },
    "thread_ts": { "type": "string" }
  },
  "required": ["channel_id", "filename", "content"]
}
```

#### 1.8 ~~New Tool: `slack_add_reaction`~~ — Already Exists Upstream

The upstream codebase already provides `reactions_add` and `reactions_remove` tools.
No new tool needed. The `/slack-listen` skill should use the existing `reactions_add` tool name.

**Existing tool names:** `reactions_add`, `reactions_remove`

**Existing input schema (upstream):**
```json
{
  "channel_id": "string (required) — Channel ID or name (#general, @username_dm)",
  "timestamp": "string (required) — Message timestamp (1234567890.123456)",
  "emoji": "string (required) — Emoji name without colons (e.g., 'thumbsup', 'eyes')"
}
```

#### 1.9 New Tool: `slack_download_file`

Download files shared in Slack channels. Enables Claude to process logs, code, documents, and images dropped into channels.

**Slack API:** Files accessed via `url_private` with bot token authorization

**Required scope:** `files:read`

**Input schema:**
```json
{
  "type": "object",
  "properties": {
    "file_id": {
      "type": "string",
      "description": "Slack file ID (from message event files array)"
    },
    "save_path": {
      "type": "string",
      "description": "Optional local path to save file. If omitted, returns content directly."
    }
  },
  "required": ["file_id"]
}
```

**Output schema:**
```json
{
  "type": "object",
  "properties": {
    "ok": { "type": "boolean" },
    "filename": { "type": "string" },
    "filetype": { "type": "string" },
    "mimetype": { "type": "string" },
    "size_bytes": { "type": "integer" },
    "content": {
      "type": "string",
      "description": "File content as text (for text files) or base64 (for binary). Omitted if save_path provided."
    },
    "saved_to": {
      "type": "string",
      "description": "Local path where file was saved. Only present if save_path was provided."
    }
  }
}
```

**Example: Download and return content**
```json
// Request
{ "file_id": "F0123ABCDEF" }

// Response
{
  "ok": true,
  "filename": "crash.log",
  "filetype": "text",
  "mimetype": "text/plain",
  "size_bytes": 4523,
  "content": "2024-01-15 03:42:01 ERROR: Battery voltage below threshold..."
}
```

**Example: Download and save to disk**
```json
// Request
{ "file_id": "F0123ABCDEF", "save_path": "/tmp/schematic.pdf" }

// Response
{
  "ok": true,
  "filename": "board_v2.pdf",
  "filetype": "pdf",
  "mimetype": "application/pdf",
  "size_bytes": 245632,
  "saved_to": "/tmp/schematic.pdf"
}
```

---

### Phase 2: Enhanced Features

| Tool | Slack API | Purpose |
|------|-----------|---------|
| `slack_pin_message` | `pins.add` | Pin important messages |
| `slack_add_bookmark` | `bookmarks.add` | Add links to channel |
| `slack_set_topic` | `conversations.setTopic` | Update channel topic |
| `slack_add_reminder` | `reminders.add` | Set follow-up reminders |
| `slack_get_user_info` | `users.info` | Look up user details |
| `slack_archive_channel` | `conversations.archive` | Clean up old channels |

---

## Complete OAuth Scopes

### Bot Token Scopes (xoxb-)

**Phase 1 Required:**
| Scope | Purpose |
|-------|---------|
| `channels:history` | Read public channel messages |
| `channels:manage` | Create public channels |
| `channels:read` | List public channels |
| `chat:write` | Post messages |
| `files:read` | Download files |
| `files:write` | Upload files |
| `groups:history` | Read private channel messages |
| `groups:read` | List private channels |
| `groups:write` | Create private channels |
| `im:history` | Read DM history |
| `im:read` | List DMs |
| `reactions:read` | Read reactions |
| `reactions:write` | Add reactions |
| `users:read` | Get user info |

**Phase 1 Recommended:**
| Scope | Purpose |
|-------|---------|
| `chat:write.public` | Post to channels without joining |
| `mpim:history` | Group DM history |
| `mpim:read` | List group DMs |

**Phase 2 (for enhanced features):**
| Scope | Purpose |
|-------|---------|
| `bookmarks:write` | Add channel bookmarks |
| `pins:write` | Pin messages |
| `reminders:write` | Create reminders |

### App-Level Token (xapp-)

| Scope | Purpose |
|-------|---------|
| `connections:write` | Socket Mode connection |

---

## Claude Code Skill: `/slack-listen`

### Skill Files

Place in `~/.claude/skills/slack-listen/` or project's `.claude/skills/slack-listen/`.

**SKILL.md:**
```markdown
# Slack Listener Skill

Monitors a Slack channel and responds to messages.

## Usage

```
/slack-listen [options]
```

## Options

| Option | Description |
|--------|-------------|
| `--channel <n>` | Monitor specific channel (overrides config) |
| `--create <n>` | Create channel if doesn't exist, then monitor |
| `--private` | With --create, makes channel private |
| `--config <path>` | Use alternate config file |

## Examples

```
/slack-listen                            # Use .slack-listener.json
/slack-listen --channel #cala-v2         # Override to specific channel
/slack-listen --create #experiment       # Create public channel and monitor
/slack-listen --create #secret --private # Create private channel
```

## Configuration

Create `.slack-listener.json` in project root:

```json
{
  "channel": "#cala-dev",
  "project_context": "Cala Health charging station firmware",
  "auto_handle": ["question", "bug", "review"],
  "require_approval": ["deploy", "merge", "delete"]
}
```

## Behavior

1. Resolves target channel (from args or config)
2. Creates channel if --create specified
3. Enters listen loop:
   - Waits for messages in channel
   - Acknowledges with 👀 reaction
   - Evaluates and handles request
   - Marks complete with ✅ or ❌
   - Loops
```

**instruction.md:**
```markdown
You are a Slack-integrated assistant for this project. Monitor a Slack channel
and respond to messages from users.

## Startup

1. Parse command arguments:
   - --channel <n>: Override channel to monitor
   - --create <n>: Create channel first, then monitor
   - --private: Used with --create for private channel
   - --config <path>: Alternate config file path

2. Load configuration:
   - If --config specified, load that file
   - Otherwise try .slack-listener.json in project root
   - If no config, use defaults

3. Resolve target channel:
   - If --create specified:
     a. Call slack_create_channel with the name
     b. If --private flag, set is_private: true
     c. Use returned channel_id
   - Else if --channel specified:
     a. Use that channel name/ID
   - Else:
     a. Use "channel" from config file
     b. If not set, error: "No channel specified"

4. Announce startup:
   - Post message to channel: "🤖 Claude is now listening. Mention me or ask questions!"

## Configuration Schema

```json
{
  "channel": "#channel-name",
  "project_context": "Description of this project",
  "auto_handle": ["question", "bug", "review"],
  "require_approval": ["deploy", "merge", "delete"],
  "include_reactions": false
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| channel | string | required | Channel to monitor |
| project_context | string | "" | Project description for context |
| auto_handle | string[] | ["question"] | Request types to handle automatically |
| require_approval | string[] | [] | Request types needing confirmation |
| include_reactions | boolean | false | Also listen for reaction events |

## Main Loop

```
while true:
    1. Call slack_wait_for_event
    2. Handle timeout → continue
    3. Skip bot messages → continue
    4. Acknowledge message
    5. Evaluate request type
    6. Handle or request approval
    7. Post response
    8. Mark complete
```

### Step 1: Wait for Event

```json
{
  "channels": ["<resolved_channel>"],
  "include_reactions": <from_config>,
  "timeout_seconds": 300
}
```

### Step 2: Handle Timeout

If event_type is "timeout":
- Continue loop (keep listening)

### Step 3: Skip Bot Messages

If is_bot_message is true:
- Continue loop (don't respond to bots, including yourself)

### Step 3b: Handle File Attachments

If event.files is non-empty:
1. For each file in event.files:
   - Call slack_download_file with file_id
   - Store content for analysis
2. Include file contents in your context when evaluating the request

**File type handling:**
| File Type | Action |
|-----------|--------|
| `.log`, `.txt` | Parse for errors, patterns, key events |
| `.c`, `.h`, `.py`, `.js` | Code review, bug search, style check |
| `.json`, `.yaml`, `.xml` | Parse and validate, check for issues |
| `.pdf` | Extract text, summarize content |
| `.png`, `.jpg`, `.gif` | Describe image (if vision available) |
| `.csv`, `.xlsx` | Parse data, summarize, find anomalies |
| Binary/unknown | Note file received, ask user what to do |

### Step 4: Acknowledge Receipt

Call slack_add_reaction:
- channel_id: event.channel_id
- message_ts: event.message_ts
- emoji: "eyes"

This shows the user you've seen their message.

### Step 5: Evaluate Request Type

Analyze the message text to classify:
- "question" — asking for information or help
- "bug" — reporting an issue to investigate
- "review" — requesting code/design review
- "deploy" — requesting deployment action
- "merge" — requesting merge/integration
- "delete" — requesting deletion of something
- "other" — unclear, ask for clarification

Consider project_context when classifying.

### Step 6: Handle or Request Approval

**If request type is in auto_handle:**
- Proceed to handle it

**If request type is in require_approval:**
1. Post to thread: "I can help with [describe task]. React with 👍 to proceed or ❌ to cancel."
2. Call slack_wait_for_event with:
   - channels: [event.channel_id]
   - include_reactions: true
   - timeout_seconds: 3600
3. Check if reaction on your approval message:
   - 👍 or ✅ → proceed
   - ❌ or 🚫 → post "Understood, cancelled." and continue loop
   - timeout → post "Request timed out, let me know if you still need this." and continue

**If unclear:**
- Post to thread asking for clarification
- Continue loop (will pick up their reply)

### Step 7: Execute and Respond

Based on request type:

**question:**
- Research using available tools and project knowledge
- Post answer to thread

**bug:**
- Investigate code, search for related issues
- Post findings and suggested fix to thread
- If fix is straightforward and in auto_handle, implement it

**review:**
- Analyze the code/design mentioned
- Post review comments to thread

**deploy/merge/delete:**
- Only reach here if approved
- Execute the action
- Post confirmation to thread

For any task producing artifacts (code, logs, diffs):
- Use slack_upload_file to share in thread

### Step 8: Mark Complete

Call slack_add_reaction:
- channel_id: event.channel_id  
- message_ts: event.message_ts
- emoji: "white_check_mark" (success) or "x" (failure)

### Step 9: Loop

Go back to Step 1.

## Error Handling

- MCP connection fails → wait 30s, retry
- Slack API error → log, continue listening
- Unhandled exception → post error to channel, continue

## Example Interaction

**User posts in #cala-dev:**
> The charging LED blinks twice then stops, should blink continuously

**You:**
1. Receive event (message in #cala-dev)
2. React with 👀
3. Classify as "bug" (in auto_handle)
4. Investigate LED code, find timer issue
5. Post to thread: "Found it - the LED timer stops after 2 cycles because [explanation]. Here's the fix..."
6. Upload diff with slack_upload_file
7. React with ✅
8. Continue listening
```

---

## Multi-Agent Setup

Share the same `.slack-listener.json` but override channels:

**Shared config in repo:**
```json
{
  "project_context": "Embedded firmware project",
  "auto_handle": ["question", "bug"],
  "require_approval": ["deploy"]
}
```

**Agent 1 (Cala dev):**
```bash
/slack-listen --channel #cala-dev
```

**Agent 2 (Cala staging):**
```bash
/slack-listen --channel #cala-staging
```

**Agent 3 (new experiment):**
```bash
/slack-listen --create #cala-experiment
```

All three use the same behavior config, but monitor different channels.

---

## Tool Summary

### Phase 1 (MVP)

| Tool | Slack API | Origin |
|------|-----------|--------|
| `channels_list` | `conversations.list` | Upstream |
| `conversations_history` | `conversations.history` | Upstream |
| `conversations_replies` | `conversations.history` (thread) | Upstream |
| `conversations_add_message` | `chat.postMessage` | Upstream |
| `conversations_search_messages` | `search.messages` | Upstream (non-bot tokens only) |
| `reactions_add` | `reactions.add` | Upstream |
| `reactions_remove` | `reactions.remove` | Upstream |
| `slack_wait_for_event` | Socket Mode | **New** |
| `slack_create_channel` | `conversations.create` | **New** |
| `slack_upload_file` | `files.uploadV2` | **New** |
| `slack_download_file` | `url_private` fetch | **New** |

### Phase 2 (Enhanced)

| Tool | Slack API | Purpose |
|------|-----------|---------|
| `slack_pin_message` | `pins.add` | Pin important messages to channel |
| `slack_add_bookmark` | `bookmarks.add` | Add links to channel bookmarks bar |
| `slack_set_topic` | `conversations.setTopic` | Update channel topic/description |
| `slack_add_reminder` | `reminders.add` | Set reminders for follow-up |
| `slack_get_user_info` | `users.info` | Look up user details by ID |
| `slack_archive_channel` | `conversations.archive` | Archive unused channels |

---

## Deployment

### Docker Compose

```yaml
services:
  slack-mcp:
    image: ghcr.io/yourname/slack-mcp-bidirectional:latest
    container_name: slack-mcp
    restart: unless-stopped
    ports:
      - "13080:13080"
    environment:
      - SLACK_MCP_XOXB_TOKEN=${SLACK_MCP_XOXB_TOKEN}
      - SLACK_MCP_APP_TOKEN=${SLACK_MCP_APP_TOKEN}
      - SLACK_MCP_HOST=0.0.0.0
      - SLACK_MCP_PORT=13080
      - SLACK_MCP_ENABLE_EVENTS=true
      - SLACK_MCP_ADD_MESSAGE_TOOL=true
      - SLACK_MCP_API_KEY=${SLACK_MCP_API_KEY}
    volumes:
      - slack-mcp-cache:/root/.cache/slack-mcp-server

volumes:
  slack-mcp-cache:
```

### Environment File (`.env`)

```bash
SLACK_MCP_XOXB_TOKEN=xoxb-your-bot-token
SLACK_MCP_APP_TOKEN=xapp-your-app-token
SLACK_MCP_API_KEY=your-random-secret-key
```

---

## Slack App Setup

### 1. Create App

1. Go to [api.slack.com/apps](https://api.slack.com/apps)
2. **Create New App** → **From scratch**
3. Name: `Claude MCP`
4. Select workspace

### 2. OAuth Scopes

**OAuth & Permissions** → **Bot Token Scopes**

Add all Phase 1 scopes listed above.

### 3. Socket Mode

1. **Socket Mode** → Enable
2. Create app token with `connections:write`
3. Copy `xapp-...` token

### 4. Event Subscriptions

1. **Event Subscriptions** → Enable
2. Subscribe to bot events:
   - `message.channels`
   - `message.groups`
   - `message.im`
   - `reaction_added`

### 5. Install

1. **OAuth & Permissions** → **Install to Workspace**
2. Copy `xoxb-...` token

### 6. Invite Bot

In Slack:
```
/invite @Claude MCP
```

---

## Development

### Dev Container

`.devcontainer/devcontainer.json`:
```json
{
  "name": "Slack MCP Dev",
  "image": "golang:1.22",
  "features": {
    "ghcr.io/devcontainers/features/node:1": {},
    "ghcr.io/devcontainers/features/git:1": {}
  },
  "postCreateCommand": "npm install -g @anthropic-ai/claude-code && go mod download",
  "customizations": {
    "vscode": {
      "extensions": ["golang.go"]
    }
  },
  "forwardPorts": [3000],
  "mounts": [
    "source=${localEnv:USERPROFILE}/.ssh,target=/root/.ssh,type=bind,readonly"
  ],
  "remoteEnv": {
    "ANTHROPIC_API_KEY": "${localEnv:ANTHROPIC_API_KEY}"
  }
}
```

### Build

```bash
git clone https://github.com/yourname/slack-mcp-bidirectional.git
cd slack-mcp-bidirectional
go build -o slack-mcp-server ./cmd/slack-mcp-server
```

### Test

```bash
export SLACK_MCP_XOXB_TOKEN=xoxb-...
export SLACK_MCP_APP_TOKEN=xapp-...
export SLACK_MCP_ENABLE_EVENTS=true
./slack-mcp-server --transport sse
```

---

## Implementation Notes

### Socket Mode Event Flow

```
Slack Cloud
    │
    │ WebSocket (outbound from server, no inbound ports needed)
    ▼
┌─────────────────────────────────────────┐
│ Socket Mode Client                       │
│                                         │
│ 1. Receive event envelope               │
│ 2. Acknowledge envelope (required)      │
│ 3. Parse event payload                  │
│ 4. Route to matching subscribers        │
└─────────────────────────────────────────┘
    │
    │ For each waiting slack_wait_for_event call:
    │   if event.channel in call.channels:
    │     deliver event to call.ResultChan
    ▼
┌─────────────────────────────────────────┐
│ Waiting Tool Calls                       │
│                                         │
│ Call A: channels=["#cala-dev"]  ← match │
│ Call B: channels=["#ciye-dev"]  ← skip  │
└─────────────────────────────────────────┘
```

### Go Implementation Sketch

```go
// Subscriber represents a waiting slack_wait_for_event call
type Subscriber struct {
    ConnectionID     string
    Channels         map[string]bool  // channel IDs for O(1) lookup
    IncludeReactions bool
    ResultChan       chan *Event
    CreatedAt        time.Time
}

var (
    subscribers = make([]*Subscriber, 0)
    subMutex    sync.RWMutex
)

// Called by Socket Mode event handler
func routeEvent(event *SlackEvent) {
    subMutex.RLock()
    defer subMutex.RUnlock()
    
    for _, sub := range subscribers {
        if sub.matches(event) {
            // Non-blocking send (subscriber might have timed out)
            select {
            case sub.ResultChan <- event:
            default:
            }
        }
    }
}

func (s *Subscriber) matches(event *SlackEvent) bool {
    // Check channel filter
    if !s.Channels[event.ChannelID] {
        return false
    }
    // Check event type
    if event.Type == "reaction_added" && !s.IncludeReactions {
        return false
    }
    return true
}
```

---

## License

MIT

---

## Changelog

### v0.1.0 (Planned)

**Already in upstream (no changes needed):**
- Host binding via `SLACK_MCP_HOST` / `SLACK_MCP_PORT` env vars
- API key authentication via `SLACK_MCP_API_KEY`
- `reactions_add` and `reactions_remove` tools

**New in this fork:**
- Add Socket Mode integration
- Add `slack_wait_for_event` (channel-based blocking)
- Add `slack_create_channel`
- Add `slack_upload_file`
- Add `slack_download_file`
- `/slack-listen` skill with --channel and --create options
