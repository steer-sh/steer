package codex

import "encoding/json"

// usageRow is a rollout line carrying a token count, in either shape Codex has
// written: a top-level token_usage_record, or an event_msg whose payload is a
// token_count.
type usageRow struct {
	Type    string `json:"type"`
	Payload struct {
		Type  string `json:"type"`
		Usage *struct {
			InputTokens int `json:"input_tokens"`
		} `json:"usage"`
		Info *struct {
			// LastTokenUsage is one turn's usage; the sibling total_token_usage
			// accumulates over the session and is no context depth.
			LastTokenUsage *struct {
				InputTokens int `json:"input_tokens"`
			} `json:"last_token_usage"`
		} `json:"info"`
	} `json:"payload"`
}

// contextTokens reads the newest context depth a rollout records. Codex's
// input_tokens already counts its cached half, so the depth is that one field
// and never a sum over the cache terms. A rollout carries one shape or the
// other, so the fallback is read only when the primary yields nothing.
func contextTokens(lines []string) (int, bool) {
	primary, fallback := 0, 0
	havePrimary, haveFallback := false, false
	for _, line := range lines {
		var row usageRow
		if json.Unmarshal([]byte(line), &row) != nil {
			continue
		}
		switch {
		case row.Type == "token_usage_record" && row.Payload.Usage != nil:
			primary, havePrimary = row.Payload.Usage.InputTokens, true
		case row.Type == "event_msg" && row.Payload.Type == "token_count" &&
			row.Payload.Info != nil && row.Payload.Info.LastTokenUsage != nil:
			fallback, haveFallback = row.Payload.Info.LastTokenUsage.InputTokens, true
		}
	}
	if havePrimary {
		return primary, true
	}
	return fallback, haveFallback
}
