package events

import (
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// SlackEvent represents an incoming Slack event routed to subscribers.
type SlackEvent struct {
	Type          string      `json:"event_type"`
	ChannelID     string      `json:"channel_id"`
	ChannelName   string      `json:"channel_name"`
	UserID        string      `json:"user_id"`
	UserName      string      `json:"user_name"`
	Text          string      `json:"text"`
	MessageTS     string      `json:"message_ts"`
	ThreadTS      string      `json:"thread_ts"`
	IsThreadReply bool        `json:"is_thread_reply"`
	IsBotMessage  bool        `json:"is_bot_message"`
	Reaction      string      `json:"reaction,omitempty"`
	Files         []SlackFile `json:"files,omitempty"`
}

// SlackFile represents a file attachment on a Slack message.
type SlackFile struct {
	FileID    string `json:"file_id"`
	Filename  string `json:"filename"`
	Filetype  string `json:"filetype"`
	Mimetype  string `json:"mimetype"`
	SizeBytes int64  `json:"size_bytes"`
}

// Subscriber represents a waiting slack_wait_for_event call.
type Subscriber struct {
	ID               string
	Channels         map[string]bool // channel IDs for O(1) lookup
	IncludeReactions bool
	ResultChan       chan *SlackEvent
	CreatedAt        time.Time
}

// EventRouter manages subscribers and routes incoming events.
type EventRouter struct {
	subscribers []*Subscriber
	mu          sync.RWMutex
	logger      *zap.Logger
}

// NewEventRouter creates a new EventRouter.
func NewEventRouter(logger *zap.Logger) *EventRouter {
	return &EventRouter{
		subscribers: make([]*Subscriber, 0),
		logger:      logger,
	}
}

// Subscribe creates a new subscriber for the given channel IDs.
func (r *EventRouter) Subscribe(channelIDs []string, includeReactions bool) *Subscriber {
	channels := make(map[string]bool, len(channelIDs))
	for _, id := range channelIDs {
		channels[id] = true
	}

	sub := &Subscriber{
		ID:               uuid.New().String(),
		Channels:         channels,
		IncludeReactions: includeReactions,
		ResultChan:       make(chan *SlackEvent, 1),
		CreatedAt:        time.Now(),
	}

	r.mu.Lock()
	r.subscribers = append(r.subscribers, sub)
	r.mu.Unlock()

	r.logger.Debug("New subscriber registered",
		zap.String("subscriber_id", sub.ID),
		zap.Int("channel_count", len(channelIDs)),
		zap.Bool("include_reactions", includeReactions),
	)

	return sub
}

// Unsubscribe removes a subscriber from the router.
func (r *EventRouter) Unsubscribe(sub *Subscriber) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i, s := range r.subscribers {
		if s.ID == sub.ID {
			r.subscribers = append(r.subscribers[:i], r.subscribers[i+1:]...)
			r.logger.Debug("Subscriber removed",
				zap.String("subscriber_id", sub.ID),
			)
			return
		}
	}
}

// RouteEvent delivers an event to all matching subscribers.
func (r *EventRouter) RouteEvent(event *SlackEvent) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	delivered := 0
	for _, sub := range r.subscribers {
		if r.matches(sub, event) {
			// Non-blocking send — subscriber might have timed out
			select {
			case sub.ResultChan <- event:
				delivered++
			default:
				r.logger.Debug("Subscriber channel full, skipping",
					zap.String("subscriber_id", sub.ID),
				)
			}
		}
	}

	r.logger.Debug("Event routed",
		zap.String("event_type", event.Type),
		zap.String("channel_id", event.ChannelID),
		zap.Int("subscribers_matched", delivered),
		zap.Int("subscribers_total", len(r.subscribers)),
	)
}

// matches checks whether a subscriber should receive the given event.
func (r *EventRouter) matches(sub *Subscriber, event *SlackEvent) bool {
	if !sub.Channels[event.ChannelID] {
		return false
	}
	if event.Type == "reaction" && !sub.IncludeReactions {
		return false
	}
	return true
}
