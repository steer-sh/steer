package plugindist

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/mods"
)

// modHandlerRe reads the event name of each `on('<event>'` registration.
var modHandlerRe = regexp.MustCompile("\\bon\\(\\s*['\"`]([^'\"`]+)['\"`]")

// Steer reports a mod that handles a tool event as one that can skip its hooks, and
// where Claude Code's own guard loads a mod's classic handler does not run. The
// bundled mod draws state and must register neither.
func TestTheBundledModHandlesNoToolOrClassicEvent(t *testing.T) {
	root := repoRoot(t)
	var doc struct {
		Modules []string `json:"modules"`
	}
	raw, err := os.ReadFile(filepath.Join(root, "hooks", "hooks.json"))
	if err != nil {
		t.Fatalf("read hooks/hooks.json: %v", err)
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse hooks/hooks.json: %v", err)
	}
	if len(doc.Modules) != 1 {
		t.Fatalf("hooks/hooks.json modules = %v, want the one hooks module", doc.Modules)
	}
	module := filepath.Join(root, "hooks", filepath.FromSlash(doc.Modules[0]))
	if _, err := os.Stat(module); err != nil {
		t.Fatalf("the hooks module is missing: %v", err)
	}
	sources, err := filepath.Glob(filepath.Join(filepath.Dir(module), "*.ts*"))
	if err != nil {
		t.Fatal(err)
	}
	var events []string
	for _, source := range sources {
		if strings.Contains(filepath.Base(source), ".test.") {
			continue
		}
		text, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range modHandlerRe.FindAllStringSubmatch(string(text), -1) {
			events = append(events, m[1])
		}
	}
	if len(events) == 0 {
		t.Fatal("the hooks module registers no handler this test can read")
	}
	if tool := mods.ToolHandlers(events); len(tool) > 0 {
		t.Errorf("the bundled mod handles %v, which Steer reports as an exposure", tool)
	}
	for _, event := range events {
		if strings.HasPrefix(event, "classic.") {
			t.Errorf("the bundled mod handles %s", event)
		}
	}
}

// Codex refuses a hooks file with any key beyond description and hooks, and then
// loads none of the plugin's hooks. Its manifest names a copy of the Claude Code
// hooks file without the modules key, so both harnesses run the same hooks.
func TestCodexHooksAreTheClaudeHooksWithoutModules(t *testing.T) {
	root := repoRoot(t)
	manifest := readJSON(t, filepath.Join(root, ".codex-plugin", "plugin.json"))
	rel, ok := manifest["hooks"].(string)
	if !ok {
		t.Fatalf("the Codex manifest declares no hooks path: %v", manifest["hooks"])
	}
	codex := readJSON(t, filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(rel, "./"))))
	for key := range codex {
		if key != "description" && key != "hooks" {
			t.Errorf("the Codex hooks file %s holds %q, which Codex refuses", rel, key)
		}
	}
	claude := readJSON(t, filepath.Join(root, "hooks", "hooks.json"))
	delete(claude, "modules")
	if !reflect.DeepEqual(codex, claude) {
		t.Errorf("the Codex hooks file %s differs from hooks/hooks.json without modules:\n codex:  %s\n claude: %s",
			rel, mustJSON(t, codex), mustJSON(t, claude))
	}
}
