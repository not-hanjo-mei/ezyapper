package web

import (
	"github.com/bwmarrin/discordgo"
)

// DiscordAdapter implements DiscordInfoProvider using Discord's in-memory state cache.
// All methods are non-blocking (no network calls) and return the original ID as fallback on error.
type DiscordAdapter struct {
	session *discordgo.Session
}

// NewDiscordAdapter creates a new DiscordAdapter wrapping the given Discord session.
func NewDiscordAdapter(s *discordgo.Session) *DiscordAdapter {
	return &DiscordAdapter{session: s}
}

// GetChannelName returns the name of the channel with the given ID.
// Returns channelID as fallback if the channel is not found in state cache.
func (a *DiscordAdapter) GetChannelName(channelID string) string {
	ch, err := a.session.State.Channel(channelID)
	if err != nil || ch == nil {
		return channelID
	}
	return ch.Name
}

// GetUserName returns the username of the user with the given ID in the specified guild.
// Tries guild member lookup first, falls back to global user lookup, then returns userID as fallback.
func (a *DiscordAdapter) GetUserName(guildID, userID string) string {
	member, err := a.session.State.Member(guildID, userID)
	if err == nil && member != nil && member.User != nil {
		return member.User.Username
	}
	user, err := a.session.User(userID)
	if err == nil && user != nil {
		return user.Username
	}
	return userID
}

// GetGuildName returns the name of the guild with the given ID.
// Returns guildID as fallback if the guild is not found in state cache.
func (a *DiscordAdapter) GetGuildName(guildID string) string {
	g, err := a.session.State.Guild(guildID)
	if err != nil || g == nil {
		return guildID
	}
	return g.Name
}
