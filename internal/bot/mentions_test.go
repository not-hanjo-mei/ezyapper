package bot

import "testing"

func TestReplaceMentions_User(t *testing.T) {
	content := "<@123> hello"
	users := []UserMapping{{ID: "123", Username: "Hanjo"}}
	var channels []ChannelMapping

	result := ReplaceMentions(content, users, channels)

	expected := "@Hanjo hello"
	if result != expected {
		t.Errorf("ReplaceMentions(%q, %v, %v) = %q, want %q", content, users, channels, result, expected)
	}
}

func TestReplaceMentions_Nickname(t *testing.T) {
	content := "<@!456> hi"
	users := []UserMapping{{ID: "456", Username: "Mei"}}
	var channels []ChannelMapping

	result := ReplaceMentions(content, users, channels)

	expected := "@Mei hi"
	if result != expected {
		t.Errorf("ReplaceMentions(%q, %v, %v) = %q, want %q", content, users, channels, result, expected)
	}
}

func TestReplaceMentions_Channel(t *testing.T) {
	content := "check <#789>"
	channels := []ChannelMapping{{ID: "789", Name: "general"}}
	var users []UserMapping

	result := ReplaceMentions(content, users, channels)

	expected := "check #general"
	if result != expected {
		t.Errorf("ReplaceMentions(%q, %v, %v) = %q, want %q", content, users, channels, result, expected)
	}
}

func TestReplaceMentions_NoMatch(t *testing.T) {
	content := "<@999>"
	users := []UserMapping{{ID: "123", Username: "Someone"}}
	var channels []ChannelMapping

	result := ReplaceMentions(content, users, channels)

	// No mapping for ID 999, so unchanged
	if result != content {
		t.Errorf("ReplaceMentions(%q, %v, %v) = %q, want %q (unchanged)", content, users, channels, result, content)
	}
}

func TestReplaceMentions_NeverEveryone(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"raw everyone", "@everyone listen up"},
		{"raw here", "@here check this"},
		{"mixed", "@everyone and @here"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var users []UserMapping
			var channels []ChannelMapping

			result := ReplaceMentions(tt.content, users, channels)

			if result != tt.content {
				t.Errorf("ReplaceMentions(%q) = %q, want %q (unchanged)", tt.content, result, tt.content)
			}
		})
	}
}
