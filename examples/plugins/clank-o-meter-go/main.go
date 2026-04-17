package main

import (
	"crypto/md5"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"ezyapper/internal/plugin"
)

type ClankOMeterPlugin struct{}

func (p *ClankOMeterPlugin) Info() (plugin.PluginInfo, error) {
	return plugin.PluginInfo{
		Name:        "clank-o-meter",
		Version:     "0.0.0",
		Author:      "EZyapper",
		Description: "Detecting clanker levels / gooner level / wanker level by given Discord user ID.",
		Priority:    10,
	}, nil
}

func (p *ClankOMeterPlugin) OnMessage(msg plugin.DiscordMessage) (bool, error) {
	return true, nil
}

func (p *ClankOMeterPlugin) OnResponse(msg plugin.DiscordMessage, response string) error {
	return nil
}

func (p *ClankOMeterPlugin) Shutdown() error {
	return nil
}

func (p *ClankOMeterPlugin) ListTools() ([]plugin.ToolSpec, error) {
	return []plugin.ToolSpec{
		{
			Name:        "get_clank_o_meter",
			Description: "Return deterministic score (0-100) for a Discord user ID",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"user_id": map[string]interface{}{
						"type":        "string",
						"description": "Discord numeric user ID",
					},
				},
				"required": []string{"user_id"},
			},
		},
	}, nil
}

func (p *ClankOMeterPlugin) ExecuteTool(name string, args map[string]interface{}) (string, error) {
	if name != "get_clank_o_meter" {
		return "", fmt.Errorf("unknown tool: %s", name)
	}

	rawID, ok := args["user_id"]
	if !ok {
		return "", fmt.Errorf("missing required argument: user_id")
	}

	userID := strings.TrimSpace(fmt.Sprint(rawID))
	if userID == "" {
		return "", fmt.Errorf("user_id cannot be empty")
	}

	if _, err := strconv.ParseUint(userID, 10, 64); err != nil {
		return "", fmt.Errorf("user_id must be a numeric Discord user ID")
	}

	digest := md5.Sum([]byte(userID))
	value := binary.BigEndian.Uint64(digest[:8])
	score := int(value % 101)

	result := map[string]interface{}{
		"user_id":       userID,
		"clank_o_meter": score,
		"algorithm":     "MD5",
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	return string(data), nil
}

func main() {
	p := &ClankOMeterPlugin{}
	if err := plugin.Serve(p); err != nil {
		fmt.Fprintf(os.Stderr, "[CLANK-O-METER] Error: %v\n", err)
		os.Exit(1)
	}
}
