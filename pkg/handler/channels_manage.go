package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/korotovsky/slack-mcp-server/pkg/provider"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	"go.uber.org/zap"
)

// ChannelsManageHandler handles channel creation operations.
type ChannelsManageHandler struct {
	apiProvider *provider.ApiProvider
	logger      *zap.Logger
}

// NewChannelsManageHandler creates a new ChannelsManageHandler.
func NewChannelsManageHandler(apiProvider *provider.ApiProvider, logger *zap.Logger) *ChannelsManageHandler {
	return &ChannelsManageHandler{
		apiProvider: apiProvider,
		logger:      logger,
	}
}

// CreateChannelHandler creates a new Slack channel or returns an existing one.
func (h *ChannelsManageHandler) CreateChannelHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("CreateChannelHandler called", zap.Any("params", request.Params))

	name := request.GetString("name", "")
	if name == "" {
		return nil, errors.New("name parameter is required")
	}

	// Normalize: remove leading # if present, lowercase, replace spaces with hyphens
	name = strings.TrimPrefix(name, "#")
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, " ", "-")

	if len(name) > 80 {
		return nil, errors.New("channel name must be 80 characters or fewer")
	}

	isPrivate := request.GetBool("is_private", false)
	description := request.GetString("description", "")

	h.logger.Debug("Creating channel",
		zap.String("name", name),
		zap.Bool("is_private", isPrivate),
		zap.String("description", description),
	)

	// Get the underlying slack.Client
	slackClient := h.apiProvider.Slack().(*provider.MCPSlackClient).Raw().Slack

	channel, err := slackClient.CreateConversationContext(ctx, slack.CreateConversationParams{
		ChannelName: name,
		IsPrivate:   isPrivate,
	})
	if err != nil {
		// Check if channel already exists
		if strings.Contains(err.Error(), "name_taken") {
			return h.handleExistingChannel(ctx, name)
		}
		h.logger.Error("Failed to create channel",
			zap.String("name", name),
			zap.Error(err),
		)
		return nil, fmt.Errorf("failed to create channel: %w", err)
	}

	// Set description/purpose if provided
	if description != "" {
		_, err := slackClient.SetPurposeOfConversationContext(ctx, channel.ID, description)
		if err != nil {
			h.logger.Warn("Failed to set channel purpose",
				zap.String("channel_id", channel.ID),
				zap.Error(err),
			)
		}
	}

	result := map[string]interface{}{
		"ok":              true,
		"channel_id":      channel.ID,
		"channel_name":    channel.Name,
		"already_existed": false,
	}

	jsonBytes, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}

	h.logger.Info("Channel created successfully",
		zap.String("channel_id", channel.ID),
		zap.String("channel_name", channel.Name),
	)

	return mcp.NewToolResultText(string(jsonBytes)), nil
}

// handleExistingChannel looks up an existing channel by name and returns it.
func (h *ChannelsManageHandler) handleExistingChannel(ctx context.Context, name string) (*mcp.CallToolResult, error) {
	// Try to find the channel in the cache first
	channelsMaps := h.apiProvider.ProvideChannelsMaps()
	searchName := "#" + name
	if id, ok := channelsMaps.ChannelsInv[searchName]; ok {
		ch := channelsMaps.Channels[id]
		result := map[string]interface{}{
			"ok":              true,
			"channel_id":      ch.ID,
			"channel_name":    ch.Name,
			"already_existed": true,
		}
		jsonBytes, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(jsonBytes)), nil
	}

	// If not in cache, try listing channels to find it
	slackClient := h.apiProvider.Slack().(*provider.MCPSlackClient).Raw().Slack
	params := &slack.GetConversationsParameters{
		Types: []string{"public_channel", "private_channel"},
		Limit: 1000,
	}

	channels, _, err := slackClient.GetConversationsContext(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("failed to list channels while looking up existing channel %q: %w", name, err)
	}

	for _, ch := range channels {
		if ch.Name == name || ch.NameNormalized == name {
			result := map[string]interface{}{
				"ok":              true,
				"channel_id":      ch.ID,
				"channel_name":    ch.Name,
				"already_existed": true,
			}
			jsonBytes, _ := json.Marshal(result)

			h.logger.Info("Found existing channel",
				zap.String("channel_id", ch.ID),
				zap.String("channel_name", ch.Name),
			)

			return mcp.NewToolResultText(string(jsonBytes)), nil
		}
	}

	return nil, fmt.Errorf("channel %q already exists but could not be found", name)
}
