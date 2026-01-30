# Slack Listener Skill

Monitors a Slack channel using a background watcher agent and responds to messages.

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
3. Creates a persistent subscription via `slack_subscribe`
4. Launches a background watcher agent that polls for events
5. When the watcher detects a real user message, it exits and returns the event
6. Parent agent handles the message:
   - Acknowledges with :eyes: reaction
   - Evaluates and responds in-thread
   - Marks complete with :white_check_mark: or :x:
7. Restarts the background watcher
8. Loops

## Platform Notes

This skill relies on background Task agents that exit to return events to the parent.
**Use the Claude Code CLI** (`claude` command) for best results. The CLI properly
propagates background agent return values and detects task completion via polling.

The **VS Code extension** has limited background task support — agent return values
may be empty and task completion is not reliably detected. If using VS Code, consider
a manual polling workflow: use `slack_get_events` directly instead of the background
watcher pattern.
