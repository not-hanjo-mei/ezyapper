package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"ezyapper/internal/logger"

	"github.com/bwmarrin/discordgo"
)

const (
	// defaultSearchLimit is the default number of members returned by searchGuildMembers.
	defaultSearchLimit = 10
	// maxSearchLimit is the maximum number of members searchGuildMembers can return.
	maxSearchLimit = 50
	// discordMemberFetchLimit is Discord's API limit for GuildMembers requests.
	discordMemberFetchLimit = 1000
	// maxChannelMemberLimit is the max number of members returned by getChannelMembers.
	maxChannelMemberLimit = 100
	// defaultChannelMemberLimit is the default number of members returned by getChannelMembers.
	defaultChannelMemberLimit = 20
	// maxRecentMessageLimit caps the number of recent messages that can be fetched.
	maxRecentMessageLimit = 10
)

type DiscordTools struct {
	session *discordgo.Session
}

func NewDiscordTools(session *discordgo.Session) *DiscordTools {
	return &DiscordTools{session: session}
}

func (d *DiscordTools) RegisterTools(registry *ToolRegistry) {
	registry.Register(&Tool{
		Name:        "get_server_info",
		Description: "Get information about a Discord server (guild)",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"guild_id": map[string]any{
					"type":        "string",
					"description": "The ID of the guild to get info for",
				},
			},
			"required": []string{"guild_id"},
		},
		Handler: d.getServerInfo,
	})

	registry.Register(&Tool{
		Name:        "get_channel_info",
		Description: "Get information about a Discord channel",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"channel_id": map[string]any{
					"type":        "string",
					"description": "The ID of the channel to get info for",
				},
			},
			"required": []string{"channel_id"},
		},
		Handler: d.getChannelInfo,
	})

	registry.Register(&Tool{
		Name:        "get_user_info",
		Description: "Get information about a Discord user in a server",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"guild_id": map[string]any{
					"type":        "string",
					"description": "The ID of the guild",
				},
				"user_id": map[string]any{
					"type":        "string",
					"description": "The ID of the user",
				},
			},
			"required": []string{"guild_id", "user_id"},
		},
		Handler: d.getUserInfo,
	})

	registry.Register(&Tool{
		Name:        "list_channels",
		Description: "List all channels in a Discord server",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"guild_id": map[string]any{
					"type":        "string",
					"description": "The ID of the guild to list channels for",
				},
			},
			"required": []string{"guild_id"},
		},
		Handler: d.listChannels,
	})

	registry.Register(&Tool{
		Name:        "get_recent_messages",
		Description: "Get recent messages from a channel (limited to last 10)",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"channel_id": map[string]any{
					"type":        "string",
					"description": "The ID of the channel to get messages from",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Number of messages to retrieve (max 10)",
					"default":     5,
				},
			},
			"required": []string{"channel_id"},
		},
		Handler: d.getRecentMessages,
	})

	registry.Register(&Tool{
		Name:        "create_thread",
		Description: "Create a new thread in a channel",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"channel_id": map[string]any{
					"type":        "string",
					"description": "The ID of the channel to create thread in",
				},
				"message_id": map[string]any{
					"type":        "string",
					"description": "The ID of the message to create thread from",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "The name of the thread",
				},
			},
			"required": []string{"channel_id", "name"},
		},
		Handler: d.createThread,
	})

	registry.Register(&Tool{
		Name:        "add_reaction",
		Description: "Add a reaction emoji to a message",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"channel_id": map[string]any{
					"type":        "string",
					"description": "The ID of the channel",
				},
				"message_id": map[string]any{
					"type":        "string",
					"description": "The ID of the message to react to",
				},
				"emoji": map[string]any{
					"type":        "string",
					"description": "The emoji to react with (unicode or custom emoji ID)",
				},
			},
			"required": []string{"channel_id", "message_id", "emoji"},
		},
		Handler: d.addReaction,
	})

	registry.Register(&Tool{
		Name:        "get_channel_members",
		Description: "Get a list of members in a channel/server for mentioning users. Returns user IDs and usernames that can be used with <@USER_ID> mentions.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"guild_id": map[string]any{
					"type":        "string",
					"description": "The ID of the server/guild to get members from",
				},
				"limit": map[string]any{
					"type":        "number",
					"description": "Maximum number of members to return (default 20, max 100)",
				},
			},
			"required": []string{"guild_id"},
		},
		Handler: d.getChannelMembers,
	})

	registry.Register(&Tool{
		Name:        "search_guild_members",
		Description: "Search for guild members by username or display name. Use this tool when the user wants to ping/mention someone by name but you don't know their exact ID. Returns matching members with their IDs, usernames, and display names. ALWAYS use this tool first when asked to ping someone by name like 'ping john' or 'let ping alex'.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"guild_id": map[string]any{
					"type":        "string",
					"description": "The ID of the server/guild to search members in",
				},
				"query": map[string]any{
					"type":        "string",
					"description": "The name to search for (e.g., 'alex', 'chris', 'john'). Use the name the user mentioned.",
				},
				"limit": map[string]any{
					"type":        "number",
					"description": "Maximum number of members to return (default 10, max 50)",
				},
			},
			"required": []string{"guild_id", "query"},
		},
		Handler: d.searchGuildMembers,
	})

}

func (d *DiscordTools) getServerInfo(ctx context.Context, args map[string]any) (string, error) {
	guildID, err := getStringArg(args, "guild_id")
	if err != nil {
		return "", err
	}

	guild, err := d.session.Guild(guildID)
	if err != nil {
		return "", fmt.Errorf("failed to get guild: %w", err)
	}

	result := map[string]any{
		"id":           guild.ID,
		"name":         guild.Name,
		"member_count": guild.MemberCount,
		"owner_id":     guild.OwnerID,
		"description":  guild.Description,
		"region":       guild.Region,
	}

	return marshalJSON(result)
}

func (d *DiscordTools) getChannelInfo(ctx context.Context, args map[string]any) (string, error) {
	channelID, err := getStringArg(args, "channel_id")
	if err != nil {
		return "", err
	}

	channel, err := d.session.Channel(channelID)
	if err != nil {
		return "", fmt.Errorf("failed to get channel: %w", err)
	}

	result := map[string]any{
		"id":       channel.ID,
		"name":     channel.Name,
		"type":     int(channel.Type),
		"topic":    channel.Topic,
		"guild_id": channel.GuildID,
	}

	return marshalJSON(result)
}

func (d *DiscordTools) getUserInfo(ctx context.Context, args map[string]any) (string, error) {
	guildID, err := getStringArg(args, "guild_id")
	if err != nil {
		return "", err
	}

	userID, err := getStringArg(args, "user_id")
	if err != nil {
		return "", err
	}

	member, err := d.session.GuildMember(guildID, userID)
	if err != nil {
		return "", fmt.Errorf("failed to get member: %w", err)
	}

	result := map[string]any{
		"id":           member.User.ID,
		"username":     member.User.Username,
		"display_name": member.Nick,
		"avatar":       member.User.AvatarURL(""),
		"joined_at":    member.JoinedAt,
		"roles":        member.Roles,
	}

	return marshalJSON(result)
}

func (d *DiscordTools) listChannels(ctx context.Context, args map[string]any) (string, error) {
	guildID, err := getStringArg(args, "guild_id")
	if err != nil {
		return "", err
	}

	channels, err := d.session.GuildChannels(guildID)
	if err != nil {
		return "", fmt.Errorf("failed to get channels: %w", err)
	}

	result := make([]map[string]any, 0, len(channels))
	for _, ch := range channels {
		result = append(result, map[string]any{
			"id":   ch.ID,
			"name": ch.Name,
			"type": int(ch.Type),
		})
	}

	return marshalJSON(result)
}

func (d *DiscordTools) getRecentMessages(ctx context.Context, args map[string]any) (string, error) {
	channelID, err := getStringArg(args, "channel_id")
	if err != nil {
		return "", err
	}

	limit := extractLimit(args, "limit", 5, 10)

	messages, err := d.session.ChannelMessages(channelID, limit, "", "", "")
	if err != nil {
		return "", fmt.Errorf("failed to get messages: %w", err)
	}

	result := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		result = append(result, map[string]any{
			"id":        msg.ID,
			"author":    msg.Author.Username,
			"content":   msg.Content,
			"timestamp": msg.Timestamp,
		})
	}

	return marshalJSON(result)
}

func (d *DiscordTools) createThread(ctx context.Context, args map[string]any) (string, error) {
	channelID, err := getStringArg(args, "channel_id")
	if err != nil {
		return "", err
	}

	name, err := getStringArg(args, "name")
	if err != nil {
		return "", err
	}

	messageID, ok := args["message_id"].(string)
	if !ok && args["message_id"] != nil {
		return "", fmt.Errorf("message_id must be a string")
	}

	var thread *discordgo.Channel

	if messageID != "" {
		thread, err = d.session.MessageThreadStartComplex(channelID, messageID, &discordgo.ThreadStart{
			Name:                name,
			AutoArchiveDuration: 60,
		})
	} else {
		thread, err = d.session.ThreadStart(channelID, name, discordgo.ChannelTypeGuildPublicThread, 60)
	}

	if err != nil {
		return "", fmt.Errorf("failed to create thread: %w", err)
	}

	result := map[string]any{
		"id":   thread.ID,
		"name": thread.Name,
	}

	return marshalJSON(result)
}

func (d *DiscordTools) addReaction(ctx context.Context, args map[string]any) (string, error) {
	channelID, err := getStringArg(args, "channel_id")
	if err != nil {
		return "", err
	}

	messageID, err := getStringArg(args, "message_id")
	if err != nil {
		return "", err
	}

	emoji, err := getStringArg(args, "emoji")
	if err != nil {
		return "", err
	}

	err = d.session.MessageReactionAdd(channelID, messageID, emoji)
	if err != nil {
		return "", fmt.Errorf("failed to add reaction: %w", err)
	}

	return "Reaction added successfully", nil
}

func (d *DiscordTools) getChannelMembers(ctx context.Context, args map[string]any) (string, error) {
	guildID, err := getStringArg(args, "guild_id")
	if err != nil {
		return "", err
	}

	limit := extractLimit(args, "limit", 20, 100)

	members, err := d.session.GuildMembers(guildID, "", limit)
	if err != nil {
		return "", fmt.Errorf("failed to get members: %w", err)
	}

	result := make([]map[string]any, 0, len(members))
	for _, member := range members {
		displayName := member.Nick
		if displayName == "" {
			displayName = member.User.Username
		}
		result = append(result, map[string]any{
			"id":           member.User.ID,
			"username":     member.User.Username,
			"display_name": displayName,
			"mention":      "<@" + member.User.ID + ">",
		})
	}

	return marshalJSON(result)
}

func (d *DiscordTools) searchGuildMembers(ctx context.Context, args map[string]any) (string, error) {
	guildID, err := getStringArg(args, "guild_id")
	if err != nil {
		return "", err
	}

	query, err := getStringArg(args, "query")
	if err != nil {
		return "", err
	}

	limit := extractLimit(args, "limit", defaultSearchLimit, maxSearchLimit)

	queryLower := strings.ToLower(query)
	result := make([]map[string]any, 0, limit)

	// Try to get members from state cache first (enables substring search)
	guild, err := d.session.State.Guild(guildID)
	if err == nil && len(guild.Members) > 0 {
		// Search in cached members
		for _, member := range guild.Members {
			if member.User == nil {
				continue
			}

			username := strings.ToLower(member.User.Username)
			nick := strings.ToLower(member.Nick)
			globalName := strings.ToLower(member.User.GlobalName)
			displayName := member.Nick
			if displayName == "" {
				displayName = member.User.GlobalName
			}
			if displayName == "" {
				displayName = member.User.Username
			}

			// Substring match (case-insensitive) - search in username, nick, and global_name
			if strings.Contains(username, queryLower) ||
				strings.Contains(nick, queryLower) ||
				strings.Contains(globalName, queryLower) {
				result = append(result, map[string]any{
					"id":           member.User.ID,
					"username":     member.User.Username,
					"display_name": displayName,
					"mention":      "<@" + member.User.ID + ">",
				})
				if len(result) >= limit {
					break
				}
			}
		}
	}

	// If no results from state cache, fetch members from API and search locally
	// This handles large guilds where state might not have all members
	if len(result) == 0 {
		// Fetch up to 1000 members (Discord's max limit for GuildMembers)
		allMembers, err := d.session.GuildMembers(guildID, "", discordMemberFetchLimit)
		if err != nil {
			logger.Warnf("[tools] GuildMembers API fetch failed for guild=%s: %v, falling back to Discord search", guildID, err)
		} else {
			for _, member := range allMembers {
				if member.User == nil {
					continue
				}

				username := strings.ToLower(member.User.Username)
				nick := strings.ToLower(member.Nick)
				globalName := strings.ToLower(member.User.GlobalName)
				displayName := member.Nick
				if displayName == "" {
					displayName = member.User.GlobalName
				}
				if displayName == "" {
					displayName = member.User.Username
				}

				// Substring match (case-insensitive) - search in username, nick, and global_name
				if strings.Contains(username, queryLower) ||
					strings.Contains(nick, queryLower) ||
					strings.Contains(globalName, queryLower) {
					result = append(result, map[string]any{
						"id":           member.User.ID,
						"username":     member.User.Username,
						"display_name": displayName,
						"mention":      "<@" + member.User.ID + ">",
					})
					if len(result) >= limit {
						break
					}
				}
			}
		}
	}

	if len(result) == 0 {
		members, err := d.session.GuildMembersSearch(guildID, query, limit)
		if err != nil {
			return "", fmt.Errorf("failed to search members: %w", err)
		}

		for _, member := range members {
			displayName := member.Nick
			if displayName == "" {
				displayName = member.User.Username
			}
			result = append(result, map[string]any{
				"id":           member.User.ID,
				"username":     member.User.Username,
				"display_name": displayName,
				"mention":      "<@" + member.User.ID + ">",
			})
		}
	}

	return marshalJSON(result)
}

// getStringArg extracts a required string argument from the args map.
// Returns an error if the key is missing, the value is not a string, or it is empty.
func getStringArg(args map[string]any, key string) (string, error) {
	val, ok := args[key]
	if !ok {
		return "", fmt.Errorf("missing required argument: %s", key)
	}
	s, ok := val.(string)
	if !ok {
		return "", fmt.Errorf("argument %s must be a string", key)
	}
	if s == "" {
		return "", fmt.Errorf("argument %s must not be empty", key)
	}
	return s, nil
}

// extractLimit extracts an optional integer limit from args with default and max bounds.
func extractLimit(args map[string]any, key string, defaultVal, maxVal int) int {
	limit := defaultVal
	if l, ok := args[key].(float64); ok {
		limit = int(l)
		if limit > maxVal {
			logger.Warnf("[tools] limit %d exceeds recommended maximum %d, honoring user value", limit, maxVal)
		}
	}
	return limit
}

// marshalJSON marshals a value to indented JSON.
// Returns the JSON string or an error if marshaling fails.
func marshalJSON(result any) (string, error) {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal result: %w", err)
	}
	return string(data), nil
}
