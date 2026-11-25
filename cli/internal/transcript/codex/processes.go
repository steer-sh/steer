package codex

import (
	"encoding/json"
	"io"
	"strconv"
	"strings"
)

type shellCall struct {
	command   string
	sessionID int
	polling   bool
	ambiguous bool
	codeMode  bool
}

// shellOperation recovers one shell operation; a batch keeps no process origin.
func shellOperation(p payload) (shellCall, bool) {
	switch p.Name {
	case "exec_command":
		return shellCall{command: argField(p.Args)}, true
	case "write_stdin":
		var args struct {
			SessionID *int `json:"session_id"`
		}
		if json.Unmarshal([]byte(p.Args), &args) == nil && args.SessionID != nil {
			return shellCall{sessionID: *args.SessionID, polling: true}, true
		}
		return shellCall{}, true
	case "exec":
		tokens := jsTokens(p.Input)
		var calls []shellCall
		polls := false
		for i := 0; i+5 < len(tokens); i++ {
			if tokens[i].literal || tokens[i].value != "tools" || tokens[i+1].value != "." ||
				(tokens[i+2].value != "exec_command" && tokens[i+2].value != "write_stdin") ||
				tokens[i+3].value != "(" || tokens[i+4].value != "{" {
				continue
			}
			call := shellCall{}
			if tokens[i+2].value == "exec_command" {
				call.command = execCommands(p.Input)
			} else {
				polls = true
				depth := 1
				for j := i + 5; j+3 < len(tokens) && depth > 0; j++ {
					if !tokens[j].literal {
						switch tokens[j].value {
						case "{", "[", "(":
							depth++
						case "}", "]", ")":
							depth--
						}
					}
					if depth == 1 && tokens[j].value == "session_id" && tokens[j+1].value == ":" &&
						!tokens[j+2].literal && (tokens[j+3].value == "," || tokens[j+3].value == "}") {
						if id, err := strconv.Atoi(tokens[j+2].value); err == nil {
							call.sessionID, call.polling = id, true
						}
					}
				}
			}
			calls = append(calls, call)
		}
		if len(calls) == 1 {
			return calls[0], true
		}
		return shellCall{ambiguous: polls}, len(calls) > 0
	}
	return shellCall{}, false
}

type processState struct {
	sessionID *int
	complete  bool
}

// processStates reads process metadata the harness returned, never stdin text.
func processStates(text string, codeMode bool) []processState {
	if codeMode {
		body := text
		if _, rest, ok := strings.Cut(text, "\nOutput:\n"); ok {
			body = rest
		}
		decoder := json.NewDecoder(strings.NewReader(body))
		var states []processState
		for {
			var raw json.RawMessage
			if err := decoder.Decode(&raw); err != nil {
				if err == io.EOF && len(states) > 0 {
					return states
				}
				break
			}
			if parsed, ok := structuredProcessStates(raw); ok {
				states = append(states, parsed...)
			} else {
				break
			}
		}
	}
	const marker = "Process running with session ID "
	header := text
	for _, boundary := range []string{"\nOutput:\n", "\nFinal output:\n"} {
		if before, _, ok := strings.Cut(header, boundary); ok {
			header = before
		}
	}
	if i := lineIndex(header, marker); i >= 0 {
		line, _, _ := strings.Cut(header[i+len(marker):], "\n")
		if id, err := strconv.Atoi(strings.TrimSpace(line)); err == nil {
			return []processState{{sessionID: &id}}
		}
	}
	if lineIndex(header, exitMarker) >= 0 {
		return []processState{{complete: true}}
	}
	return nil
}

func structuredProcessStates(raw json.RawMessage) ([]processState, bool) {
	var result struct {
		Output    *string `json:"output"`
		SessionID *int    `json:"session_id"`
		ExitCode  *int    `json:"exit_code"`
	}
	if json.Unmarshal(raw, &result) == nil && result.Output != nil && (result.SessionID != nil || result.ExitCode != nil) {
		return []processState{{sessionID: result.SessionID, complete: result.ExitCode != nil}}, true
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil || len(list) == 0 {
		return nil, false
	}
	var states []processState
	for _, item := range list {
		var settled struct {
			Status string          `json:"status"`
			Value  json.RawMessage `json:"value"`
		}
		if json.Unmarshal(item, &settled) == nil && settled.Status == "fulfilled" {
			item = settled.Value
		}
		parsed, ok := structuredProcessStates(item)
		if !ok {
			return nil, false
		}
		states = append(states, parsed...)
	}
	return states, true
}
