package bot

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"ezyapper/internal/discordvis"
	"ezyapper/internal/memory"
	"ezyapper/internal/plugin"
	"ezyapper/internal/types"

	"github.com/bwmarrin/discordgo"
)

// Prompt layout
//
// The system prompt is byte-identical for every request of a bot instance:
//
//	<instruction>
//	{core.ai.system_prompt}
//	</instruction>
//
//	<{plugin tag}>          static sections registered by plugins at load
//	...
//	</{plugin tag}>
//
// Everything that varies goes into the single user message, ordered from
// "changes least" to "changes most" so provider prompt caches keep the
// longest possible prefix:
//
//	<server_info> <channel_info>   stable per channel
//	<context>                      append-only between window flushes
//	<participants>                 changes when someone new speaks
//	<{plugin tag}> ...             per-reply plugin injections (plugin slot)
//	<user_profile> <key_facts> <recent_memories>
//	<currentMessage>
//
// Messages are rendered as
//
//	[2026-11-01T05:20:11Z] [User] [reply to Bob: "text"] Alice: "content"
//
// with user mentions shown as <@DisplayName>.

// SystemPromptFor builds the static system prompt.
func SystemPromptFor(instructions string, pluginBlocks []plugin.ContextBlock) string {
	var b strings.Builder
	b.WriteString("<instruction>\n")
	b.WriteString(strings.TrimSpace(instructions))
	b.WriteString("\n</instruction>")
	for _, blk := range pluginBlocks {
		writeSection(&b, blk.Tag, blk.Content)
	}
	return b.String()
}

func writeSection(b *strings.Builder, tag, body string) {
	body = strings.TrimSpace(body)
	if body == "" {
		return
	}
	if b.Len() > 0 {
		b.WriteString("\n\n")
	}
	fmt.Fprintf(b, "<%s>\n%s\n</%s>", tag, body, tag)
}

// participant is one person (or bot) in the conversation.
type participant struct {
	ID          string
	Username    string
	DisplayName string
	Role        string // user | bot | assistant (self)
}

// promptInput is everything the user prompt is assembled from.
type promptInput struct {
	GuildID, GuildName, GuildDescription string
	ChannelID, ChannelName, ChannelTopic string
	BotID                                string

	// History is newest first (as fetched), without the current message.
	History []*types.DiscordMessage
	// Current is the message being answered; Content may already include
	// image/audio descriptions.
	Current *types.DiscordMessage
	// Extra users (e.g. the current message's mentions) used to resolve
	// mentions and fill participants.
	MentionedUsers []*discordgo.User

	PluginBlocks []plugin.ContextBlock
	Profile      *memory.Profile
	// Memories is the pre-rendered <key_facts>/<recent_memories> text.
	Memories string

	// ChannelNames maps channel IDs to names for <#id> mentions.
	ChannelNames map[string]string
}

// buildUserPrompt assembles the user message in the cache-friendly order.
func buildUserPrompt(in promptInput) string {
	people := collectParticipants(in)
	names := make(map[string]string, len(people))
	for _, p := range people {
		names[p.ID] = p.DisplayName
	}

	var b strings.Builder

	var server strings.Builder
	fmt.Fprintf(&server, "<server_name>%s</server_name>\n<server_id>%s</server_id>", orUnknown(in.GuildName), orUnknown(in.GuildID))
	if d := strings.TrimSpace(in.GuildDescription); d != "" {
		fmt.Fprintf(&server, "\n<server_description>%s</server_description>", d)
	}
	if in.GuildID != "" {
		writeSection(&b, "server_info", server.String())
	}

	var channel strings.Builder
	fmt.Fprintf(&channel, "<channel_name>%s</channel_name>\n<channel_id>%s</channel_id>", orUnknown(in.ChannelName), orUnknown(in.ChannelID))
	if t := strings.TrimSpace(in.ChannelTopic); t != "" {
		fmt.Fprintf(&channel, "\n<channel_description>%s</channel_description>", t)
	}
	writeSection(&b, "channel_info", channel.String())

	if len(in.History) > 0 {
		lines := make([]string, 0, len(in.History))
		seen := make(map[string]struct{}, len(in.History))
		for i := len(in.History) - 1; i >= 0; i-- {
			m := in.History[i]
			if m == nil || (in.Current != nil && m.ID == in.Current.ID) {
				continue
			}
			if m.ID != "" {
				if _, dup := seen[m.ID]; dup {
					continue
				}
				seen[m.ID] = struct{}{}
			}
			lines = append(lines, renderMessageLine(m, in.BotID, names, in.ChannelNames))
		}
		if len(lines) > 0 {
			writeSection(&b, "context", strings.Join(lines, "\n"))
		}
	}

	if len(people) > 0 {
		var p strings.Builder
		p.WriteString("Format: Name (@username, ID:discord_id) — role")
		for _, person := range people {
			fmt.Fprintf(&p, "\n%s (@%s, ID:%s) — %s", person.DisplayName, person.Username, person.ID, person.Role)
		}
		writeSection(&b, "participants", p.String())
	}

	for _, blk := range in.PluginBlocks {
		writeSection(&b, blk.Tag, blk.Content)
	}

	if prof := renderProfile(in.Profile); prof != "" {
		writeSection(&b, "user_profile", prof)
	}

	if mem := strings.TrimSpace(in.Memories); mem != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(mem)
	}

	if in.Current != nil {
		writeSection(&b, "currentMessage", renderMessageLine(in.Current, in.BotID, names, in.ChannelNames))
	}

	return b.String()
}

func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "unknown"
	}
	return s
}

func displayNameOf(m *types.DiscordMessage) string {
	if m.DisplayName != "" {
		return m.DisplayName
	}
	return m.Username
}

func roleOf(m *types.DiscordMessage, botID string) string {
	switch {
	case botID != "" && m.AuthorID == botID:
		return "Assistant"
	case m.IsBot:
		return "Bot"
	default:
		return "User"
	}
}

// renderMessageLine renders one message in the canonical line format.
func renderMessageLine(m *types.DiscordMessage, botID string, names map[string]string, channels map[string]string) string {
	var b strings.Builder
	ts := m.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}
	fmt.Fprintf(&b, "[%s] [%s] ", ts.UTC().Format(time.RFC3339), roleOf(m, botID))
	switch {
	case m.ReplyToID == "":
	case m.ReplyToUsername == "(deleted message)":
		b.WriteString("[reply to a deleted message] ")
	default:
		who := m.ReplyToUsername
		if n, ok := names[m.ReplyToAuthorID]; ok && m.ReplyToAuthorID != "" {
			who = n
		}
		if m.ReplyToContent != "" {
			fmt.Fprintf(&b, "[reply to %s: %q] ", who, oneLine(resolveMentions(m.ReplyToContent, names, channels), 200))
		} else {
			fmt.Fprintf(&b, "[reply to %s] ", who)
		}
	}
	fmt.Fprintf(&b, "%s: %q", displayNameOf(m), resolveMentions(m.FullText(), names, channels))
	return b.String()
}

func oneLine(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > limit {
		return string(r[:limit]) + "…"
	}
	return s
}

var (
	userMentionRe    = regexp.MustCompile(`<@!?(\d+)>`)
	channelMentionRe = regexp.MustCompile(`<#(\d+)>`)
)

// resolveMentions turns <@id> into <@DisplayName> for known users (unknown
// user IDs are left untouched) and <#id> into #name. channels holds only the
// channels the bot can view; a channel missing from it (hidden from the bot,
// deleted, or in another server) renders as #unknown-channel, so neither
// Discord's "___hidden___" placeholder nor a bare ID reaches the model. A nil
// channels map means no channel info is available and leaves <#id> as is.
func resolveMentions(content string, names map[string]string, channels map[string]string) string {
	content = userMentionRe.ReplaceAllStringFunc(content, func(m string) string {
		id := userMentionRe.FindStringSubmatch(m)[1]
		if n, ok := names[id]; ok && n != "" {
			return "<@" + n + ">"
		}
		return m
	})
	if channels != nil {
		content = channelMentionRe.ReplaceAllStringFunc(content, func(m string) string {
			id := channelMentionRe.FindStringSubmatch(m)[1]
			if n, ok := channels[id]; ok && n != "" && n != discordvis.HiddenChannelName {
				return "#" + n
			}
			return discordvis.UnknownChannelMention
		})
	}
	return content
}

// collectParticipants lists everyone in the window in order of first
// appearance (oldest first), then the current author, mentioned users and
// the bot itself. The order is stable while the window only appends.
func collectParticipants(in promptInput) []participant {
	var out []participant
	idx := map[string]int{}
	add := func(id, username, display, role string) {
		if id == "" {
			return
		}
		if display == "" {
			display = username
		}
		if i, ok := idx[id]; ok {
			// Newer messages carry the current names.
			if display != "" {
				out[i].DisplayName = display
			}
			if username != "" {
				out[i].Username = username
			}
			return
		}
		idx[id] = len(out)
		out = append(out, participant{ID: id, Username: username, DisplayName: display, Role: role})
	}
	roleFor := func(m *types.DiscordMessage) string {
		switch roleOf(m, in.BotID) {
		case "Assistant":
			return "assistant (self)"
		case "Bot":
			return "bot"
		}
		return "user"
	}
	for i := len(in.History) - 1; i >= 0; i-- {
		if m := in.History[i]; m != nil {
			add(m.AuthorID, m.Username, displayNameOf(m), roleFor(m))
		}
	}
	if in.Current != nil {
		add(in.Current.AuthorID, in.Current.Username, displayNameOf(in.Current), roleFor(in.Current))
	}
	for _, u := range in.MentionedUsers {
		if u == nil {
			continue
		}
		role := "user"
		if u.ID == in.BotID {
			role = "assistant (self)"
		} else if u.Bot {
			role = "bot"
		}
		add(u.ID, u.Username, u.GlobalName, role)
	}
	return out
}

// renderProfile renders the user profile body (traits, facts, preferences,
// interests), with map entries sorted for byte-stable output.
func renderProfile(p *memory.Profile) string {
	if p == nil {
		return ""
	}
	var lines []string
	if len(p.Traits) > 0 {
		lines = append(lines, "traits: "+strings.Join(p.Traits, ", "))
	}
	if len(p.Interests) > 0 {
		lines = append(lines, "interests: "+strings.Join(p.Interests, ", "))
	}
	for _, kv := range sortedPairs(p.Facts) {
		lines = append(lines, kv)
	}
	if prefs := sortedPairs(p.Preferences); len(prefs) > 0 {
		lines = append(lines, "preferences: "+strings.Join(prefs, ", "))
	}
	return strings.Join(lines, "\n")
}

func sortedPairs(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+": "+m[k])
	}
	return out
}

var nameMentionRe = regexp.MustCompile(`<@([^>\n]{1,100})>`)

// mentionsToIDs converts <@Name> mentions the model wrote (mirroring the
// prompt format) back into Discord <@id> mentions. Only exact,
// case-insensitive matches against participants' display names or
// usernames are converted, and only when the name is unambiguous; anything
// else, including real <@id> mentions, is left unchanged.
func mentionsToIDs(text string, people []participant) string {
	if len(people) == 0 || !strings.Contains(text, "<@") {
		return text
	}
	byName := map[string]string{}
	ambiguous := map[string]bool{}
	addName := func(name, id string) {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" {
			return
		}
		if prev, ok := byName[key]; ok && prev != id {
			ambiguous[key] = true
		}
		byName[key] = id
	}
	for _, p := range people {
		addName(p.DisplayName, p.ID)
		addName(p.Username, p.ID)
	}
	return nameMentionRe.ReplaceAllStringFunc(text, func(m string) string {
		name := nameMentionRe.FindStringSubmatch(m)[1]
		name = strings.TrimPrefix(strings.TrimPrefix(name, "!"), "@")
		if userMentionRe.MatchString(m) {
			return m
		}
		key := strings.ToLower(strings.TrimSpace(name))
		if id, ok := byName[key]; ok && !ambiguous[key] {
			return "<@" + id + ">"
		}
		return m
	})
}
