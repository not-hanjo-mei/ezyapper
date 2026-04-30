package web

// DiscordAdapter implements DiscordInfoProvider using function fields.
// All methods are non-blocking (no network calls) and return the original ID as fallback on error.
type DiscordAdapter struct {
	channelName func(string) string
	userName    func(string, string) string
	guildName   func(string) string
}

// NewDiscordAdapter creates a new DiscordAdapter with the given lookup functions.
// Each function should be non-blocking and return the resolved name, falling back to the lookup ID on error.
func NewDiscordAdapter(channelName func(string) string, userName func(string, string) string, guildName func(string) string) *DiscordAdapter {
	return &DiscordAdapter{
		channelName: channelName,
		userName:    userName,
		guildName:   guildName,
	}
}

// GetChannelName returns the name of the channel with the given ID.
// Returns channelID as fallback if the channel is not found in state cache.
func (a *DiscordAdapter) GetChannelName(channelID string) string {
	return a.channelName(channelID)
}

// GetUserName returns the username of the user with the given ID in the specified guild.
// Tries guild member lookup first, falls back to global user lookup, then returns userID as fallback.
func (a *DiscordAdapter) GetUserName(guildID, userID string) string {
	return a.userName(guildID, userID)
}

// GetGuildName returns the name of the guild with the given ID.
// Returns guildID as fallback if the guild is not found in state cache.
func (a *DiscordAdapter) GetGuildName(guildID string) string {
	return a.guildName(guildID)
}
