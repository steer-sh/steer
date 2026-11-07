package plugindist

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The repository ships four manifests describing one plugin: the two
// marketplaces and the two plugin manifests each harness reads at the plugin
// root. Codex copies a plugin root verbatim and Claude Code installs one
// directly from a git URL, so a field that disagrees between them installs one
// plugin under two identities. These tests compare whole documents rather than
// a modeled subset, because an unmodeled field is exactly the one that drifts.

// pluginManifests are the per-harness manifests at the plugin root. Both are
// read in preference to a synthesized marketplace entry, so both are pinned.
var pluginManifests = []string{
	filepath.Join(".codex-plugin", "plugin.json"),
	filepath.Join(".claude-plugin", "plugin.json"),
}

// marketplaceOnlyKeys are the keys a marketplace entry carries that the plugin
// manifest at the plugin root cannot: the root is its own source, and strictness
// is the marketplace's decision about the entry.
var marketplaceOnlyKeys = []string{"source", "strict"}

// manifestOnlyKeys are the components only the plugin manifest declares. Claude
// Code refuses to load a non-strict entry when both documents declare
// components, and refuses a hooks file path in any marketplace entry.
var manifestOnlyKeys = []string{"skills", "hooks"}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".claude-plugin", "marketplace.json")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repository root not found: no .claude-plugin/marketplace.json above the test")
		}
		dir = parent
	}
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return doc
}

func soleSteerPlugin(t *testing.T, path string) map[string]any {
	t.Helper()
	doc := readJSON(t, path)
	if doc["name"] != "steer-marketplace" {
		t.Fatalf("%s: marketplace name = %v, want steer-marketplace", path, doc["name"])
	}
	plugins, ok := doc["plugins"].([]any)
	if !ok || len(plugins) != 1 {
		t.Fatalf("%s: want exactly 1 plugin, got %v", path, doc["plugins"])
	}
	entry, ok := plugins[0].(map[string]any)
	if !ok {
		t.Fatalf("%s: plugin entry is not an object", path)
	}
	if entry["name"] != "steer" {
		t.Fatalf("%s: plugin name = %v, want steer", path, entry["name"])
	}
	return entry
}

func keys(doc map[string]any) []string {
	out := make([]string, 0, len(doc))
	for k := range doc {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestBothMarketplacesAreTheSameDocument(t *testing.T) {
	root := repoRoot(t)
	cc := readJSON(t, filepath.Join(root, ".claude-plugin", "marketplace.json"))
	codex := readJSON(t, filepath.Join(root, ".agents", "plugins", "marketplace.json"))
	if !reflect.DeepEqual(cc, codex) {
		t.Fatalf("the two marketplaces describe different plugins:\n claude: %s\n codex:  %s",
			mustJSON(t, cc), mustJSON(t, codex))
	}
}

func TestEveryPluginManifestMatchesItsMarketplaceEntry(t *testing.T) {
	root := repoRoot(t)
	for _, rel := range pluginManifests {
		entry := soleSteerPlugin(t, filepath.Join(root, ".agents", "plugins", "marketplace.json"))
		manifest := readJSON(t, filepath.Join(root, rel))

		for _, key := range marketplaceOnlyKeys {
			if _, ok := entry[key]; !ok {
				t.Errorf("marketplace entry is missing %q", key)
			}
			if value, ok := manifest[key]; ok {
				t.Errorf("%s carries %q = %v; that key is the marketplace's", rel, key, value)
			}
			delete(entry, key)
		}
		for _, key := range manifestOnlyKeys {
			if _, ok := manifest[key]; !ok {
				t.Errorf("%s is missing %q", rel, key)
			}
			if value, ok := entry[key]; ok {
				t.Errorf("marketplace entry carries %q = %v; that key is the plugin manifest's", key, value)
			}
			delete(manifest, key)
		}
		if !reflect.DeepEqual(entry, manifest) {
			t.Errorf("%s differs from its marketplace entry:\n keys(manifest): %v\n keys(entry):    %v\n manifest: %s\n entry:    %s",
				rel, keys(manifest), keys(entry), mustJSON(t, manifest), mustJSON(t, entry))
		}
	}
}

var (
	readmeMarketplaceAdd = regexp.MustCompile("marketplace add ([^\\s`'\"]+?)[.,;:)]*(?:[\\s`'\"]|$)")
	readmePluginInstall  = regexp.MustCompile("plugin (?:install|add) steer@([^\\s`'\"]+?)[.,;:)]*(?:[\\s`'\"]|$)")
)

// A user installs from the README's commands, so each one must name the
// repository and the marketplace the manifests declare; a slug that drifts from
// them installs nothing.
//
// pins existing behaviour
func TestReadmeInstallCommandsNameTheManifestRepository(t *testing.T) {
	root := repoRoot(t)
	doc := readJSON(t, filepath.Join(root, ".claude-plugin", "marketplace.json"))
	repository, _ := soleSteerPlugin(t, filepath.Join(root, ".claude-plugin", "marketplace.json"))["repository"].(string)
	slug, ok := githubSlug(repository)
	if !ok {
		t.Fatalf("marketplace repository %q is not a https://github.com/<owner>/<repo> URL", repository)
	}
	raw, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	readme := string(raw)

	adds := readmeMarketplaceAdd.FindAllStringSubmatch(readme, -1)
	installs := readmePluginInstall.FindAllStringSubmatch(readme, -1)
	if len(adds) == 0 || len(installs) == 0 {
		t.Fatalf("README carries %d marketplace add and %d plugin install command(s); want both", len(adds), len(installs))
	}
	for _, m := range adds {
		if got, _ := githubSlug(m[1]); got != slug {
			t.Errorf("README: %q names %s; the manifests declare %s", m[0], m[1], slug)
		}
	}
	for _, m := range installs {
		if m[1] != doc["name"] {
			t.Errorf("README: %q names marketplace %s; the manifests declare %v", m[0], m[1], doc["name"])
		}
	}
}

// githubSlug reduces a GitHub repository reference, as a slug or a URL, to
// <owner>/<repo>, and reports whether it names exactly one repository.
func githubSlug(ref string) (string, bool) {
	slug := strings.TrimSuffix(strings.TrimPrefix(ref, "https://github.com/"), ".git")
	return slug, strings.Count(slug, "/") == 1 && !strings.Contains(slug, ":")
}

// A strict entry defers to the plugin manifest, so Claude Code loads the
// components the manifest declares.
func TestTheMarketplaceEntryIsStrict(t *testing.T) {
	entry := soleSteerPlugin(t, filepath.Join(repoRoot(t), ".claude-plugin", "marketplace.json"))
	if entry["strict"] != true {
		t.Fatalf("marketplace entry strict = %v, want true", entry["strict"])
	}
}

func TestEveryDeclaredPluginPathExists(t *testing.T) {
	root := repoRoot(t)
	for _, rel := range pluginManifests {
		assertDeclaredPathsExist(t, root, readJSON(t, filepath.Join(root, rel)))
	}
}

func assertDeclaredPathsExist(t *testing.T, root string, manifest map[string]any) {
	t.Helper()

	hooks, ok := manifest["hooks"].(string)
	if !ok {
		t.Fatalf("plugin manifest declares no hooks path: %v", manifest["hooks"])
	}
	skills, ok := manifest["skills"].([]any)
	if !ok || len(skills) == 0 {
		t.Fatalf("plugin manifest declares no skills: %v", manifest["skills"])
	}

	paths := []string{hooks, "scripts/steer"}
	for _, skill := range skills {
		dir, ok := skill.(string)
		if !ok {
			t.Fatalf("skill entry is not a string: %v", skill)
		}
		paths = append(paths, filepath.Join(dir, "SKILL.md"))
	}
	for _, rel := range paths {
		rel = strings.TrimPrefix(rel, "./")
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Errorf("declared path %s: %v", rel, err)
		}
	}
}

func TestBundledHookResolvesEitherHarnessPluginRoot(t *testing.T) {
	root := repoRoot(t)
	var bundle struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	raw, err := os.ReadFile(filepath.Join(root, "hooks", "hooks.json"))
	if err != nil {
		t.Fatalf("read hooks/hooks.json: %v", err)
	}
	if err := json.Unmarshal(raw, &bundle); err != nil {
		t.Fatalf("parse hooks/hooks.json: %v", err)
	}

	entries, ok := bundle.Hooks["SessionStart"]
	if !ok || len(entries) == 0 || len(entries[0].Hooks) == 0 {
		t.Fatal("hooks/hooks.json declares no SessionStart command")
	}
	command := entries[0].Hooks[0].Command

	// Codex sets PLUGIN_ROOT and Claude Code sets CLAUDE_PLUGIN_ROOT, so one
	// bundled command reads the first and falls back to the second. The inner
	// name stays braced: Claude Code may substitute the placeholder into the
	// string instead of exporting it, and the braced form is the one every
	// version of that substitution has accepted.
	if !strings.Contains(command, "${PLUGIN_ROOT:-${CLAUDE_PLUGIN_ROOT}}") {
		t.Fatalf("SessionStart command does not resolve both plugin roots: %q", command)
	}
}

// Codex lists a hook in /hooks by its statusMessage and shows "Unnamed hook" for one
// without, so every bundled handler carries one.
func TestBundledHooksCarryAStatusMessage(t *testing.T) {
	root := repoRoot(t)
	var bundle struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command       string `json:"command"`
				StatusMessage string `json:"statusMessage"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	raw, err := os.ReadFile(filepath.Join(root, "hooks", "hooks.json"))
	if err != nil {
		t.Fatalf("read hooks/hooks.json: %v", err)
	}
	if err := json.Unmarshal(raw, &bundle); err != nil {
		t.Fatalf("parse hooks/hooks.json: %v", err)
	}
	for event, entries := range bundle.Hooks {
		for _, entry := range entries {
			for _, hook := range entry.Hooks {
				if strings.TrimSpace(hook.StatusMessage) == "" {
					t.Errorf("%s hook %q has no statusMessage", event, hook.Command)
				}
			}
		}
	}
}

var claudeRootExpansion = regexp.MustCompile(`\$\{?CLAUDE_PLUGIN_ROOT\b[^}"\s]*\}?`)

// Claude Code exports no plugin root to the Bash tool; it substitutes only the
// exact text ${CLAUDE_PLUGIN_ROOT}, and only into SKILL.md. A skill's shell
// expansion of that name is the bare placeholder, in its SKILL.md.
func TestSkillsExpandTheClaudePluginRootPlaceholder(t *testing.T) {
	found := 0
	for _, dir := range skillDirs(t) {
		err := filepath.WalkDir(dir, func(file string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || filepath.Ext(file) != ".md" {
				return err
			}
			raw, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			for _, m := range claudeRootExpansion.FindAllString(string(raw), -1) {
				found++
				if m != "${CLAUDE_PLUGIN_ROOT}" || filepath.Base(file) != "SKILL.md" {
					t.Errorf("%s expands %s, which Claude Code leaves unsubstituted", file, m)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	if found == 0 {
		t.Error("no skill resolves the bundled launcher through ${CLAUDE_PLUGIN_ROOT}")
	}
}

// skillDirs returns the absolute directory of every skill a plugin manifest lists.
func skillDirs(t *testing.T) []string {
	t.Helper()
	root := repoRoot(t)
	seen := map[string]bool{}
	var dirs []string
	for _, rel := range pluginManifests {
		skills, _ := readJSON(t, filepath.Join(root, rel))["skills"].([]any)
		for _, s := range skills {
			name, _ := s.(string)
			dir := filepath.Join(root, strings.TrimPrefix(name, "./"))
			if !seen[dir] {
				seen[dir] = true
				dirs = append(dirs, dir)
			}
		}
	}
	sort.Strings(dirs)
	if len(dirs) == 0 {
		t.Fatal("no plugin manifest lists a skill")
	}
	return dirs
}

// launcherStatements are the sentences each SKILL.md names its launcher with:
// Claude Code substitutes the plugin root into the first, and Codex, which
// substitutes nothing, leaves the agent the directory of the skill.
var launcherStatements = []string{
	"The launcher is `${CLAUDE_PLUGIN_ROOT}/scripts/steer`.",
	"the launcher is `scripts/steer` two directories above `<SKILL_DIR>`",
	"write it without `..`.",
}

// Every skill names its launcher as a path the agent writes out, and the
// launcher two directories above each skill exists and is executable.
func TestSkillsNameTheLauncherBesideTheSkill(t *testing.T) {
	for _, dir := range skillDirs(t) {
		raw, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		text := strings.Join(strings.Fields(string(raw)), " ")
		for _, statement := range launcherStatements {
			if !strings.Contains(text, statement) {
				t.Errorf("%s/SKILL.md does not say %q", dir, statement)
			}
		}
		info, err := os.Stat(filepath.Join(dir, "..", "..", "scripts", "steer"))
		if err != nil {
			t.Errorf("%s: no launcher two directories above the skill: %v", dir, err)
		} else if info.Mode().Perm()&0o111 == 0 {
			t.Errorf("%s: the launcher two directories above the skill is not executable", dir)
		}
	}
}

// launcherVariable is a shell variable or assignment carrying the launcher.
var launcherVariable = regexp.MustCompile(`STEER_(?:BIN|ROOT)=|\$\{?STEER_`)

// A permission rule matches a command by its text, and Claude Code refuses to
// check a command that expands a braced variable, so every steer command a
// skill runs starts with the <STEER_BIN> placeholder the agent fills with the
// launcher's absolute path, never with bare steer or a shell variable.
func TestSkillCommandsStartWithTheLauncherPlaceholder(t *testing.T) {
	placeholders := 0
	for _, dir := range skillDirs(t) {
		err := filepath.WalkDir(dir, func(file string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || filepath.Ext(file) != ".md" {
				return err
			}
			raw, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			if m := launcherVariable.FindString(string(raw)); m != "" {
				t.Errorf("%s carries the launcher in a shell variable (%s)", file, m)
			}
			fenced := false
			for i, line := range strings.Split(string(raw), "\n") {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "```") {
					fenced = !fenced
					continue
				}
				if !fenced {
					continue
				}
				switch {
				case strings.HasPrefix(trimmed, "<STEER_BIN> "):
					placeholders++
				case strings.HasPrefix(trimmed, "steer "), strings.HasPrefix(trimmed, "$"),
					strings.HasPrefix(trimmed, `"$`):
					t.Errorf("%s:%d runs a command the launcher placeholder does not start: %s", file, i+1, trimmed)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	if placeholders == 0 {
		t.Error("no skill runs a command through <STEER_BIN>")
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(raw)
}
