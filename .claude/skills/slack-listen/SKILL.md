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
| `--mode <subscribe\|event\|poll>` | Listening mode: subscribe (persistent, default), event (ephemeral), or poll (history-based) |

## Examples

```
/slack-listen                            # Use .slack-listener.json
/slack-listen --channel #cala-v2         # Override to specific channel
/slack-listen --create #experiment       # Create public channel and monitor
/slack-listen --create #secret --private # Create private channel
/slack-listen --channel #dev --mode subscribe  # Persistent subscription (default)
/slack-listen --channel #dev --mode poll       # History polling fallback
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
   - Acknowledges with eyes reaction
   - Evaluates and handles request
   - Marks complete with white_check_mark or x
   - Loops
