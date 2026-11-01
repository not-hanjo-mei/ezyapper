// Package filter applies regex-based allow/ignore rules to incoming
// messages. Rules are evaluated in order and the first match wins; when no
// rule matches, the configured default action applies. An ignored message is
// neither answered nor shown to the model as context.
package filter

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"ezyapper/internal/types"
)

// Action is what happens to a message a rule matches.
type Action string

const (
	ActionAllow  Action = "allow"
	ActionIgnore Action = "ignore"
)

// RuleConfig is one rule as written in the config file. Every pattern is a
// Go regular expression searched in the value, unanchored: use ^...$ for an
// exact ID. Empty fields match everything. Kinds matches when the
// message has any of the listed kinds; KindsAll requires all of them.
type RuleConfig struct {
	Name     string   `mapstructure:"name" yaml:"name"`
	Action   string   `mapstructure:"action" yaml:"action"`
	Guild    string   `mapstructure:"guild" yaml:"guild"`
	Channel  string   `mapstructure:"channel" yaml:"channel"`
	User     string   `mapstructure:"user" yaml:"user"`
	Username string   `mapstructure:"username" yaml:"username"`
	Content  string   `mapstructure:"content" yaml:"content"`
	Kinds    []string `mapstructure:"kinds" yaml:"kinds"`
	KindsAll []string `mapstructure:"kinds_all" yaml:"kinds_all"`
}

// Config is the message_filters section.
type Config struct {
	DefaultAction string       `mapstructure:"default_action" yaml:"default_action"`
	Rules         []RuleConfig `mapstructure:"rules" yaml:"rules"`
}

type rule struct {
	name     string
	action   Action
	guild    *regexp.Regexp
	channel  *regexp.Regexp
	user     *regexp.Regexp
	username *regexp.Regexp
	content  *regexp.Regexp
	kinds    []string
	kindsAll []string
}

// Filter is a compiled, immutable rule set. A nil *Filter allows everything.
type Filter struct {
	defaultAction Action
	rules         []rule
}

// Subject is what a rule is matched against.
type Subject struct {
	GuildID   string
	ChannelID string
	UserID    string
	Username  string
	Content   string
	Kinds     []string
}

// SubjectOf builds a Subject from a canonical message.
func SubjectOf(m *types.DiscordMessage) Subject {
	if m == nil {
		return Subject{}
	}
	return Subject{GuildID: m.GuildID, ChannelID: m.ChannelID, UserID: m.AuthorID, Username: m.Username, Content: m.FullText(), Kinds: m.Kinds}
}

// Compile validates and compiles cfg. An empty config yields a nil Filter.
func Compile(cfg Config) (*Filter, error) {
	def := Action(strings.TrimSpace(cfg.DefaultAction))
	if def == "" {
		def = ActionAllow
	}
	if def != ActionAllow && def != ActionIgnore {
		return nil, fmt.Errorf("default_action must be allow or ignore, got %q", cfg.DefaultAction)
	}
	if len(cfg.Rules) == 0 && def == ActionAllow {
		return nil, nil
	}

	f := &Filter{defaultAction: def}
	var errs []string
	for i, rc := range cfg.Rules {
		label := rc.Name
		if label == "" {
			label = fmt.Sprintf("rules[%d]", i)
		}
		r := rule{name: label, action: Action(strings.TrimSpace(rc.Action))}
		if r.action != ActionAllow && r.action != ActionIgnore {
			errs = append(errs, fmt.Sprintf("%s: action must be allow or ignore, got %q", label, rc.Action))
		}
		compile := func(field, pattern string) *regexp.Regexp {
			if pattern == "" {
				return nil
			}
			re, err := regexp.Compile(pattern)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: invalid %s regex %q: %v", label, field, pattern, err))
				return nil
			}
			return re
		}
		r.guild = compile("guild", rc.Guild)
		r.channel = compile("channel", rc.Channel)
		r.user = compile("user", rc.User)
		r.username = compile("username", rc.Username)
		r.content = compile("content", rc.Content)
		for _, k := range append(append([]string{}, rc.Kinds...), rc.KindsAll...) {
			if !slices.Contains(types.AllKinds, k) {
				errs = append(errs, fmt.Sprintf("%s: unknown kind %q (valid: %s)", label, k, strings.Join(types.AllKinds, ", ")))
			}
		}
		r.kinds, r.kindsAll = rc.Kinds, rc.KindsAll
		if r.guild == nil && r.channel == nil && r.user == nil && r.username == nil && r.content == nil && len(r.kinds) == 0 && len(r.kindsAll) == 0 {
			errs = append(errs, fmt.Sprintf("%s: rule matches every message; add at least one condition (use default_action instead)", label))
		}
		f.rules = append(f.rules, r)
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return f, nil
}

func matchOrEmpty(re *regexp.Regexp, v string) bool {
	if re == nil {
		return true
	}
	return re.MatchString(v)
}

func (r *rule) matches(s Subject) bool {
	if !matchOrEmpty(r.guild, s.GuildID) || !matchOrEmpty(r.channel, s.ChannelID) ||
		!matchOrEmpty(r.user, s.UserID) || !matchOrEmpty(r.username, s.Username) ||
		!matchOrEmpty(r.content, s.Content) {
		return false
	}
	if len(r.kinds) > 0 && !slices.ContainsFunc(r.kinds, func(k string) bool { return slices.Contains(s.Kinds, k) }) {
		return false
	}
	for _, k := range r.kindsAll {
		if !slices.Contains(s.Kinds, k) {
			return false
		}
	}
	return true
}

// Decide returns the action for s and the name of the rule that decided it
// ("default" when no rule matched).
func (f *Filter) Decide(s Subject) (Action, string) {
	if f == nil {
		return ActionAllow, "default"
	}
	for i := range f.rules {
		if f.rules[i].matches(s) {
			return f.rules[i].action, f.rules[i].name
		}
	}
	return f.defaultAction, "default"
}

// Ignored is a convenience for Decide(s) == ignore.
func (f *Filter) Ignored(s Subject) bool {
	a, _ := f.Decide(s)
	return a == ActionIgnore
}

// Messages returns msgs without the ignored ones. The input is not modified.
func (f *Filter) Messages(msgs []*types.DiscordMessage) []*types.DiscordMessage {
	if f == nil {
		return msgs
	}
	out := make([]*types.DiscordMessage, 0, len(msgs))
	for _, m := range msgs {
		if m != nil && f.Ignored(SubjectOf(m)) {
			continue
		}
		out = append(out, m)
	}
	return out
}
