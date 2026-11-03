// Package discordvis decides which Discord channels the bot may show to the
// model by name.
//
// From 16 November 2026 Discord obfuscates guild channels the bot cannot
// view: over the Gateway they arrive with the name "___hidden___", the
// CHANNEL_OBFUSCATED flag (1<<17) and a single overwrite denying VIEW_CHANNEL
// for @everyone; over HTTP they are omitted entirely. Only id, type,
// position and parent_id are real. Everything that lists channels or renders
// a channel name for the model goes through this package so a hidden channel
// is never shown as "___hidden___" and its name never leaks.
package discordvis

import (
	"github.com/bwmarrin/discordgo"
)

const (
	// ChannelFlagObfuscated is Discord's CHANNEL_OBFUSCATED channel flag.
	// discordgo v0.29.0 does not define it.
	ChannelFlagObfuscated discordgo.ChannelFlags = 1 << 17

	// HiddenChannelName is the placeholder name Discord gives obfuscated
	// channels.
	HiddenChannelName = "___hidden___"

	// UnknownChannelName is the neutral name shown to the model for a
	// channel that is hidden from the bot or not known at all.
	UnknownChannelName = "unknown-channel"

	// UnknownChannelMention is UnknownChannelName as a rendered mention.
	UnknownChannelMention = "#" + UnknownChannelName
)

// PermissionFunc returns the bot's computed permissions in a channel.
type PermissionFunc func(channelID string) (int64, error)

// IsObfuscated reports whether Discord sent ch with its metadata obfuscated:
// the CHANNEL_OBFUSCATED flag is set or the name is Discord's placeholder.
// The flag is the documented signal; the name check is a fallback.
func IsObfuscated(ch *discordgo.Channel) bool {
	if ch == nil {
		return false
	}
	return ch.Flags&ChannelFlagObfuscated != 0 || ch.Name == HiddenChannelName
}

// Visible reports whether ch may be shown to the model. A channel is hidden
// when it is obfuscated, or when perms is given and says the bot lacks View
// Channel. When permissions cannot be computed (perms is nil or returns an
// error) the channel is kept: being unable to compute them must not hide
// every channel. Threads are checked against their parent, since discordgo
// computes thread permissions from the thread's own (empty) overwrites.
// Categories skip the permission check: Discord sends a category in full
// whenever any child is viewable, even without View Channel on the category
// itself, and obfuscates it otherwise.
func Visible(ch *discordgo.Channel, perms PermissionFunc) bool {
	if ch == nil {
		return false
	}
	if IsObfuscated(ch) {
		return false
	}
	if perms == nil || ch.Type == discordgo.ChannelTypeGuildCategory {
		return true
	}
	id := ch.ID
	if ch.IsThread() && ch.ParentID != "" {
		id = ch.ParentID
	}
	p, err := perms(id)
	if err != nil {
		return true
	}
	return p&discordgo.PermissionViewChannel != 0
}

// StatePermissions returns a PermissionFunc computing userID's permissions
// from the session state cache, or nil when the state or user is missing.
func StatePermissions(st *discordgo.State, userID string) PermissionFunc {
	if st == nil || userID == "" {
		return nil
	}
	return func(channelID string) (int64, error) {
		return st.UserChannelPermissions(userID, channelID)
	}
}

// StateBotID returns the bot's user ID from the state's READY payload, or ""
// when it is not known yet.
func StateBotID(st *discordgo.State) string {
	if st == nil || st.User == nil {
		return ""
	}
	return st.User.ID
}

// Name returns ch's name when it is visible, and ok=false otherwise.
func Name(ch *discordgo.Channel, perms PermissionFunc) (name string, ok bool) {
	if !Visible(ch, perms) || ch.Name == "" {
		return "", false
	}
	return ch.Name, true
}

// FilterVisible returns the channels that are Visible, in order.
func FilterVisible(channels []*discordgo.Channel, perms PermissionFunc) []*discordgo.Channel {
	out := make([]*discordgo.Channel, 0, len(channels))
	for _, ch := range channels {
		if Visible(ch, perms) {
			out = append(out, ch)
		}
	}
	return out
}
