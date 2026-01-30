package events

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/korotovsky/slack-mcp-server/pkg/provider"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
	"go.uber.org/zap"
)

// messageEventRaw is used to extract the files field from the raw event JSON,
// since slackevents.MessageEvent does not include it.
type messageEventRaw struct {
	Files []struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Filetype string `json:"filetype"`
		Mimetype string `json:"mimetype"`
		Size     int64  `json:"size"`
	} `json:"files"`
}

// SocketModeClient wraps a Slack Socket Mode connection and routes events.
type SocketModeClient struct {
	client   *socketmode.Client
	router   *EventRouter
	provider *provider.ApiProvider
	logger   *zap.Logger
}

// NewSocketModeClient creates a new Socket Mode client.
// botToken is the xoxb-... token, appToken is the xapp-... token.
func NewSocketModeClient(botToken, appToken string, apiProvider *provider.ApiProvider, router *EventRouter, logger *zap.Logger) *SocketModeClient {
	// Create a new slack.Client with both the bot token and the app-level token.
	// The app-level token is required by Socket Mode to open a WebSocket connection.
	api := slack.New(botToken, slack.OptionAppLevelToken(appToken))
	smClient := socketmode.New(api)

	return &SocketModeClient{
		client:   smClient,
		router:   router,
		provider: apiProvider,
		logger:   logger,
	}
}

// Start connects to Slack via Socket Mode and processes events.
// It blocks until the context is canceled.
func (s *SocketModeClient) Start(ctx context.Context) error {
	s.logger.Info("Starting Socket Mode client",
		zap.String("context", "console"),
	)

	go s.handleEvents(ctx)

	err := s.client.RunContext(ctx)
	if err != nil && ctx.Err() == nil {
		s.logger.Error("Socket Mode client error",
			zap.String("context", "console"),
			zap.Error(err),
		)
		return err
	}

	s.logger.Info("Socket Mode client stopped",
		zap.String("context", "console"),
	)
	return nil
}

// handleEvents processes incoming Socket Mode events.
func (s *SocketModeClient) handleEvents(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-s.client.Events:
			if !ok {
				return
			}
			s.processEvent(evt)
		}
	}
}

// processEvent handles a single Socket Mode event.
func (s *SocketModeClient) processEvent(evt socketmode.Event) {
	switch evt.Type {
	case socketmode.EventTypeEventsAPI:
		eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
		if !ok {
			s.logger.Warn("Failed to cast EventsAPI event data")
			return
		}
		// Acknowledge the event immediately
		s.client.Ack(*evt.Request)

		s.handleEventsAPIEvent(eventsAPIEvent, evt.Request)

	case socketmode.EventTypeConnecting:
		s.logger.Info("Socket Mode connecting...",
			zap.String("context", "console"),
		)

	case socketmode.EventTypeConnected:
		s.logger.Info("Socket Mode connected",
			zap.String("context", "console"),
		)

	case socketmode.EventTypeConnectionError:
		s.logger.Error("Socket Mode connection error",
			zap.String("context", "console"),
		)

	case socketmode.EventTypeDisconnect:
		s.logger.Warn("Socket Mode disconnected, will reconnect",
			zap.String("context", "console"),
		)

	default:
		s.logger.Debug("Unhandled Socket Mode event type",
			zap.String("type", string(evt.Type)),
		)
	}
}

// handleEventsAPIEvent processes Events API payloads.
func (s *SocketModeClient) handleEventsAPIEvent(event slackevents.EventsAPIEvent, req *socketmode.Request) {
	switch event.Type {
	case slackevents.CallbackEvent:
		innerEvent := event.InnerEvent
		switch ev := innerEvent.Data.(type) {
		case *slackevents.MessageEvent:
			s.handleMessageEvent(ev, req)
		case *slackevents.ReactionAddedEvent:
			s.handleReactionEvent(ev)
		default:
			s.logger.Debug("Unhandled inner event type",
				zap.String("type", fmt.Sprintf("%T", ev)),
			)
		}
	default:
		s.logger.Debug("Unhandled Events API event type",
			zap.String("type", string(event.Type)),
		)
	}
}

// handleMessageEvent converts a Slack message event and routes it.
func (s *SocketModeClient) handleMessageEvent(ev *slackevents.MessageEvent, req *socketmode.Request) {
	// Skip message subtypes that aren't actual messages (e.g., message_changed, message_deleted)
	if ev.SubType != "" && ev.SubType != "bot_message" && ev.SubType != "file_share" {
		s.logger.Debug("Skipping message subtype",
			zap.String("subtype", ev.SubType),
		)
		return
	}

	channelName := s.resolveChannelName(ev.Channel)
	userName := s.resolveUserName(ev.User)

	isThreadReply := ev.ThreadTimeStamp != "" && ev.ThreadTimeStamp != ev.TimeStamp
	isBotMessage := ev.SubType == "bot_message" || ev.BotID != ""

	// Extract files from the raw event payload, since slackevents.MessageEvent
	// does not have a Files field.
	var files []SlackFile
	if req != nil && req.Payload != nil {
		var envelope struct {
			Event messageEventRaw `json:"event"`
		}
		if err := json.Unmarshal(req.Payload, &envelope); err == nil {
			for _, f := range envelope.Event.Files {
				files = append(files, SlackFile{
					FileID:    f.ID,
					Filename:  f.Name,
					Filetype:  f.Filetype,
					Mimetype:  f.Mimetype,
					SizeBytes: f.Size,
				})
			}
		}
	}

	slackEvent := &SlackEvent{
		Type:          "message",
		ChannelID:     ev.Channel,
		ChannelName:   channelName,
		UserID:        ev.User,
		UserName:      userName,
		Text:          ev.Text,
		MessageTS:     ev.TimeStamp,
		ThreadTS:      ev.ThreadTimeStamp,
		IsThreadReply: isThreadReply,
		IsBotMessage:  isBotMessage,
		Files:         files,
	}

	s.logger.Debug("Routing message event",
		zap.String("channel_id", ev.Channel),
		zap.String("channel_name", channelName),
		zap.String("user", userName),
		zap.Bool("is_thread_reply", isThreadReply),
		zap.Bool("is_bot", isBotMessage),
	)

	s.router.RouteEvent(slackEvent)
}

// handleReactionEvent converts a Slack reaction event and routes it.
func (s *SocketModeClient) handleReactionEvent(ev *slackevents.ReactionAddedEvent) {
	channelName := s.resolveChannelName(ev.Item.Channel)
	userName := s.resolveUserName(ev.User)

	slackEvent := &SlackEvent{
		Type:        "reaction",
		ChannelID:   ev.Item.Channel,
		ChannelName: channelName,
		UserID:      ev.User,
		UserName:    userName,
		MessageTS:   ev.Item.Timestamp,
		Reaction:    ev.Reaction,
	}

	s.logger.Debug("Routing reaction event",
		zap.String("channel_id", ev.Item.Channel),
		zap.String("user", userName),
		zap.String("reaction", ev.Reaction),
	)

	s.router.RouteEvent(slackEvent)
}

// resolveChannelName looks up a channel name from the provider cache.
func (s *SocketModeClient) resolveChannelName(channelID string) string {
	channelsMaps := s.provider.ProvideChannelsMaps()
	if ch, ok := channelsMaps.Channels[channelID]; ok {
		return ch.Name
	}
	return channelID
}

// resolveUserName looks up a user name from the provider cache.
func (s *SocketModeClient) resolveUserName(userID string) string {
	if userID == "" {
		return ""
	}
	usersMap := s.provider.ProvideUsersMap()
	if u, ok := usersMap.Users[userID]; ok {
		return u.Name
	}
	return userID
}

