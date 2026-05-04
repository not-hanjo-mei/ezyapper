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
	defaultSearchLimit        = 10
	maxSearchLimit            = 50
	discordMemberFetchLimit   = 1000
	maxChannelMemberLimit     = 100
	defaultChannelMemberLimit = 20
	maxRecentMessageLimit     = 10
)

type DiscordTools struct {
	session *discordgo.Session
}

func NewDiscordTools(session *discordgo.Session) *DiscordTools {
	return &DiscordTools{session: session}
}

type toolDef struct {
	name        string
	description string
	params      []paramDef
	handler     ToolExecutor
}

type paramDef struct {
	name        string
	typ         string
	description string
	required    bool
	defaultVal  any
}

func (td toolDef) toTool() *Tool {
	props := make(map[string]any, len(td.params))
	req := make([]string, 0, len(td.params))

	for _, p := range td.params {
		prop := map[string]any{
			"type":        p.typ,
			"description": p.description,
		}
		if p.defaultVal != nil {
			prop["default"] = p.defaultVal
		}
		props[p.name] = prop
		if p.required {
			req = append(req, p.name)
		}
	}

	return &Tool{
		Name:        td.name,
		Description: td.description,
		Parameters: map[string]any{
			"type":       "object",
			"properties": props,
			"required":   req,
		},
		Handler: td.handler,
	}
}

func (d *DiscordTools) RegisterTools(registry *ToolRegistry) {
	tools := []toolDef{
		{
			name: "get_server_info", description: "Get information about a Discord server (guild)",
			params:  []paramDef{{name: "guild_id", typ: "string", description: "The ID of the guild to get info for", required: true}},
			handler: d.getServerInfo,
		},
		{
			name: "get_channel_info", description: "Get information about a Discord channel",
			params:  []paramDef{{name: "channel_id", typ: "string", description: "The ID of the channel to get info for", required: true}},
			handler: d.getChannelInfo,
		},
		{
			name: "get_user_info", description: "Get information about a Discord user in a server",
			params: []paramDef{
				{name: "guild_id", typ: "string", description: "The ID of the guild", required: true},
				{name: "user_id", typ: "string", description: "The ID of the user", required: true},
			},
			handler: d.getUserInfo,
		},
		{
			name: "list_channels", description: "List all channels in a Discord server",
			params:  []paramDef{{name: "guild_id", typ: "string", description: "The ID of the guild to list channels for", required: true}},
			handler: d.listChannels,
		},
		{
			name: "get_recent_messages", description: "Get recent messages from a channel (limited to last 10)",
			params: []paramDef{
				{name: "channel_id", typ: "string", description: "The ID of the channel to get messages from", required: true},
				{name: "limit", typ: "integer", description: "Number of messages to retrieve (max 10)", defaultVal: 5},
			},
			handler: d.getRecentMessages,
		},
		{
			name: "create_thread", description: "Create a new thread in a channel",
			params: []paramDef{
				{name: "channel_id", typ: "string", description: "The ID of the channel to create thread in", required: true},
				{name: "message_id", typ: "string", description: "The ID of the message to create thread from"},
				{name: "name", typ: "string", description: "The name of the thread", required: true},
			},
			handler: d.createThread,
		},
		{
			name: "add_reaction", description: "Add a reaction emoji to a message",
			params: []paramDef{
				{name: "channel_id", typ: "string", description: "The ID of the channel", required: true},
				{name: "message_id", typ: "string", description: "The ID of the message to react to", required: true},
				{name: "emoji", typ: "string", description: "The emoji to react with (unicode or custom emoji ID)", required: true},
			},
			handler: d.addReaction,
		},
		{
			name: "get_channel_members", description: "Get a list of members in a channel/server for mentioning users. Returns user IDs and usernames that can be used with <@USER_ID> mentions.",
			params: []paramDef{
				{name: "guild_id", typ: "string", description: "The ID of the server/guild to get members from", required: true},
				{name: "limit", typ: "number", description: "Maximum number of members to return (default 20, max 100)"},
			},
			handler: d.getChannelMembers,
		},
		{
			name: "search_guild_members", description: "Search for guild members by username or display name. Use this tool when the user wants to ping/mention someone by name but you don't know their exact ID. Returns matching members with their IDs, usernames, and display names. ALWAYS use this tool first when asked to ping someone by name like 'ping john' or 'let ping alex'.",
			params: []paramDef{
				{name: "guild_id", typ: "string", description: "The ID of the server/guild to search members in", required: true},
				{name: "query", typ: "string", description: "The name to search for (e.g., 'alex', 'chris', 'john'). Use the name the user mentioned.", required: true},
				{name: "limit", typ: "number", description: "Maximum number of members to return (default 10, max 50)"},
			},
			handler: d.searchGuildMembers,
		},
	}

	for _, t := range tools {
		registry.Register(t.toTool())
	}
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
		for _, member := range guild.Members {
			if matched, displayName := matchMember(member, queryLower); matched {
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
		allMembers, err := d.session.GuildMembers(guildID, "", discordMemberFetchLimit)
		if err != nil {
			logger.Warnf("[tools] GuildMembers API fetch failed for guild=%s: %v, falling back to Discord search", guildID, err)
		} else {
			for _, member := range allMembers {
				if matched, displayName := matchMember(member, queryLower); matched {
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

// matchMember checks if a guild member's username, nickname, or global name
// contains the query string (case-insensitive). Returns match result and display name.
func matchMember(member *discordgo.Member, queryLower string) (matched bool, displayName string) {
	if member.User == nil {
		return false, ""
	}

	username := strings.ToLower(member.User.Username)
	nick := strings.ToLower(member.Nick)
	globalName := strings.ToLower(member.User.GlobalName)
	displayName = member.Nick
	if displayName == "" {
		displayName = member.User.GlobalName
	}
	if displayName == "" {
		displayName = member.User.Username
	}

	if strings.Contains(username, queryLower) ||
		strings.Contains(nick, queryLower) ||
		strings.Contains(globalName, queryLower) {
		return true, displayName
	}
	return false, ""
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
