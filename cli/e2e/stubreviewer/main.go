// Command stubreviewer stands in for a reviewer CLI in the end-to-end suite. Linked
// on PATH as agy, claude or codex, it takes that name as its provider, accepts only
// the argv the gate's adapter for that provider builds, and answers in that
// provider's reply format with the mode its script names for the call.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/steer-sh/steer-cli/e2e/stubreviewer/stubcall"
)

const (
	envScript = "STEER_STUB_SCRIPT" // one mode per call, the last one repeating; absent is clean
	envState  = "STEER_STUB_STATE"  // directory of per-call claim files
	envLog    = "STEER_STUB_LOG"    // JSON Lines file of Call records
	envSleep  = "STEER_STUB_SLEEP"  // seconds the sleep mode waits; default 30
)

// Call is the record the stub appends to the log.
type Call = stubcall.Call

var diffPathRe = regexp.MustCompile(`(?m)^\+\+\+ b/(\S+)`)

func main() {
	provider := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
	call := Call{Provider: provider, Argv: os.Args[1:]}
	stdin, _ := io.ReadAll(os.Stdin)
	call.Stdin = string(stdin)
	if err := parseArgv(&call); err != nil {
		fmt.Fprintf(os.Stderr, "stub %s: %v\n", provider, err)
		os.Exit(64)
	}
	call.Cwd, _ = os.Getwd()
	call.Index = claim()
	call.Mode = mode(call.Index)
	record(call)
	os.Exit(reply(call))
}

// parseArgv accepts the argv shape of call.Provider's adapter and fills the prompt,
// model and effort it carries. Any other flag is an error.
func parseArgv(call *Call) error {
	args := call.Argv
	value := func(i int) (string, error) {
		if i+1 >= len(args) {
			return "", fmt.Errorf("flag %s needs a value", args[i])
		}
		return args[i+1], nil
	}
	switch call.Provider {
	case "agy":
		format := ""
		for i := 0; i < len(args); i++ {
			v, err := value(i)
			if err != nil {
				return err
			}
			switch args[i] {
			case "--model":
				call.Model = v
			case "--effort":
				call.Effort = v
			case "--print-timeout":
				if _, err := time.ParseDuration(v); err != nil {
					return fmt.Errorf("--print-timeout %q: %v", v, err)
				}
			case "--output-format":
				format = v
			case "-p":
				call.Prompt = v
			default:
				return fmt.Errorf("unknown flag %q", args[i])
			}
			i++
		}
		if format != "json" || call.Prompt == "" {
			return fmt.Errorf("want --output-format json and -p <prompt>, got %q", args)
		}
	case "claude":
		seen := map[string]bool{}
		for i := 0; i < len(args); i++ {
			switch args[i] {
			case "-p", "--no-session-persistence", "--safe-mode":
				seen[args[i]] = true
			case "--output-format", "--tools", "--model", "--effort":
				v, err := value(i)
				if err != nil {
					return err
				}
				switch args[i] {
				case "--output-format":
					seen["json"] = v == "json"
				case "--tools":
					seen["read-only tools"] = v == "Read,Grep,Glob"
				case "--model":
					call.Model = v
				case "--effort":
					call.Effort = v
				}
				i++
			default:
				return fmt.Errorf("unknown flag %q", args[i])
			}
		}
		if !seen["-p"] || !seen["json"] || !seen["--no-session-persistence"] ||
			!seen["--safe-mode"] || !seen["read-only tools"] {
			return fmt.Errorf("want -p --output-format json --no-session-persistence --safe-mode --tools Read,Grep,Glob, got %q", args)
		}
		call.Prompt = call.Stdin
	case "codex":
		if len(args) == 0 || args[0] != "exec" || args[len(args)-1] != "-" {
			return fmt.Errorf("want exec ... -, got %q", args)
		}
		seen := map[string]bool{}
		for i := 1; i < len(args)-1; i++ {
			switch args[i] {
			case "--skip-git-repo-check", "--ephemeral":
				seen[args[i]] = true
			case "--sandbox", "--disable", "--model", "-c":
				v, err := value(i)
				if err != nil {
					return err
				}
				if args[i] == "--sandbox" {
					seen["read-only"] = v == "read-only"
				} else if args[i] == "--disable" {
					if v != "hooks" && v != "apps" {
						return fmt.Errorf("unknown feature to disable %q", v)
					}
					seen[v+" off"] = true
				} else if args[i] == "--model" {
					call.Model = v
				} else if effort, ok := strings.CutPrefix(v, "model_reasoning_effort="); ok {
					call.Effort = effort
				} else {
					return fmt.Errorf("unknown config override %q", v)
				}
				i++
			default:
				return fmt.Errorf("unknown flag %q", args[i])
			}
		}
		if !seen["--skip-git-repo-check"] || !seen["--ephemeral"] ||
			!seen["read-only"] || !seen["hooks off"] || !seen["apps off"] {
			return fmt.Errorf("want --skip-git-repo-check --ephemeral --sandbox read-only --disable hooks --disable apps, got %q", args)
		}
		call.Prompt = call.Stdin
	default:
		return fmt.Errorf("unknown provider name %q", call.Provider)
	}
	return nil
}

// claim takes the lowest call index that no concurrent panel run holds.
func claim() int {
	dir := os.Getenv(envState)
	if dir == "" {
		return 0
	}
	for i := 0; ; i++ {
		f, err := os.OpenFile(filepath.Join(dir, "call-"+strconv.Itoa(i)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			f.Close()
			return i
		}
		if !os.IsExist(err) {
			return i
		}
	}
}

func mode(index int) string {
	data, err := os.ReadFile(os.Getenv(envScript))
	if err != nil {
		return "clean"
	}
	var modes []string
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			modes = append(modes, line)
		}
	}
	if len(modes) == 0 {
		return "clean"
	}
	return modes[min(index, len(modes)-1)]
}

func record(call Call) {
	path := os.Getenv(envLog)
	if path == "" {
		return
	}
	line, _ := json.Marshal(call)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(line, '\n'))
}

func finding(n int, file string) string {
	return fmt.Sprintf("[%d] SEVERITY: BLOCKING\n    FILE: %s\n    CLAIM: stub finding %d.\n    FAILS WHEN: the stub says so.\n", n, file, n)
}

// reply writes the answer for call.Mode and returns the exit code.
func reply(call Call) int {
	file := "stats.py"
	if m := diffPathRe.FindStringSubmatch(call.Prompt); m != nil {
		file = m[1]
	}
	var text string
	switch call.Mode {
	case "clean":
		text = "NO FINDINGS"
	case "finding":
		text = finding(1, file)
	case "findings":
		text = finding(1, file) + finding(2, file) + finding(3, file)
	case "malformed":
		if call.Provider == "codex" {
			fmt.Print("I read the change and have some thoughts but no structured answer.")
		} else {
			fmt.Print("{not json")
		}
		return 0
	case "empty":
		return 0
	case "exit":
		fmt.Fprintln(os.Stderr, "stub: the reviewer crashed")
		return 3
	case "auth":
		fmt.Fprintln(os.Stderr, "stub: authentication required, not signed in")
		return 1
	case "unknown-model":
		fmt.Fprintf(os.Stderr, "stub: unknown model %q\n", call.Model)
		return 1
	case "sleep":
		seconds, err := strconv.Atoi(os.Getenv(envSleep))
		if err != nil {
			seconds = 30
		}
		time.Sleep(time.Duration(seconds) * time.Second)
		text = "NO FINDINGS"
	default:
		fmt.Fprintf(os.Stderr, "stub: unknown mode %q\n", call.Mode)
		return 65
	}
	switch call.Provider {
	case "agy":
		out, _ := json.Marshal(map[string]any{"response": text, "status": "SUCCESS", "error": nil})
		fmt.Println(string(out))
	case "claude":
		out, _ := json.Marshal(map[string]any{"result": text, "is_error": false, "subtype": "success"})
		fmt.Println(string(out))
	default:
		fmt.Println(text)
	}
	return 0
}
