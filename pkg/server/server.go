package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/korotovsky/slack-mcp-server/pkg/events"
	"github.com/korotovsky/slack-mcp-server/pkg/handler"
	"github.com/korotovsky/slack-mcp-server/pkg/provider"
	"github.com/korotovsky/slack-mcp-server/pkg/server/auth"
	"github.com/korotovsky/slack-mcp-server/pkg/text"
	"github.com/korotovsky/slack-mcp-server/pkg/version"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"
)

type MCPServer struct {
	server *server.MCPServer
	logger *zap.Logger
}

// MCPServerOptions holds optional dependencies for the MCP server.
type MCPServerOptions struct {
	EventRouter  interface{} // *events.EventRouter, nil if events disabled
	EventsEnabled bool
}

func NewMCPServer(provider *provider.ApiProvider, logger *zap.Logger, opts ...MCPServerOptions) *MCPServer {
	var options MCPServerOptions
	if len(opts) > 0 {
		options = opts[0]
	}
	s := server.NewMCPServer(
		"Slack MCP Server",
		version.Version,
		server.WithLogging(),
		server.WithRecovery(),
		server.WithToolHandlerMiddleware(buildLoggerMiddleware(logger)),
		server.WithToolHandlerMiddleware(auth.BuildMiddleware(provider.ServerTransport(), logger)),
	)

	conversationsHandler := handler.NewConversationsHandler(provider, logger)

	s.AddTool(mcp.NewTool("conversations_history",
		mcp.WithDescription("Get messages from the channel (or DM) by channel_id, the last row/column in the response is used as 'cursor' parameter for pagination if not empty"),
		mcp.WithTitleAnnotation("Get Conversation History"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("channel_id",
			mcp.Required(),
			mcp.Description("    - `channel_id` (string): ID of the channel in format Cxxxxxxxxxx or its name starting with #... or @... aka #general or @username_dm."),
		),
		mcp.WithBoolean("include_activity_messages",
			mcp.Description("If true, the response will include activity messages such as 'channel_join' or 'channel_leave'. Default is boolean false."),
			mcp.DefaultBool(false),
		),
		mcp.WithString("cursor",
			mcp.Description("Cursor for pagination. Use the value of the last row and column in the response as next_cursor field returned from the previous request."),
		),
		mcp.WithString("limit",
			mcp.DefaultString("1d"),
			mcp.Description("Limit of messages to fetch in format of maximum ranges of time (e.g. 1d - 1 day, 1w - 1 week, 30d - 30 days, 90d - 90 days which is a default limit for free tier history) or number of messages (e.g. 50). Must be empty when 'cursor' is provided."),
		),
	), conversationsHandler.ConversationsHistoryHandler)

	s.AddTool(mcp.NewTool("conversations_replies",
		mcp.WithDescription("Get a thread of messages posted to a conversation by channelID and thread_ts, the last row/column in the response is used as 'cursor' parameter for pagination if not empty"),
		mcp.WithTitleAnnotation("Get Thread Replies"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("channel_id",
			mcp.Required(),
			mcp.Description("ID of the channel in format Cxxxxxxxxxx or its name starting with #... or @... aka #general or @username_dm."),
		),
		mcp.WithString("thread_ts",
			mcp.Required(),
			mcp.Description("Unique identifier of either a thread's parent message or a message in the thread. ts must be the timestamp in format 1234567890.123456 of an existing message with 0 or more replies."),
		),
		mcp.WithBoolean("include_activity_messages",
			mcp.Description("If true, the response will include activity messages such as 'channel_join' or 'channel_leave'. Default is boolean false."),
			mcp.DefaultBool(false),
		),
		mcp.WithString("cursor",
			mcp.Description("Cursor for pagination. Use the value of the last row and column in the response as next_cursor field returned from the previous request."),
		),
		mcp.WithString("limit",
			mcp.DefaultString("1d"),
			mcp.Description("Limit of messages to fetch in format of maximum ranges of time (e.g. 1d - 1 day, 30d - 30 days, 90d - 90 days which is a default limit for free tier history) or number of messages (e.g. 50). Must be empty when 'cursor' is provided."),
		),
	), conversationsHandler.ConversationsRepliesHandler)

	s.AddTool(mcp.NewTool("conversations_add_message",
		mcp.WithDescription("Add a message to a public channel, private channel, or direct message (DM, or IM) conversation by channel_id and thread_ts."),
		mcp.WithTitleAnnotation("Send Message"),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("channel_id",
			mcp.Required(),
			mcp.Description("ID of the channel in format Cxxxxxxxxxx or its name starting with #... or @... aka #general or @username_dm."),
		),
		mcp.WithString("thread_ts",
			mcp.Description("Unique identifier of either a thread's parent message or a message in the thread_ts must be the timestamp in format 1234567890.123456 of an existing message with 0 or more replies. Optional, if not provided the message will be added to the channel itself, otherwise it will be added to the thread."),
		),
		mcp.WithString("payload",
			mcp.Description("Message payload in specified content_type format. Example: 'Hello, world!' for text/plain or '# Hello, world!' for text/markdown."),
		),
		mcp.WithString("content_type",
			mcp.DefaultString("text/markdown"),
			mcp.Description("Content type of the message. Default is 'text/markdown'. Allowed values: 'text/markdown', 'text/plain'."),
		),
	), conversationsHandler.ConversationsAddMessageHandler)

	s.AddTool(mcp.NewTool("reactions_add",
		mcp.WithDescription("Add an emoji reaction to a message in a public channel, private channel, or direct message (DM, or IM) conversation."),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("channel_id",
			mcp.Required(),
			mcp.Description("ID of the channel in format Cxxxxxxxxxx or its name starting with #... or @... aka #general or @username_dm."),
		),
		mcp.WithString("timestamp",
			mcp.Required(),
			mcp.Description("Timestamp of the message to add reaction to, in format 1234567890.123456."),
		),
		mcp.WithString("emoji",
			mcp.Required(),
			mcp.Description("The name of the emoji to add as a reaction (without colons). Example: 'thumbsup', 'heart', 'rocket'."),
		),
	), conversationsHandler.ReactionsAddHandler)

	s.AddTool(mcp.NewTool("reactions_remove",
		mcp.WithDescription("Remove an emoji reaction from a message in a public channel, private channel, or direct message (DM, or IM) conversation."),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("channel_id",
			mcp.Required(),
			mcp.Description("ID of the channel in format Cxxxxxxxxxx or its name starting with #... or @... aka #general or @username_dm."),
		),
		mcp.WithString("timestamp",
			mcp.Required(),
			mcp.Description("Timestamp of the message to remove reaction from, in format 1234567890.123456."),
		),
		mcp.WithString("emoji",
			mcp.Required(),
			mcp.Description("The name of the emoji to remove as a reaction (without colons). Example: 'thumbsup', 'heart', 'rocket'."),
		),
	), conversationsHandler.ReactionsRemoveHandler)

	s.AddTool(mcp.NewTool("attachment_get_data",
		mcp.WithDescription("Download an attachment's content by file ID. Returns file metadata and content (text files as-is, binary files as base64). Maximum file size is 5MB."),
		mcp.WithTitleAnnotation("Get Attachment Data"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("file_id",
			mcp.Required(),
			mcp.Description("The ID of the attachment to download, in format Fxxxxxxxxxx. Attachment IDs can be found in message metadata when HasMedia is true or AttachmentCount > 0."),
		),
	), conversationsHandler.FilesGetHandler)

	conversationsSearchTool := mcp.NewTool("conversations_search_messages",
		mcp.WithDescription("Search messages in a public channel, private channel, or direct message (DM, or IM) conversation using filters. All filters are optional, if not provided then search_query is required."),
		mcp.WithTitleAnnotation("Search Messages"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("search_query",
			mcp.Description("Search query to filter messages. Example: 'marketing report' or full URL of Slack message e.g. 'https://slack.com/archives/C1234567890/p1234567890123456', then the tool will return a single message matching given URL, herewith all other parameters will be ignored."),
		),
		mcp.WithString("filter_in_channel",
			mcp.Description("Filter messages in a specific public/private channel by its ID or name. Example: 'C1234567890', 'G1234567890', or '#general'. If not provided, all channels will be searched."),
		),
		mcp.WithString("filter_in_im_or_mpim",
			mcp.Description("Filter messages in a direct message (DM) or multi-person direct message (MPIM) conversation by its ID or name. Example: 'D1234567890' or '@username_dm'. If not provided, all DMs and MPIMs will be searched."),
		),
		mcp.WithString("filter_users_with",
			mcp.Description("Filter messages with a specific user by their ID or display name in threads and DMs. Example: 'U1234567890' or '@username'. If not provided, all threads and DMs will be searched."),
		),
		mcp.WithString("filter_users_from",
			mcp.Description("Filter messages from a specific user by their ID or display name. Example: 'U1234567890' or '@username'. If not provided, all users will be searched."),
		),
		mcp.WithString("filter_date_before",
			mcp.Description("Filter messages sent before a specific date in format 'YYYY-MM-DD'. Example: '2023-10-01', 'July', 'Yesterday' or 'Today'. If not provided, all dates will be searched."),
		),
		mcp.WithString("filter_date_after",
			mcp.Description("Filter messages sent after a specific date in format 'YYYY-MM-DD'. Example: '2023-10-01', 'July', 'Yesterday' or 'Today'. If not provided, all dates will be searched."),
		),
		mcp.WithString("filter_date_on",
			mcp.Description("Filter messages sent on a specific date in format 'YYYY-MM-DD'. Example: '2023-10-01', 'July', 'Yesterday' or 'Today'. If not provided, all dates will be searched."),
		),
		mcp.WithString("filter_date_during",
			mcp.Description("Filter messages sent during a specific period in format 'YYYY-MM-DD'. Example: 'July', 'Yesterday' or 'Today'. If not provided, all dates will be searched."),
		),
		mcp.WithBoolean("filter_threads_only",
			mcp.Description("If true, the response will include only messages from threads. Default is boolean false."),
		),
		mcp.WithString("cursor",
			mcp.DefaultString(""),
			mcp.Description("Cursor for pagination. Use the value of the last row and column in the response as next_cursor field returned from the previous request."),
		),
		mcp.WithNumber("limit",
			mcp.DefaultNumber(20),
			mcp.Description("The maximum number of items to return. Must be an integer between 1 and 100."),
		),
	)
	// Only register search tool for non-bot tokens (bot tokens cannot use search.messages API)
	if !provider.IsBotToken() {
		s.AddTool(conversationsSearchTool, conversationsHandler.ConversationsSearchHandler)
	}

	channelsHandler := handler.NewChannelsHandler(provider, logger)

	s.AddTool(mcp.NewTool("channels_list",
		mcp.WithDescription("Get list of channels"),
		mcp.WithTitleAnnotation("List Channels"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("channel_types",
			mcp.Required(),
			mcp.Description("Comma-separated channel types. Allowed values: 'mpim', 'im', 'public_channel', 'private_channel'. Example: 'public_channel,private_channel,im'"),
		),
		mcp.WithString("sort",
			mcp.Description("Type of sorting. Allowed values: 'popularity' - sort by number of members/participants in each channel."),
		),
		mcp.WithNumber("limit",
			mcp.DefaultNumber(100),
			mcp.Description("The maximum number of items to return. Must be an integer between 1 and 1000 (maximum 999)."), // context fix for cursor: https://github.com/korotovsky/slack-mcp-server/issues/7
		),
		mcp.WithString("cursor",
			mcp.Description("Cursor for pagination. Use the value of the last row and column in the response as next_cursor field returned from the previous request."),
		),
	), channelsHandler.ChannelsHandler)

	logger.Info("Authenticating with Slack API...",
		zap.String("context", "console"),
	)
	ar, err := provider.Slack().AuthTest()
	if err != nil {
		logger.Fatal("Failed to authenticate with Slack",
			zap.String("context", "console"),
			zap.Error(err),
		)
	}

	logger.Info("Successfully authenticated with Slack",
		zap.String("context", "console"),
		zap.String("team", ar.Team),
		zap.String("user", ar.User),
		zap.String("enterprise", ar.EnterpriseID),
		zap.String("url", ar.URL),
	)

	ws, err := text.Workspace(ar.URL)
	if err != nil {
		logger.Fatal("Failed to parse workspace from URL",
			zap.String("context", "console"),
			zap.String("url", ar.URL),
			zap.Error(err),
		)
	}

	s.AddResource(mcp.NewResource(
		"slack://"+ws+"/channels",
		"Directory of Slack channels",
		mcp.WithResourceDescription("This resource provides a directory of Slack channels."),
		mcp.WithMIMEType("text/csv"),
	), channelsHandler.ChannelsResource)

	s.AddResource(mcp.NewResource(
		"slack://"+ws+"/users",
		"Directory of Slack users",
		mcp.WithResourceDescription("This resource provides a directory of Slack users."),
		mcp.WithMIMEType("text/csv"),
	), conversationsHandler.UsersResource)

	// Register new bidirectional tools

	// slack_wait_for_event — only when Socket Mode events are enabled
	if options.EventsEnabled {
		var router *events.EventRouter
		if options.EventRouter != nil {
			router = options.EventRouter.(*events.EventRouter)
		}
		eventsHandler := handler.NewEventsHandler(provider, router, logger)

		s.AddTool(mcp.NewTool("slack_wait_for_event",
			mcp.WithDescription("Block until a message or reaction arrives in the specified Slack channels. Returns the event details or times out."),
			mcp.WithTitleAnnotation("Wait for Slack Event"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithArray("channels",
				mcp.Required(),
				mcp.Description("Channel IDs or names to monitor (e.g., ['#cala-dev', 'C0123456'])"),
			),
			mcp.WithBoolean("include_reactions",
				mcp.Description("Also notify on reaction events. Default is false."),
				mcp.DefaultBool(false),
			),
			mcp.WithNumber("timeout_seconds",
				mcp.Description("Max time to wait in seconds. 0 = no timeout. Default is 300."),
				mcp.DefaultNumber(300),
			),
		), eventsHandler.WaitForEventHandler)

		logger.Info("Registered slack_wait_for_event tool (Socket Mode enabled)",
			zap.String("context", "console"),
		)

		// Persistent subscription tools
		subscriptionsHandler := handler.NewSubscriptionsHandler(provider, router, logger)

		s.AddTool(mcp.NewTool("slack_subscribe",
			mcp.WithDescription("Register a persistent subscription to receive events from specified Slack channels. Events are buffered server-side until retrieved with slack_get_events. Returns a subscription_id."),
			mcp.WithTitleAnnotation("Subscribe to Slack Events"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithArray("channels",
				mcp.Required(),
				mcp.Description("Channel IDs or names to monitor (e.g., ['#general', 'C0123456'])"),
			),
			mcp.WithBoolean("include_reactions",
				mcp.Description("Also buffer reaction events. Default is false."),
				mcp.DefaultBool(false),
			),
		), subscriptionsHandler.SubscribeHandler)

		s.AddTool(mcp.NewTool("slack_get_events",
			mcp.WithDescription("Retrieve all buffered events for a persistent subscription. Non-blocking: returns an empty array if no events are queued. Does NOT destroy the subscription."),
			mcp.WithTitleAnnotation("Get Buffered Slack Events"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithString("subscription_id",
				mcp.Required(),
				mcp.Description("The subscription ID returned by slack_subscribe."),
			),
		), subscriptionsHandler.GetEventsHandler)

		s.AddTool(mcp.NewTool("slack_unsubscribe",
			mcp.WithDescription("Destroy a persistent subscription and discard any buffered events."),
			mcp.WithTitleAnnotation("Unsubscribe from Slack Events"),
			mcp.WithDestructiveHintAnnotation(true),
			mcp.WithString("subscription_id",
				mcp.Required(),
				mcp.Description("The subscription ID returned by slack_subscribe."),
			),
		), subscriptionsHandler.UnsubscribeHandler)

		logger.Info("Registered persistent subscription tools (slack_subscribe, slack_get_events, slack_unsubscribe)",
			zap.String("context", "console"),
		)
	}

	// slack_create_channel
	channelsManageHandler := handler.NewChannelsManageHandler(provider, logger)
	s.AddTool(mcp.NewTool("slack_create_channel",
		mcp.WithDescription("Create a new Slack channel. If the channel already exists, returns the existing channel (idempotent)."),
		mcp.WithTitleAnnotation("Create Channel"),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("Channel name (without #, lowercase, no spaces, max 80 chars)."),
		),
		mcp.WithBoolean("is_private",
			mcp.Description("Create as a private channel. Default is false."),
			mcp.DefaultBool(false),
		),
		mcp.WithString("description",
			mcp.Description("Channel purpose/description."),
		),
	), channelsManageHandler.CreateChannelHandler)

	// slack_upload_file
	filesHandler := handler.NewFilesHandler(provider, logger)
	s.AddTool(mcp.NewTool("slack_upload_file",
		mcp.WithDescription("Upload a file (code, logs, images) to a Slack channel or thread."),
		mcp.WithTitleAnnotation("Upload File"),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("channel_id",
			mcp.Required(),
			mcp.Description("Channel ID or name (e.g., 'C0123456' or '#general') to upload to."),
		),
		mcp.WithString("filename",
			mcp.Required(),
			mcp.Description("Name for the uploaded file (e.g., 'crash.log', 'fix.diff')."),
		),
		mcp.WithString("content",
			mcp.Required(),
			mcp.Description("Text content or base64-encoded binary content of the file."),
		),
		mcp.WithString("content_type",
			mcp.Description("MIME type of the content. Default is 'text/plain'."),
			mcp.DefaultString("text/plain"),
		),
		mcp.WithString("title",
			mcp.Description("Title for the file."),
		),
		mcp.WithString("initial_comment",
			mcp.Description("Message to post alongside the file."),
		),
		mcp.WithString("thread_ts",
			mcp.Description("Thread timestamp to upload the file into a thread."),
		),
	), filesHandler.UploadFileHandler)

	// slack_download_file
	s.AddTool(mcp.NewTool("slack_download_file",
		mcp.WithDescription("Download a file shared in Slack by its file ID. Returns content inline or saves to disk."),
		mcp.WithTitleAnnotation("Download File"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("file_id",
			mcp.Required(),
			mcp.Description("Slack file ID (from message event files array, e.g., 'F0123ABCDEF')."),
		),
		mcp.WithString("save_path",
			mcp.Description("Optional local path to save the file. If omitted, returns content directly."),
		),
	), filesHandler.DownloadFileHandler)

	return &MCPServer{
		server: s,
		logger: logger,
	}
}

func (s *MCPServer) ServeSSE(addr string) *server.SSEServer {
	s.logger.Info("Creating SSE server",
		zap.String("context", "console"),
		zap.String("version", version.Version),
		zap.String("build_time", version.BuildTime),
		zap.String("commit_hash", version.CommitHash),
		zap.String("address", addr),
	)
	return server.NewSSEServer(s.server,
		server.WithBaseURL(fmt.Sprintf("http://%s", addr)),
		server.WithSSEContextFunc(func(ctx context.Context, r *http.Request) context.Context {
			ctx = auth.AuthFromRequest(s.logger)(ctx, r)

			return ctx
		}),
	)
}

func (s *MCPServer) ServeHTTP(addr string) *server.StreamableHTTPServer {
	s.logger.Info("Creating HTTP server",
		zap.String("context", "console"),
		zap.String("version", version.Version),
		zap.String("build_time", version.BuildTime),
		zap.String("commit_hash", version.CommitHash),
		zap.String("address", addr),
	)
	return server.NewStreamableHTTPServer(s.server,
		server.WithEndpointPath("/mcp"),
		server.WithHTTPContextFunc(func(ctx context.Context, r *http.Request) context.Context {
			ctx = auth.AuthFromRequest(s.logger)(ctx, r)

			return ctx
		}),
	)
}

func (s *MCPServer) ServeStdio() error {
	s.logger.Info("Starting STDIO server",
		zap.String("version", version.Version),
		zap.String("build_time", version.BuildTime),
		zap.String("commit_hash", version.CommitHash),
	)
	err := server.ServeStdio(s.server)
	if err != nil {
		s.logger.Error("STDIO server error", zap.Error(err))
	}
	return err
}

func buildLoggerMiddleware(logger *zap.Logger) server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			logger.Info("Request received",
				zap.String("tool", req.Params.Name),
				zap.Any("params", req.Params),
			)

			startTime := time.Now()

			res, err := next(ctx, req)

			duration := time.Since(startTime)

			logger.Info("Request finished",
				zap.String("tool", req.Params.Name),
				zap.Duration("duration", duration),
			)

			return res, err
		}
	}
}
