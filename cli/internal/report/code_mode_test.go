package report_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/steer-sh/steer-cli/internal/report"
	"github.com/steer-sh/steer-cli/internal/transcript"
	"github.com/steer-sh/steer-cli/internal/transcript/codex"
)

// TestCodexCodeModeFailuresReachTheClassifier covers literal arguments and printed results.
func TestCodexCodeModeFailuresReachTheClassifier(t *testing.T) {
	failure := "--- FAIL: TestBroken (0.00s)\nFAIL"
	for _, tc := range []struct {
		name, input, output string
		want                int
	}{
		{"json control", `text((await tools.exec_command({"cmd":"go test ./..."})).output)`, failure, 1},
		{"unquoted keys", `text((await tools.exec_command({cmd:"go test ./..."})).output)`, failure, 1},
		{"single quotes", `text((await tools.exec_command({cmd:'go test ./...'})).output)`, failure, 1},
		{"literal template", "text((await tools.exec_command({cmd:`go test ./...`})).output)", failure, 1},
		{"printed object", `text(await tools.exec_command({cmd:"go test ./..."}))`, `{"exit_code":1,"output":"--- FAIL: TestBroken (0.00s)\nFAIL"}`, 1},
		{"framed object", `text(await tools.exec_command({cmd:"go test ./..."}))`, "Script completed\nWall time 0.1 seconds\nOutput:\n" + `{"exit_code":1,"output":"--- FAIL: TestBroken (0.00s)\nFAIL"}`, 1},
		{"batched calls", `const results = await Promise.allSettled([tools.exec_command({cmd:"git status"}),tools.exec_command({cmd:"go test ./..."})]); text(results);`, `[{"status":"fulfilled","value":{"exit_code":0,"output":"clean"}},{"status":"fulfilled","value":{"exit_code":1,"output":"--- FAIL: TestBroken (0.00s)\nFAIL"}}]`, 1},
		{"unrelated object", `const options={max_tokens:1000}; text(await tools.exec_command({cmd:"go test ./..."}));`, failure, 1},
		{"dynamic command", `text(await tools.exec_command({cmd: command}))`, failure, 0},
		{"interpolated template", "text(await tools.exec_command({cmd:`go test ${path}`}))", failure, 0},
		{"computed command", `text(await tools.exec_command({cmd:"go test " + path}))`, failure, 0},
		{"printed source", `text('tools.exec_command({cmd:"go test ./..."})');`, failure, 0},
		{"regex source", `text(/tools.exec_command({cmd:"go test"})/);`, failure, 0},
		{"commented call", `/* tools.exec_command({cmd:"go test ./..."}) */ text("example");`, failure, 0},
		{"reader output", `text(await tools.exec_command({cmd:"cat fixtures/test-output.txt"}))`, `{"exit_code":0,"output":"--- FAIL: TestBroken (0.00s)\nFAIL"}`, 0},
		{"quoted source in runner", `text(await tools.exec_command({cmd:"go test ./..."}))`, `{"exit_code":0,"output":"source = \"--- FAIL: TestBroken\""}`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "rollout.jsonl")
			rows := []map[string]any{
				{"type": "session_meta", "payload": map[string]any{"id": "root", "cwd": "/repo"}},
				{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "exec", "call_id": "call", "input": tc.input}},
				{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call_output", "call_id": "call", "output": tc.output}},
			}
			var data []byte
			for _, row := range rows {
				b, err := json.Marshal(row)
				if err != nil {
					t.Fatal(err)
				}
				data = append(data, append(b, '\n')...)
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			classifier := report.NewClassifier()
			signals := 0
			_, err := codex.New().Stream(transcript.Session{Path: path}, transcript.Filter{}, func(e transcript.Event) error {
				if signal, ok := classifier.Classify(e); ok {
					if signal.Kind != "tool_failures" {
						t.Fatalf("unexpected signal %+v", signal)
					}
					signals++
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if signals != tc.want {
				t.Errorf("got %d failure signals; want %d", signals, tc.want)
			}
		})
	}
}
