package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/korotovsky/slack-mcp-server/pkg/events"
	"github.com/korotovsky/slack-mcp-server/pkg/provider"
	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"
)

// SubscriptionsHandler handles persistent subscription tools.
type SubscriptionsHandler struct {
	apiProvider *provider.ApiProvider
	router      *events.EventRouter
	logger      *zap.Logger
}

// NewSubscriptionsHandler creates a new SubscriptionsHandler.
func NewSubscriptionsHandler(apiProvider *provider.ApiProvider, router *events.EventRouter, logger *zap.Logger) *SubscriptionsHandler {
	return &SubscriptionsHandler{
		apiProvider: apiProvider,
		router:      router,
		logger:      logger,
	}
}

// SubscribeHandler creates a persistent subscription to Slack channels.
func (h *SubscriptionsHandler) SubscribeHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if h.router == nil {
		return nil, errors.New("event routing is not enabled; set SLACK_MCP_ENABLE_EVENTS=true and provide SLACK_MCP_APP_TOKEN")
	}

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

	ps := h.router.PersistentSubscribe(channelIDs, includeReactions)

	h.logger.Info("Persistent subscription created",
		zap.String("subscription_id", ps.ID),
		zap.Strings("channels", channelIDs),
		zap.Bool("include_reactions", includeReactions),
	)

	result := map[string]interface{}{
		"ok":                true,
		"subscription_id":   ps.ID,
		"channels":          channelIDs,
		"include_reactions":  includeReactions,
		"buffer_size":       events.DefaultMaxBufferSize,
	}
	jsonBytes, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}
	return mcp.NewToolResultText(string(jsonBytes)), nil
}

// GetEventsHandler retrieves buffered events from a persistent subscription.
func (h *SubscriptionsHandler) GetEventsHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if h.router == nil {
		return nil, errors.New("event routing is not enabled; set SLACK_MCP_ENABLE_EVENTS=true and provide SLACK_MCP_APP_TOKEN")
	}

	args := request.GetArguments()
	subID, ok := args["subscription_id"].(string)
	if !ok || subID == "" {
		return nil, errors.New("subscription_id parameter is required")
	}

	ps := h.router.GetPersistentSubscriber(subID)
	if ps == nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{mcp.TextContent{
				Annotated: mcp.Annotated{},
				Type:      "text",
				Text:      fmt.Sprintf(`{"ok":false,"error":"subscription %q not found (may have expired)"}`, subID),
			}},
			IsError: true,
		}, nil
	}

	drainedEvents := ps.DrainEvents()

	eventMaps := make([]map[string]interface{}, 0, len(drainedEvents))
	for _, event := range drainedEvents {
		eventMaps = append(eventMaps, eventToMap(event))
	}

	result := map[string]interface{}{
		"ok":              true,
		"subscription_id": subID,
		"events":          eventMaps,
		"event_count":     len(eventMaps),
	}
	jsonBytes, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}
	return mcp.NewToolResultText(string(jsonBytes)), nil
}

// UnsubscribeHandler destroys a persistent subscription.
func (h *SubscriptionsHandler) UnsubscribeHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if h.router == nil {
		return nil, errors.New("event routing is not enabled; set SLACK_MCP_ENABLE_EVENTS=true and provide SLACK_MCP_APP_TOKEN")
	}

	args := request.GetArguments()
	subID, ok := args["subscription_id"].(string)
	if !ok || subID == "" {
		return nil, errors.New("subscription_id parameter is required")
	}

	removed := h.router.PersistentUnsubscribe(subID)
	if !removed {
		return &mcp.CallToolResult{
			Content: []mcp.Content{mcp.TextContent{
				Annotated: mcp.Annotated{},
				Type:      "text",
				Text:      fmt.Sprintf(`{"ok":false,"error":"subscription %q not found (may have expired)"}`, subID),
			}},
			IsError: true,
		}, nil
	}

	h.logger.Info("Persistent subscription removed",
		zap.String("subscription_id", subID),
	)

	result := map[string]interface{}{
		"ok":              true,
		"subscription_id": subID,
		"unsubscribed":    true,
	}
	jsonBytes, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}
	return mcp.NewToolResultText(string(jsonBytes)), nil
}

// eventToMap converts a SlackEvent to a map for JSON serialization.
func eventToMap(event *events.SlackEvent) map[string]interface{} {
	m := map[string]interface{}{
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
		m["thread_ts"] = event.ThreadTS
	}
	if event.Reaction != "" {
		m["reaction"] = event.Reaction
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
		m["files"] = files
	}
	return m
}

// resolveChannelID resolves a channel name (e.g., "#general") to a channel ID.
func (h *SubscriptionsHandler) resolveChannelID(channel string) (string, error) {
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
