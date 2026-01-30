package events

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

const DefaultMaxBufferSize = 100

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

// PersistentSubscriber represents a long-lived subscription that buffers events.
type PersistentSubscriber struct {
	ID               string
	Channels         map[string]bool
	IncludeReactions bool
	buffer           []*SlackEvent
	mu               sync.Mutex
	maxBufferSize    int
	lastAccess       time.Time
	CreatedAt        time.Time
}

// AppendEvent adds an event to the buffer, dropping the oldest if full.
// Returns true if an event was dropped due to buffer overflow.
func (ps *PersistentSubscriber) AppendEvent(event *SlackEvent) bool {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	dropped := false
	if len(ps.buffer) >= ps.maxBufferSize {
		ps.buffer = ps.buffer[1:]
		dropped = true
	}
	ps.buffer = append(ps.buffer, event)
	return dropped
}

// DrainEvents returns all buffered events and clears the buffer. Updates lastAccess.
func (ps *PersistentSubscriber) DrainEvents() []*SlackEvent {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	ps.lastAccess = time.Now()
	if len(ps.buffer) == 0 {
		return []*SlackEvent{}
	}
	result := ps.buffer
	ps.buffer = nil
	return result
}

// IsStale returns true if lastAccess is older than the given duration.
func (ps *PersistentSubscriber) IsStale(maxAge time.Duration) bool {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return time.Since(ps.lastAccess) > maxAge
}

// EventRouter manages subscribers and routes incoming events.
type EventRouter struct {
	subscribers           []*Subscriber
	persistentSubscribers []*PersistentSubscriber
	mu                    sync.RWMutex
	logger                *zap.Logger
}

// NewEventRouter creates a new EventRouter.
func NewEventRouter(logger *zap.Logger) *EventRouter {
	return &EventRouter{
		subscribers:           make([]*Subscriber, 0),
		persistentSubscribers: make([]*PersistentSubscriber, 0),
		logger:                logger,
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

// RouteEvent delivers an event to all matching subscribers (ephemeral and persistent).
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

	// Deliver to persistent subscribers
	persistentDelivered := 0
	for _, ps := range r.persistentSubscribers {
		if r.matchesPersistent(ps, event) {
			dropped := ps.AppendEvent(event)
			persistentDelivered++
			if dropped {
				r.logger.Debug("Persistent subscriber buffer full, oldest event dropped",
					zap.String("subscriber_id", ps.ID),
				)
			}
		}
	}

	r.logger.Debug("Event routed",
		zap.String("event_type", event.Type),
		zap.String("channel_id", event.ChannelID),
		zap.Int("ephemeral_matched", delivered),
		zap.Int("persistent_matched", persistentDelivered),
		zap.Int("subscribers_total", len(r.subscribers)+len(r.persistentSubscribers)),
	)
}

// matches checks whether an ephemeral subscriber should receive the given event.
func (r *EventRouter) matches(sub *Subscriber, event *SlackEvent) bool {
	if !sub.Channels[event.ChannelID] {
		return false
	}
	if event.Type == "reaction" && !sub.IncludeReactions {
		return false
	}
	return true
}

// matchesPersistent checks whether a persistent subscriber should receive the given event.
func (r *EventRouter) matchesPersistent(ps *PersistentSubscriber, event *SlackEvent) bool {
	if !ps.Channels[event.ChannelID] {
		return false
	}
	if event.Type == "reaction" && !ps.IncludeReactions {
		return false
	}
	return true
}

// PersistentSubscribe creates a new persistent subscription that buffers events.
func (r *EventRouter) PersistentSubscribe(channelIDs []string, includeReactions bool) *PersistentSubscriber {
	channels := make(map[string]bool, len(channelIDs))
	for _, id := range channelIDs {
		channels[id] = true
	}

	ps := &PersistentSubscriber{
		ID:               uuid.New().String(),
		Channels:         channels,
		IncludeReactions: includeReactions,
		buffer:           make([]*SlackEvent, 0),
		maxBufferSize:    DefaultMaxBufferSize,
		lastAccess:       time.Now(),
		CreatedAt:        time.Now(),
	}

	r.mu.Lock()
	r.persistentSubscribers = append(r.persistentSubscribers, ps)
	r.mu.Unlock()

	r.logger.Debug("New persistent subscriber registered",
		zap.String("subscriber_id", ps.ID),
		zap.Int("channel_count", len(channelIDs)),
		zap.Bool("include_reactions", includeReactions),
		zap.Int("buffer_size", ps.maxBufferSize),
	)

	return ps
}

// PersistentUnsubscribe removes a persistent subscription by ID.
func (r *EventRouter) PersistentUnsubscribe(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i, ps := range r.persistentSubscribers {
		if ps.ID == id {
			r.persistentSubscribers = append(r.persistentSubscribers[:i], r.persistentSubscribers[i+1:]...)
			r.logger.Debug("Persistent subscriber removed",
				zap.String("subscriber_id", id),
			)
			return true
		}
	}
	return false
}

// GetPersistentSubscriber looks up a persistent subscriber by ID.
func (r *EventRouter) GetPersistentSubscriber(id string) *PersistentSubscriber {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, ps := range r.persistentSubscribers {
		if ps.ID == id {
			return ps
		}
	}
	return nil
}

// ReapStaleSubscriptions removes persistent subscriptions not accessed within maxAge.
func (r *EventRouter) ReapStaleSubscriptions(maxAge time.Duration) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	reaped := 0
	remaining := make([]*PersistentSubscriber, 0, len(r.persistentSubscribers))
	for _, ps := range r.persistentSubscribers {
		if ps.IsStale(maxAge) {
			r.logger.Info("Reaping stale persistent subscription",
				zap.String("subscriber_id", ps.ID),
				zap.Time("created_at", ps.CreatedAt),
			)
			reaped++
		} else {
			remaining = append(remaining, ps)
		}
	}
	r.persistentSubscribers = remaining
	return reaped
}

// StartReaper launches a background goroutine that periodically reaps stale subscriptions.
func (r *EventRouter) StartReaper(ctx context.Context, interval time.Duration, maxAge time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if reaped := r.ReapStaleSubscriptions(maxAge); reaped > 0 {
					r.logger.Info("Reaped stale persistent subscriptions",
						zap.Int("count", reaped),
					)
				}
			}
		}
	}()
}
