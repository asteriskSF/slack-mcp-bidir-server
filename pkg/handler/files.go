package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/korotovsky/slack-mcp-server/pkg/provider"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	"go.uber.org/zap"
)

const maxInlineFileSize = 10 * 1024 * 1024 // 10MB

// FilesHandler handles file upload and download operations.
type FilesHandler struct {
	apiProvider *provider.ApiProvider
	logger      *zap.Logger
}

// NewFilesHandler creates a new FilesHandler.
func NewFilesHandler(apiProvider *provider.ApiProvider, logger *zap.Logger) *FilesHandler {
	return &FilesHandler{
		apiProvider: apiProvider,
		logger:      logger,
	}
}

// UploadFileHandler uploads a file to a Slack channel.
func (h *FilesHandler) UploadFileHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("UploadFileHandler called", zap.Any("params", request.Params))

	channelID := request.GetString("channel_id", "")
	if channelID == "" {
		return nil, errors.New("channel_id parameter is required")
	}

	// Resolve channel name to ID if needed
	channelID, err := h.resolveChannelID(channelID)
	if err != nil {
		return nil, err
	}

	filename := request.GetString("filename", "")
	if filename == "" {
		return nil, errors.New("filename parameter is required")
	}

	content := request.GetString("content", "")
	if content == "" {
		return nil, errors.New("content parameter is required")
	}

	title := request.GetString("title", "")
	initialComment := request.GetString("initial_comment", "")
	threadTS := request.GetString("thread_ts", "")

	h.logger.Debug("Uploading file",
		zap.String("channel_id", channelID),
		zap.String("filename", filename),
		zap.Int("content_length", len(content)),
	)

	slackClient := h.apiProvider.Slack().(*provider.MCPSlackClient).Raw().Slack

	params := slack.UploadFileV2Parameters{
		Channel:        channelID,
		Filename:       filename,
		FileSize:       len(content),
		Reader:         strings.NewReader(content),
		Title:          title,
		InitialComment: initialComment,
		ThreadTimestamp: threadTS,
	}

	summary, err := slackClient.UploadFileV2Context(ctx, params)
	if err != nil {
		h.logger.Error("Failed to upload file",
			zap.String("channel_id", channelID),
			zap.String("filename", filename),
			zap.Error(err),
		)
		return nil, fmt.Errorf("failed to upload file: %w", err)
	}

	result := map[string]interface{}{
		"ok":       true,
		"file_id":  summary.ID,
		"filename": filename,
		"title":    title,
	}

	jsonBytes, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}

	h.logger.Info("File uploaded successfully",
		zap.String("file_id", summary.ID),
		zap.String("filename", filename),
	)

	return mcp.NewToolResultText(string(jsonBytes)), nil
}

// DownloadFileHandler downloads a file from Slack.
func (h *FilesHandler) DownloadFileHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("DownloadFileHandler called", zap.Any("params", request.Params))

	fileID := request.GetString("file_id", "")
	if fileID == "" {
		return nil, errors.New("file_id parameter is required")
	}

	savePath := request.GetString("save_path", "")

	h.logger.Debug("Downloading file",
		zap.String("file_id", fileID),
		zap.String("save_path", savePath),
	)

	slackClient := h.apiProvider.Slack().(*provider.MCPSlackClient).Raw().Slack

	// Get file info
	fileInfo, _, _, err := slackClient.GetFileInfoContext(ctx, fileID, 0, 0)
	if err != nil {
		h.logger.Error("Failed to get file info",
			zap.String("file_id", fileID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("failed to get file info: %w", err)
	}

	// Download the file content
	fileContent, err := h.downloadFileContent(ctx, fileInfo, slackClient)
	if err != nil {
		return nil, err
	}

	if savePath != "" {
		// Save to disk
		err := os.WriteFile(savePath, fileContent, 0644)
		if err != nil {
			h.logger.Error("Failed to save file to disk",
				zap.String("save_path", savePath),
				zap.Error(err),
			)
			return nil, fmt.Errorf("failed to save file to %q: %w", savePath, err)
		}

		result := map[string]interface{}{
			"ok":         true,
			"filename":   fileInfo.Name,
			"filetype":   fileInfo.Filetype,
			"mimetype":   fileInfo.Mimetype,
			"size_bytes": fileInfo.Size,
			"saved_to":   savePath,
		}

		jsonBytes, _ := json.Marshal(result)

		h.logger.Info("File saved to disk",
			zap.String("file_id", fileID),
			zap.String("save_path", savePath),
		)

		return mcp.NewToolResultText(string(jsonBytes)), nil
	}

	// Return content inline
	if int64(len(fileContent)) > maxInlineFileSize {
		return nil, fmt.Errorf("file too large for inline return (%d bytes); use save_path parameter", len(fileContent))
	}

	var contentStr string
	if isTextMimetype(fileInfo.Mimetype) {
		contentStr = string(fileContent)
	} else {
		contentStr = base64.StdEncoding.EncodeToString(fileContent)
	}

	result := map[string]interface{}{
		"ok":         true,
		"filename":   fileInfo.Name,
		"filetype":   fileInfo.Filetype,
		"mimetype":   fileInfo.Mimetype,
		"size_bytes": fileInfo.Size,
		"content":    contentStr,
	}

	jsonBytes, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}

	h.logger.Info("File downloaded successfully",
		zap.String("file_id", fileID),
		zap.String("filename", fileInfo.Name),
		zap.Int("size_bytes", fileInfo.Size),
	)

	return mcp.NewToolResultText(string(jsonBytes)), nil
}

// downloadFileContent fetches the file content from Slack's url_private.
func (h *FilesHandler) downloadFileContent(_ context.Context, fileInfo *slack.File, client *slack.Client) ([]byte, error) {
	if fileInfo.URLPrivateDownload == "" && fileInfo.URLPrivate == "" {
		return nil, fmt.Errorf("file %q has no downloadable URL", fileInfo.ID)
	}

	downloadURL := fileInfo.URLPrivateDownload
	if downloadURL == "" {
		downloadURL = fileInfo.URLPrivate
	}

	// slack-go's GetFile handles authorization internally using the client's token
	var buf bytes.Buffer
	err := client.GetFile(downloadURL, &buf)
	if err != nil {
		return nil, fmt.Errorf("failed to download file: %w", err)
	}

	return buf.Bytes(), nil
}

// resolveChannelID resolves a channel name to a channel ID.
func (h *FilesHandler) resolveChannelID(channel string) (string, error) {
	if !strings.HasPrefix(channel, "#") && !strings.HasPrefix(channel, "@") {
		return channel, nil
	}

	channelsMaps := h.apiProvider.ProvideChannelsMaps()
	id, ok := channelsMaps.ChannelsInv[channel]
	if !ok {
		return "", fmt.Errorf("channel %q not found", channel)
	}
	return channelsMaps.Channels[id].ID, nil
}

// isTextMimetype returns true if the MIME type represents text content.
func isTextMimetype(mimetype string) bool {
	if strings.HasPrefix(mimetype, "text/") {
		return true
	}
	textTypes := []string{
		"application/json",
		"application/xml",
		"application/javascript",
		"application/x-yaml",
		"application/yaml",
		"application/x-sh",
		"application/x-python",
	}
	for _, t := range textTypes {
		if mimetype == t {
			return true
		}
	}
	return false
}

