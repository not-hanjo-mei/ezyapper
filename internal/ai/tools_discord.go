package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
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
	guildID, ok := args["guild_id"].(string)
	if !ok {
		return "", fmt.Errorf("invalid guild_id")
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

	data, _ := json.MarshalIndent(result, "", "  ")
	return string(data), nil
}

func (d *DiscordTools) getChannelInfo(ctx context.Context, args map[string]any) (string, error) {
	channelID, ok := args["channel_id"].(string)
	if !ok {
		return "", fmt.Errorf("invalid channel_id")
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

	data, _ := json.MarshalIndent(result, "", "  ")
	return string(data), nil
}

func (d *DiscordTools) getUserInfo(ctx context.Context, args map[string]any) (string, error) {
	guildID, ok := args["guild_id"].(string)
	if !ok {
		return "", fmt.Errorf("invalid guild_id")
	}

	userID, ok := args["user_id"].(string)
	if !ok {
		return "", fmt.Errorf("invalid user_id")
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

	data, _ := json.MarshalIndent(result, "", "  ")
	return string(data), nil
}

func (d *DiscordTools) listChannels(ctx context.Context, args map[string]any) (string, error) {
	guildID, ok := args["guild_id"].(string)
	if !ok {
		return "", fmt.Errorf("invalid guild_id")
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

	data, _ := json.MarshalIndent(result, "", "  ")
	return string(data), nil
}

func (d *DiscordTools) getRecentMessages(ctx context.Context, args map[string]any) (string, error) {
	channelID, ok := args["channel_id"].(string)
	if !ok {
		return "", fmt.Errorf("invalid channel_id")
	}

	limit := 5
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
		if limit > 10 {
			limit = 10
		}
	}

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

	data, _ := json.MarshalIndent(result, "", "  ")
	return string(data), nil
}

func (d *DiscordTools) createThread(ctx context.Context, args map[string]any) (string, error) {
	channelID, ok := args["channel_id"].(string)
	if !ok {
		return "", fmt.Errorf("invalid channel_id")
	}

	name, ok := args["name"].(string)
	if !ok {
		return "", fmt.Errorf("invalid name")
	}

	messageID, _ := args["message_id"].(string)

	var thread *discordgo.Channel
	var err error

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

	data, _ := json.MarshalIndent(result, "", "  ")
	return string(data), nil
}

func (d *DiscordTools) addReaction(ctx context.Context, args map[string]any) (string, error) {
	channelID, ok := args["channel_id"].(string)
	if !ok {
		return "", fmt.Errorf("invalid channel_id")
	}

	messageID, ok := args["message_id"].(string)
	if !ok {
		return "", fmt.Errorf("invalid message_id")
	}

	emoji, ok := args["emoji"].(string)
	if !ok {
		return "", fmt.Errorf("invalid emoji")
	}

	err := d.session.MessageReactionAdd(channelID, messageID, emoji)
	if err != nil {
		return "", fmt.Errorf("failed to add reaction: %w", err)
	}

	return "Reaction added successfully", nil
}

func (d *DiscordTools) getChannelMembers(ctx context.Context, args map[string]any) (string, error) {
	guildID, ok := args["guild_id"].(string)
	if !ok {
		return "", fmt.Errorf("invalid guild_id")
	}

	limit := 20
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
		if limit > 100 {
			limit = 100
		}
	}

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

	data, _ := json.MarshalIndent(result, "", "  ")
	return string(data), nil
}

func (d *DiscordTools) searchGuildMembers(ctx context.Context, args map[string]any) (string, error) {
	guildID, ok := args["guild_id"].(string)
	if !ok {
		return "", fmt.Errorf("invalid guild_id")
	}

	query, ok := args["query"].(string)
	if !ok || query == "" {
		return "", fmt.Errorf("invalid query")
	}

	limit := 10
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
		if limit > 50 {
			limit = 50
		}
	}

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
		allMembers, err := d.session.GuildMembers(guildID, "", 1000)
		if err != nil {
			// API error, will fall back to Discord's search
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

	// Final fallback: use Discord's search API (prefix match only)
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

	data, _ := json.MarshalIndent(result, "", "  ")
	return string(data), nil
}
