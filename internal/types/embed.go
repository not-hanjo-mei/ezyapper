package types

import (
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// maxEmbedTextRunes caps the rendered text of one embed so a huge bot embed
// cannot flood the prompt.
const maxEmbedTextRunes = 1500

// EmbedSummary is the readable part of a Discord embed.
type EmbedSummary struct {
	Type        string       `json:"type,omitempty"`
	Provider    string       `json:"provider,omitempty"`
	Author      string       `json:"author,omitempty"`
	Title       string       `json:"title,omitempty"`
	URL         string       `json:"url,omitempty"`
	Description string       `json:"description,omitempty"`
	Fields      []EmbedField `json:"fields,omitempty"`
	Footer      string       `json:"footer,omitempty"`
}

// EmbedField is one name/value pair of an embed.
type EmbedField struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// SummarizeEmbeds extracts the text of every embed that carries any. Pure
// image/gifv embeds have no text and are left to the vision path.
func SummarizeEmbeds(embeds []*discordgo.MessageEmbed) []EmbedSummary {
	var out []EmbedSummary
	for _, e := range embeds {
		if e == nil {
			continue
		}
		s := EmbedSummary{
			Type:        string(e.Type),
			Title:       strings.TrimSpace(e.Title),
			URL:         strings.TrimSpace(e.URL),
			Description: strings.TrimSpace(e.Description),
		}
		if e.Provider != nil {
			s.Provider = strings.TrimSpace(e.Provider.Name)
		}
		if e.Author != nil {
			s.Author = strings.TrimSpace(e.Author.Name)
		}
		if e.Footer != nil {
			s.Footer = strings.TrimSpace(e.Footer.Text)
		}
		for _, f := range e.Fields {
			if f == nil {
				continue
			}
			name, value := strings.TrimSpace(f.Name), strings.TrimSpace(f.Value)
			if name == "" && value == "" {
				continue
			}
			s.Fields = append(s.Fields, EmbedField{Name: name, Value: value})
		}
		if s.Title == "" && s.Description == "" && s.Author == "" && len(s.Fields) == 0 && s.Footer == "" {
			continue
		}
		out = append(out, s)
	}
	return out
}

// Render formats the embed as one compact line for the model, for example:
//
//	[Embed (YouTube) "Title" by Author: description | Field: value | footer]
func (s EmbedSummary) Render() string {
	var b strings.Builder
	b.WriteString("[Embed")
	if s.Provider != "" {
		fmt.Fprintf(&b, " (%s)", s.Provider)
	}
	if s.Title != "" {
		fmt.Fprintf(&b, " %q", s.Title)
	}
	if s.Author != "" {
		fmt.Fprintf(&b, " by %s", s.Author)
	}
	parts := []string{}
	if s.Description != "" {
		parts = append(parts, s.Description)
	}
	for _, f := range s.Fields {
		switch {
		case f.Name == "":
			parts = append(parts, f.Value)
		case f.Value == "":
			parts = append(parts, f.Name)
		default:
			parts = append(parts, f.Name+": "+f.Value)
		}
	}
	if s.Footer != "" {
		parts = append(parts, s.Footer)
	}
	if len(parts) > 0 {
		b.WriteString(": ")
		b.WriteString(strings.Join(parts, " | "))
	}
	b.WriteString("]")
	return truncateRunes(collapseNewlines(b.String()), maxEmbedTextRunes)
}

// RenderEmbeds renders every summary on its own line.
func RenderEmbeds(embeds []EmbedSummary) string {
	if len(embeds) == 0 {
		return ""
	}
	lines := make([]string, len(embeds))
	for i, e := range embeds {
		lines[i] = e.Render()
	}
	return strings.Join(lines, "\n")
}

// FullText returns the message content followed by its rendered embeds.
func (m DiscordMessage) FullText() string {
	return JoinContentAndEmbeds(m.Content, m.Embeds)
}

// JoinContentAndEmbeds appends rendered embeds to content.
func JoinContentAndEmbeds(content string, embeds []EmbedSummary) string {
	rendered := RenderEmbeds(embeds)
	switch {
	case rendered == "":
		return content
	case strings.TrimSpace(content) == "":
		return rendered
	default:
		return content + "\n" + rendered
	}
}

func collapseNewlines(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
}

func truncateRunes(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit-2]) + "…]"
}
