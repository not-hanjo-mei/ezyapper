// Package bot provides Discord bot event handlers
package bot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ezyapper/internal/logger"
	"ezyapper/internal/plugin"
	"ezyapper/internal/types"

	"github.com/bwmarrin/discordgo"
)

// localUploadFile represents a file to be uploaded to Discord
type localUploadFile struct {
	Path              string
	Name              string
	ContentType       string
	Data              []byte
	DeleteAfterUpload bool
}

// maintainTyping keeps the typing indicator alive by sending it periodically
func maintainTyping(ctx context.Context, s *discordgo.Session, channelID string, intervalSec int) {
	ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
	defer ticker.Stop()

	for {
		s.ChannelTyping(channelID)

		select {
		case <-ticker.C:
			// Continue maintaining typing
		case <-ctx.Done():
			// Stop maintaining when parent context is cancelled
			return
		}
	}
}

// pluginFilesToLocalUploadFiles converts plugin files to local upload files
func pluginFilesToLocalUploadFiles(files []plugin.LocalFile) []localUploadFile {
	if len(files) == 0 {
		return nil
	}

	converted := make([]localUploadFile, 0, len(files))
	for _, f := range files {
		converted = append(converted, localUploadFile{
			Path:              strings.TrimSpace(f.Path),
			Name:              strings.TrimSpace(f.Name),
			ContentType:       strings.TrimSpace(f.ContentType),
			Data:              f.Data,
			DeleteAfterUpload: f.DeleteAfterUpload,
		})
	}

	return converted
}

// runBeforeSendPluginHooks runs plugin hooks before sending a message
func (b *Bot) runBeforeSendPluginHooks(
	ctx context.Context,
	m *discordgo.MessageCreate,
	response string,
) (string, []localUploadFile, bool, error) {
	if b.pluginManager == nil {
		return response, nil, false, nil
	}

	updatedResponse, files, skipSend, err := b.pluginManager.BeforeSend(ctx, m, response)
	if err != nil {
		return "", nil, false, err
	}

	return updatedResponse, pluginFilesToLocalUploadFiles(files), skipSend, nil
}

// closeAllClosers closes all closers, logging any close errors.
func closeAllClosers(closers []io.Closer) {
	for _, closer := range closers {
		if closer != nil {
			if err := closer.Close(); err != nil {
				logger.Warnf("[send] failed to close file: %v", err)
			}
		}
	}
}

// cleanupUploadedTempFiles cleans up temporary upload files
func cleanupUploadedTempFiles(paths []string) {
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			logger.Warnf("[send] failed to remove temp upload file %s: %v", path, err)
		}
	}
}

// buildDiscordFiles builds Discord file objects from local upload files
func buildDiscordFiles(localFiles []localUploadFile) ([]*discordgo.File, []io.Closer, []string, error) {
	if len(localFiles) == 0 {
		return nil, nil, nil, nil
	}

	files := make([]*discordgo.File, 0, len(localFiles))
	closers := make([]io.Closer, 0, len(localFiles))
	cleanupPaths := make([]string, 0, len(localFiles))

	for _, localFile := range localFiles {
		path := strings.TrimSpace(localFile.Path)
		name := strings.TrimSpace(localFile.Name)
		contentType := strings.TrimSpace(localFile.ContentType)

		if len(localFile.Data) > 0 {
			if name == "" {
				if path != "" {
					name = filepath.Base(path)
				} else {
					name = "upload.bin"
				}
			}

			files = append(files, &discordgo.File{
				Name:        name,
				ContentType: contentType,
				Reader:      bytes.NewReader(localFile.Data),
			})

			if localFile.DeleteAfterUpload && path != "" {
				cleanupPaths = append(cleanupPaths, path)
			}
			continue
		}

		if path == "" {
			closeAllClosers(closers)
			cleanupUploadedTempFiles(cleanupPaths)
			return nil, nil, nil, fmt.Errorf("file upload requires either data or path")
		}

		openedFile, err := os.Open(path)
		if err != nil {
			closeAllClosers(closers)
			cleanupUploadedTempFiles(cleanupPaths)
			return nil, nil, nil, fmt.Errorf("open upload file %s: %w", path, err)
		}
		closers = append(closers, openedFile)

		if name == "" {
			name = filepath.Base(path)
		}

		files = append(files, &discordgo.File{
			Name:        name,
			ContentType: contentType,
			Reader:      openedFile,
		})

		if localFile.DeleteAfterUpload {
			cleanupPaths = append(cleanupPaths, path)
		}
	}

	return files, closers, cleanupPaths, nil
}

// sendMessageWithLocalFiles sends a message with local files
func (b *Bot) sendMessageWithLocalFiles(
	s *discordgo.Session,
	m *discordgo.MessageCreate,
	content string,
	localFiles []localUploadFile,
	asReply bool,
) (*discordgo.Message, error) {
	if len(localFiles) == 0 {
		if asReply {
			return s.ChannelMessageSendReply(m.ChannelID, content, m.Reference())
		}

		return s.ChannelMessageSend(m.ChannelID, content)
	}

	discordFiles, closers, cleanupPaths, err := buildDiscordFiles(localFiles)
	if err != nil {
		return nil, err
	}
	defer closeAllClosers(closers)
	defer cleanupUploadedTempFiles(cleanupPaths)

	messageSend := &discordgo.MessageSend{
		Content: content,
		Files:   discordFiles,
	}
	if asReply {
		messageSend.Reference = m.Reference()
	}

	return s.ChannelMessageSendComplex(m.ChannelID, messageSend)
}

// buildBotMessage creates a DiscordMessage record for the bot's own sent message.
func (b *Bot) buildBotMessage(sentMsg *discordgo.Message, m *discordgo.MessageCreate, s *discordgo.Session) *types.DiscordMessage {
	return &types.DiscordMessage{
		ID:        sentMsg.ID,
		ChannelID: sentMsg.ChannelID,
		GuildID:   m.GuildID,
		AuthorID:  s.State.User.ID,
		Username:  s.State.User.Username,
		Content:   sentMsg.Content,
		Timestamp: sentMsg.Timestamp,
		IsBot:     true,
	}
}

// sendResponse sends a response to a channel
func (b *Bot) sendResponse(ctx context.Context, s *discordgo.Session, m *discordgo.MessageCreate, response string) error {
	response, localFiles, skipSend, err := b.runBeforeSendPluginHooks(ctx, m, response)
	if err != nil {
		return err
	}
	if skipSend {
		logger.Infof("[send] skipped by plugin before_send hook: message=%s channel=%s", m.ID, m.ChannelID)
		return nil
	}

	// Check if response is too long
	if len(response) > discordMessageLimit {
		// Split into multiple messages
		return b.sendLongResponse(s, m, response, localFiles)
	}

	// Check if we should reply or just send
	var sentMsg *discordgo.Message
	if b.IsBotMentioned(m) || m.ReferencedMessage != nil {
		sentMsg, err = b.sendMessageWithLocalFiles(s, m, response, localFiles, true)
	} else {
		sentMsg, err = b.sendMessageWithLocalFiles(s, m, response, localFiles, false)
	}

	if err != nil {
		return err
	}

	// Add bot's own message to channel buffer for complete conversation context
	if sentMsg != nil {
		botMsg := b.buildBotMessage(sentMsg, m, s)
		b.addMessageToChannelBuffer(m.ChannelID, botMsg)
	}

	return nil
}

// sendLongResponse sends a response that exceeds Discord's character limit
func (b *Bot) sendLongResponse(s *discordgo.Session, m *discordgo.MessageCreate, response string, firstChunkFiles []localUploadFile) error {
	var chunks []string
	remaining := response

	for len(remaining) > 0 {
		if len(remaining) <= discordChunkLimit {
			chunks = append(chunks, remaining)
			break
		}

		splitAt := strings.LastIndex(remaining[:discordChunkLimit], "\n")
		if splitAt <= 0 {
			splitAt = strings.LastIndex(remaining[:discordChunkLimit], " ")
		}
		if splitAt <= 0 {
			splitAt = discordChunkLimit
		}

		chunk := strings.TrimSpace(remaining[:splitAt])
		if chunk == "" {
			chunk = remaining[:splitAt]
		}
		chunks = append(chunks, chunk)
		remaining = strings.TrimLeft(remaining[splitAt:], "\n ")
	}

	// Send chunks
	for i, chunk := range chunks {
		if i > 0 {
			time.Sleep(time.Duration(b.cfg().Discord.LongResponseDelayMs) * time.Millisecond) // Rate limit protection
		}

		var sentMsg *discordgo.Message
		var err error

		if i == 0 {
			// First chunk as reply and attach generated files.
			sentMsg, err = b.sendMessageWithLocalFiles(s, m, chunk, firstChunkFiles, true)
		} else {
			// Subsequent chunks as normal messages
			sentMsg, err = b.sendMessageWithLocalFiles(s, m, chunk, nil, false)
		}

		if err != nil {
			return err
		}

		// Add bot's own message to channel buffer
		if sentMsg != nil {
			botMsg := b.buildBotMessage(sentMsg, m, s)
			b.addMessageToChannelBuffer(m.ChannelID, botMsg)
		}
	}

	return nil
}
