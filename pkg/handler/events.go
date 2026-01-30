package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/korotovsky/slack-mcp-server/pkg/events"
	"github.com/korotovsky/slack-mcp-server/pkg/provider"
	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"
)

// EventsHandler handles the slack_wait_for_event tool.
type EventsHandler struct {
	apiProvider *provider.ApiProvider
	router      *events.EventRouter
	logger      *zap.Logger
}

// NewEventsHandler creates a new EventsHandler.
func NewEventsHandler(apiProvider *provider.ApiProvider, router *events.EventRouter, logger *zap.Logger) *EventsHandler {
	return &EventsHandler{
		apiProvider: apiProvider,
		router:      router,
		logger:      logger,
	}
}

// WaitForEventHandler blocks until a matching Slack event arrives or timeout.
func (h *EventsHandler) WaitForEventHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("WaitForEventHandler called", zap.Any("params", request.Params))

	if h.router == nil {
		return nil, errors.New("event routing is not enabled; set SLACK_MCP_ENABLE_EVENTS=true and provide SLACK_MCP_APP_TOKEN")
	}

	// Parse channels parameter
	args := request.GetArguments()
	channelsRaw, ok := args["channels"]
	if !ok {
		return nil, errors.New("channels parameter is required")
	}
	channelsSlice, ok := channelsRaw.([]interface{})
	if !ok {
		return nil, errors.New("channels must be an array of strings")
	}
	if len(channelsSlice) == 0 {
		return nil, errors.New("channels must contain at least one channel")
	}

	// Resolve channel names to IDs
	var channelIDs []string
	for _, ch := range channelsSlice {
		channelStr, ok := ch.(string)
		if !ok {
			return nil, errors.New("each channel must be a string")
		}
		resolved, err := h.resolveChannelID(channelStr)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve channel %q: %w", channelStr, err)
		}
		channelIDs = append(channelIDs, resolved)
	}

	includeReactions := request.GetBool("include_reactions", false)
	timeoutSeconds := request.GetInt("timeout_seconds", 300)

	h.logger.Debug("Waiting for event",
		zap.Strings("channel_ids", channelIDs),
		zap.Bool("include_reactions", includeReactions),
		zap.Int("timeout_seconds", timeoutSeconds),
	)

	// Subscribe to events
	sub := h.router.Subscribe(channelIDs, includeReactions)
	defer h.router.Unsubscribe(sub)

	// Set up timeout
	var timeoutCh <-chan time.Time
	if timeoutSeconds > 0 {
		timeoutCh = time.After(time.Duration(timeoutSeconds) * time.Second)
	}

	// Wait for event, timeout, or context cancellation
	select {
	case event := <-sub.ResultChan:
		return h.eventToResult(event)
	case <-timeoutCh:
		result := map[string]interface{}{
			"event_type": "timeout",
		}
		jsonBytes, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(jsonBytes)), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// eventToResult converts a SlackEvent to an MCP tool result.
func (h *EventsHandler) eventToResult(event *events.SlackEvent) (*mcp.CallToolResult, error) {
	result := map[string]interface{}{
		"event_type":      event.Type,
		"channel_id":      event.ChannelID,
		"channel_name":    event.ChannelName,
		"message_ts":      event.MessageTS,
		"user_id":         event.UserID,
		"user_name":       event.UserName,
		"text":            event.Text,
		"is_thread_reply": event.IsThreadReply,
		"is_bot_message":  event.IsBotMessage,
	}

	if event.ThreadTS != "" {
		result["thread_ts"] = event.ThreadTS
	}
	if event.Reaction != "" {
		result["reaction"] = event.Reaction
	}
	if len(event.Files) > 0 {
		var files []map[string]interface{}
		for _, f := range event.Files {
			files = append(files, map[string]interface{}{
				"file_id":    f.FileID,
				"filename":   f.Filename,
				"filetype":   f.Filetype,
				"mimetype":   f.Mimetype,
				"size_bytes": f.SizeBytes,
			})
		}
		result["files"] = files
	}

	jsonBytes, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal event result: %w", err)
	}

	return mcp.NewToolResultText(string(jsonBytes)), nil
}

// resolveChannelID resolves a channel name (e.g., "#general") to a channel ID.
func (h *EventsHandler) resolveChannelID(channel string) (string, error) {
	if !strings.HasPrefix(channel, "#") && !strings.HasPrefix(channel, "@") {
		return channel, nil
	}

	if ready, err := h.apiProvider.IsReady(); !ready {
		return "", fmt.Errorf("cannot resolve channel name %q: %w", channel, err)
	}

	channelsMaps := h.apiProvider.ProvideChannelsMaps()
	id, ok := channelsMaps.ChannelsInv[channel]
	if !ok {
		return "", fmt.Errorf("channel %q not found in cache", channel)
	}
	return channelsMaps.Channels[id].ID, nil
}
