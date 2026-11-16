package codex

import (
	"encoding/json"
	"io"
	"strconv"
	"strings"
)

type jsToken struct {
	value   string
	literal bool
}

// execCommands reads static shell arguments at actual tools.exec_command calls.
// Strings, comments and interpolated templates cannot supply a command.
func execCommands(source string) string {
	tokens := jsTokens(source)
	var commands []string
	for i := 0; i+5 < len(tokens); i++ {
		if tokens[i].literal || tokens[i].value != "tools" || tokens[i+1].value != "." ||
			tokens[i+2].value != "exec_command" || tokens[i+3].value != "(" || tokens[i+4].value != "{" {
			continue
		}
		depth := 1
		for j := i + 5; j < len(tokens) && depth > 0; j++ {
			token := tokens[j]
			if !token.literal {
				switch token.value {
				case "{", "[", "(":
					depth++
				case "}", "]", ")":
					depth--
				}
			}
			if depth == 1 && (token.value == "cmd" || token.value == "command") && j+3 < len(tokens) &&
				tokens[j+1].value == ":" && tokens[j+2].literal &&
				(tokens[j+3].value == "," || tokens[j+3].value == "}") {
				commands = append(commands, tokens[j+2].value)
			}
		}
	}
	return strings.Join(commands, "\n")
}

// jsTokens retains literal strings and identifiers without executing JavaScript.
func jsTokens(source string) []jsToken {
	var tokens []jsToken
	for i := 0; i < len(source); {
		ch := source[i]
		if ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' {
			i++
			continue
		}
		if strings.HasPrefix(source[i:], "//") {
			if end := strings.IndexByte(source[i:], '\n'); end >= 0 {
				i += end + 1
			} else {
				break
			}
			continue
		}
		if strings.HasPrefix(source[i:], "/*") {
			if end := strings.Index(source[i+2:], "*/"); end >= 0 {
				i += end + 4
			} else {
				break
			}
			continue
		}
		if ch == '/' && regexPosition(tokens) {
			if end := jsRegexEnd(source, i); end > i {
				tokens = append(tokens, jsToken{literal: true})
				i = end
				continue
			}
		}
		if ch == '\'' || ch == '"' || ch == '`' {
			value, end, static := jsString(source, i)
			tokens = append(tokens, jsToken{value: value, literal: static})
			i = end
			continue
		}
		if jsIdentifier(ch) {
			end := i + 1
			for end < len(source) && jsIdentifier(source[end]) {
				end++
			}
			tokens = append(tokens, jsToken{value: source[i:end]})
			i = end
			continue
		}
		tokens = append(tokens, jsToken{value: string(ch)})
		i++
	}
	return tokens
}

func regexPosition(tokens []jsToken) bool {
	if len(tokens) == 0 {
		return true
	}
	switch tokens[len(tokens)-1].value {
	case "(", "=", ",", ":", "[", "return":
		return true
	}
	return false
}

func jsRegexEnd(source string, start int) int {
	bracket := false
	for i := start + 1; i < len(source); i++ {
		switch source[i] {
		case '\n', '\r':
			return start
		case '\\':
			i++
		case '[':
			bracket = true
		case ']':
			bracket = false
		case '/':
			if !bracket {
				i++
				for i < len(source) && jsIdentifier(source[i]) {
					i++
				}
				return i
			}
		}
	}
	return start
}

func jsIdentifier(ch byte) bool {
	return ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '$'
}

func jsString(source string, start int) (string, int, bool) {
	quote := source[start]
	var value strings.Builder
	static := true
	for i := start + 1; i < len(source); {
		if source[i] == quote {
			return value.String(), i + 1, static
		}
		if quote == '`' && strings.HasPrefix(source[i:], "${") {
			static = false
		}
		if source[i] != '\\' {
			value.WriteByte(source[i])
			i++
			continue
		}
		if i+1 >= len(source) {
			break
		}
		if source[i+1] == '\n' {
			i += 2
			continue
		}
		if source[i+1] == quote || source[i+1] == '/' {
			value.WriteByte(source[i+1])
			i += 2
			continue
		}
		decoded, _, tail, err := strconv.UnquoteChar(source[i:], quote)
		if err != nil {
			static = false
			i += 2
			continue
		}
		value.WriteRune(decoded)
		i = len(source) - len(tail)
	}
	return "", len(source), false
}

// execOutput unwraps the objects text() prints when handed an exec_command result.
// A plain output string is preserved, including source text containing JSON.
func execOutput(text string) (string, bool) {
	body := text
	if _, rest, ok := strings.Cut(text, "\nOutput:\n"); ok {
		body = rest
	}
	decoder := json.NewDecoder(strings.NewReader(body))
	var parts []string
	failed := false
	for {
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			if err != io.EOF {
				return text, false
			}
			break
		}
		part, bad, ok := execResult(raw)
		if !ok {
			return text, false
		}
		parts = append(parts, part)
		failed = failed || bad
	}
	if len(parts) == 0 {
		return text, false
	}
	return strings.Join(parts, "\n"), failed
}

func execResult(raw json.RawMessage) (string, bool, bool) {
	var result struct {
		Output    *string  `json:"output"`
		ExitCode  *int     `json:"exit_code"`
		SessionID *int     `json:"session_id"`
		WallTime  *float64 `json:"wall_time_seconds"`
	}
	if json.Unmarshal(raw, &result) == nil && result.Output != nil &&
		(result.ExitCode != nil || result.SessionID != nil || result.WallTime != nil) {
		return *result.Output, result.ExitCode != nil && *result.ExitCode != 0, true
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil || len(list) == 0 {
		return "", false, false
	}
	var parts []string
	failed := false
	for _, item := range list {
		// Promise.allSettled wraps each tool result in a fulfilled value.
		var settled struct {
			Status string          `json:"status"`
			Value  json.RawMessage `json:"value"`
		}
		if json.Unmarshal(item, &settled) == nil && settled.Status == "fulfilled" {
			item = settled.Value
		}
		part, bad, ok := execResult(item)
		if !ok {
			return "", false, false
		}
		parts = append(parts, part)
		failed = failed || bad
	}
	return strings.Join(parts, "\n"), failed, true
}
