package mods

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// plugin writes a plugin root, with a hooks module when module is true.
func plugin(t *testing.T, root string, module bool) string {
	t.Helper()
	hooks := map[string]any{"hooks": map[string]any{}}
	if module {
		hooks = map[string]any{"modules": []string{"./register.js"}}
	}
	writeJSON(t, filepath.Join(root, "hooks", "hooks.json"), hooks)
	return root
}

// TestParseValidationSplitsHooksOutsideFilters pins that a filter holding an array or
// a pattern stays one hook, and that only the hooks that see a tool call are kept.
func TestParseValidationSplitsHooksOutsideFilters(t *testing.T) {
	report := `{"success":true,"manifest":{"errors":[]},"contents":[{"type":"hooks","errors":[],
		"notes":["./register.js hooks: *, tool.call{tool=Edit|Write}, tool.call{tool=/\"^a,b\"/}, session.start, tool.check{tool=Bash}",
		"./register.js calls: $.fs.read"]}]}`
	hooks, err := ParseValidation([]byte(report))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"*", "tool.call{tool=Edit|Write}", `tool.call{tool=/"^a,b"/}`, "session.start", "tool.check{tool=Bash}"}
	if !reflect.DeepEqual(hooks, want) {
		t.Fatalf("hooks = %q, want %q", hooks, want)
	}
	gotTool := ToolHandlers(hooks)
	wantTool := []string{"*", "tool.call{tool=Edit|Write}", `tool.call{tool=/"^a,b"/}`, "tool.check{tool=Bash}"}
	if !reflect.DeepEqual(gotTool, wantTool) {
		t.Fatalf("ToolHandlers = %q, want %q", gotTool, wantTool)
	}
}

// TestParseValidationRefusesAReportWithErrors pins that a module the validator
// refused, which Claude Code would not load as written, is an inspection failure and
// never a reading of "no tool hooks".
func TestParseValidationRefusesAReportWithErrors(t *testing.T) {
	report := `{"success":false,"manifest":{"errors":[]},"contents":[{"type":"hooks",
		"errors":[{"message":"the event name passed to on() is not a string literal"}],"notes":[]}]}`
	if _, err := ParseValidation([]byte(report)); err == nil {
		t.Fatal("a refused module parsed as a reading")
	}
	if _, err := ParseValidation([]byte(`{"success":true,"contents":[]}`)); err == nil {
		t.Fatal("a report with no hooks module parsed as a reading")
	}
}

// TestCandidatesFollowSettingsPrecedence pins that local settings override project
// settings, which override user settings, and that a plugin with no hooks module is
// no candidate.
func TestCandidatesFollowSettingsPrecedence(t *testing.T) {
	config := t.TempDir()
	repo := t.TempDir()
	a := plugin(t, filepath.Join(config, "plugins", "cache", "m", "a", "1"), true)
	b := plugin(t, filepath.Join(config, "plugins", "cache", "m", "b", "1"), true)
	c := plugin(t, filepath.Join(config, "plugins", "cache", "m", "c", "1"), false)
	writeJSON(t, filepath.Join(config, "plugins", "installed_plugins.json"), map[string]any{
		"version": 2,
		"plugins": map[string]any{
			"a@m": []map[string]string{{"scope": "user", "installPath": a}},
			"b@m": []map[string]string{{"scope": "user", "installPath": b}},
			"c@m": []map[string]string{{"scope": "user", "installPath": c}},
		},
	})
	writeJSON(t, filepath.Join(config, "settings.json"), map[string]any{
		"enabledPlugins": map[string]any{"a@m": true, "b@m": true, "c@m": true}})
	writeJSON(t, filepath.Join(repo, ".claude", "settings.json"), map[string]any{
		"enabledPlugins": map[string]any{"a@m": false, "b@m": false}})
	writeJSON(t, filepath.Join(repo, ".claude", "settings.local.json"), map[string]any{
		"enabledPlugins": map[string]any{"b@m": true}})

	got := Candidates(config, repo)
	want := []Candidate{{ID: "b@m", Path: b, Enabled: "local"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Candidates = %+v, want %+v", got, want)
	}
	outside := Candidates(config, "")
	if len(outside) != 2 || outside[0].ID != "a@m" || outside[1].ID != "b@m" {
		t.Fatalf("outside a repository, Candidates = %+v, want a@m and b@m from user settings", outside)
	}
}

// TestReadJoinsInspectionsToCurrentFiles pins the four readings: a fresh inspection
// with tool handlers, a mod changed since its inspection, a failed inspection, and a
// mod no inspection covers.
func TestReadJoinsInspectionsToCurrentFiles(t *testing.T) {
	dir := t.TempDir()
	cache := t.TempDir()
	tool := plugin(t, filepath.Join(dir, "tool"), true)
	changed := plugin(t, filepath.Join(dir, "changed"), true)
	failed := plugin(t, filepath.Join(dir, "failed"), true)
	fresh := plugin(t, filepath.Join(dir, "fresh"), true)
	candidates := []Candidate{
		{ID: "changed@m", Path: changed, Enabled: "user"},
		{ID: "failed@m", Path: failed, Enabled: "user"},
		{ID: "fresh@m", Path: fresh, Enabled: "user"},
		{ID: "tool@m", Path: tool, Enabled: "user"},
	}
	reports := map[string]string{
		tool:    `{"success":true,"contents":[{"type":"hooks","notes":["./r.js hooks: tool.call, session.start"]}]}`,
		changed: `{"success":true,"contents":[{"type":"hooks","notes":["./r.js hooks: session.start"]}]}`,
	}
	validate := func(_ context.Context, path string) ([]byte, error) {
		if r, ok := reports[path]; ok {
			return []byte(r), nil
		}
		return nil, errors.New("validator crashed")
	}
	rows := Inspect(context.Background(), candidates[:2], validate, "2.1.288")
	rows = append(rows, Inspect(context.Background(), candidates[3:], validate, "2.1.288")...)
	if err := UpdateCache(cache, rows); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(changed, "hooks", "hooks.json"), later, later); err != nil {
		t.Fatal(err)
	}

	states := map[string]string{}
	for _, e := range Read(candidates, cache) {
		states[e.ID] = e.State
		if e.ID == "tool@m" && !reflect.DeepEqual(e.Handlers, []string{"tool.call"}) {
			t.Errorf("tool@m handlers = %q, want only tool.call", e.Handlers)
		}
	}
	want := map[string]string{"changed@m": StateStale, "failed@m": StateFailed,
		"fresh@m": StateUninspected, "tool@m": StateToolHooks}
	if !reflect.DeepEqual(states, want) {
		t.Fatalf("states = %v, want %v", states, want)
	}
}

// TestUpdateCacheKeepsOtherInstalls pins that an inspection run where one repository's
// mods are enabled keeps the inspections of installs it did not read: a project mod
// that only another repository enables stays inspected there.
func TestUpdateCacheKeepsOtherInstalls(t *testing.T) {
	dir := t.TempDir()
	cache := t.TempDir()
	a := plugin(t, filepath.Join(dir, "a"), true)
	b := plugin(t, filepath.Join(dir, "b"), true)
	report := func(context.Context, string) ([]byte, error) {
		return []byte(`{"success":true,"contents":[{"type":"hooks","notes":["./r.js hooks: tool.check"]}]}`), nil
	}
	inA := []Candidate{{ID: "x@m", Path: a, Enabled: "project"}}
	inB := []Candidate{{ID: "x@m", Path: b, Enabled: "project"}}
	if err := UpdateCache(cache, Inspect(context.Background(), inA, report, "")); err != nil {
		t.Fatal(err)
	}
	if err := UpdateCache(cache, Inspect(context.Background(), inB, report, "")); err != nil {
		t.Fatal(err)
	}
	for _, cands := range [][]Candidate{inA, inB} {
		if got := Read(cands, cache); len(got) != 1 || got[0].State != StateToolHooks {
			t.Fatalf("Read(%s) = %+v; want the install inspected in its own run", cands[0].Path, got)
		}
	}
}
