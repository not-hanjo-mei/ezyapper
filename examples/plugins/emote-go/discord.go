package main

import (
	"fmt"
	"os"

	"github.com/bwmarrin/discordgo"
)

type DiscordSession struct {
	session *discordgo.Session
}

func (d *DiscordSession) Connect(token string) error {
	if token == "" {
		return nil // no token = silently disabled
	}
	var err error
	d.session, err = discordgo.New("Bot " + token)
	if err != nil {
		return fmt.Errorf("failed to create Discord session: %w", err)
	}
	d.session.Identify.Intents = 0 // we only send, no receiving
	if err := d.session.Open(); err != nil {
		return fmt.Errorf("failed to open Discord connection: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[EMOTE] Discord session connected\n")
	return nil
}

func (d *DiscordSession) SendMessage(channelID, content string) error {
	if d.session == nil {
		return nil // silently skip if not connected
	}
	_, err := d.session.ChannelMessageSend(channelID, content)
	return err
}

func (d *DiscordSession) SendFile(channelID, path string) error {
	if d.session == nil {
		return nil
	}
	reader, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", path, err)
	}
	defer reader.Close()
	_, err = d.session.ChannelFileSend(channelID, path, reader)
	return err
}

func (d *DiscordSession) Close() error {
	if d.session != nil {
		return d.session.Close()
	}
	return nil
}
