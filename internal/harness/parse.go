package harness

import (
	"encoding/json"
	"strings"
)

type piContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type piMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	Usage   *struct {
		TotalTokens int `json:"totalTokens"`
		Cost        struct {
			Total float64 `json:"total"`
		} `json:"cost"`
	} `json:"usage"`
}

type piEvent struct {
	Type     string      `json:"type"`
	Messages []piMessage `json:"messages"`
}

func parsePIJSON(stdout string) (string, Usage, bool) {
	var last piEvent
	found := false
	for _, line := range strings.Split(stdout, "\n") {
		var ev piEvent
		if json.Unmarshal([]byte(line), &ev) != nil || ev.Type != "agent_end" {
			continue
		}
		last, found = ev, true
	}
	if !found {
		return "", Usage{}, false
	}
	reply := ""
	u := Usage{Known: true, HasCost: true}
	for _, m := range last.Messages {
		if m.Role != "assistant" {
			continue
		}
		var parts []piContent
		if json.Unmarshal(m.Content, &parts) != nil {
			continue
		}
		var b strings.Builder
		for _, part := range parts {
			if part.Type == "text" {
				b.WriteString(part.Text)
			}
		}
		reply = b.String()
		if m.Usage != nil {
			u.Tokens += m.Usage.TotalTokens
			u.Cost += m.Usage.Cost.Total
		}
	}
	return reply, u, true
}

type claudeResult struct {
	Type         string  `json:"type"`
	Result       string  `json:"result"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	Usage        struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	} `json:"usage"`
}

func parseClaudeJSON(stdout string) (string, Usage, bool) {
	var out claudeResult
	if err := json.Unmarshal([]byte(stdout), &out); err != nil || out.Type != "result" {
		return "", Usage{}, false
	}
	u := Usage{
		Tokens:  out.Usage.InputTokens + out.Usage.OutputTokens + out.Usage.CacheReadInputTokens + out.Usage.CacheCreationInputTokens,
		Cost:    out.TotalCostUSD,
		Known:   true,
		HasCost: true,
	}
	return out.Result, u, true
}

type codexEvent struct {
	Type string `json:"type"`
	Item *struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"item"`
	Usage *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func parseCodexJSON(stdout string) (string, Usage, bool) {
	reply := ""
	u := Usage{Known: true}
	decoded := false
	for _, line := range strings.Split(stdout, "\n") {
		var ev codexEvent
		if json.Unmarshal([]byte(line), &ev) != nil || ev.Type == "" {
			continue
		}
		decoded = true
		if ev.Type == "item.completed" && ev.Item != nil && ev.Item.Type == "agent_message" {
			reply = ev.Item.Text
		}
		if ev.Type == "turn.completed" && ev.Usage != nil {
			u.Tokens += ev.Usage.InputTokens + ev.Usage.OutputTokens
		}
	}
	if !decoded {
		return "", Usage{}, false
	}
	return reply, u, true
}

type opencodeEvent struct {
	Type string `json:"type"`
	Part struct {
		MessageID string  `json:"messageID"`
		Text      string  `json:"text"`
		Cost      float64 `json:"cost"`
		Tokens    *struct {
			Total int `json:"total"`
		} `json:"tokens"`
	} `json:"part"`
}

func parseOpencodeJSON(stdout string) (string, Usage, bool) {
	var texts []string
	lastMessage := ""
	u := Usage{Known: true, HasCost: true}
	decoded := false
	for _, line := range strings.Split(stdout, "\n") {
		var ev opencodeEvent
		if json.Unmarshal([]byte(line), &ev) != nil || ev.Type == "" {
			continue
		}
		decoded = true
		switch ev.Type {
		case "text":
			if ev.Part.MessageID != lastMessage {
				texts, lastMessage = nil, ev.Part.MessageID
			}
			texts = append(texts, ev.Part.Text)
		case "step_finish":
			if ev.Part.Tokens != nil {
				u.Tokens += ev.Part.Tokens.Total
			}
			u.Cost += ev.Part.Cost
		}
	}
	if !decoded {
		return "", Usage{}, false
	}
	return strings.Join(texts, ""), u, true
}

func parseText(stdout string) (string, Usage, bool) {
	return stdout, Usage{}, true
}

var parsers = map[string]func(string) (string, Usage, bool){
	"pi-json":       parsePIJSON,
	"claude-json":   parseClaudeJSON,
	"codex-json":    parseCodexJSON,
	"opencode-json": parseOpencodeJSON,
	"text":          parseText,
}

// ParseOutput extracts the final reply and token usage. Output the parser cannot decode is returned as it is.
func ParseOutput(parser, stdout string) (string, Usage) {
	parse := parsers[parser]
	if parse == nil {
		return strings.TrimSpace(stdout), Usage{}
	}
	reply, u, ok := parse(stdout)
	if !ok {
		return strings.TrimSpace(stdout), Usage{}
	}
	return strings.TrimSpace(reply), u
}
