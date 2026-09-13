package checkscope

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/gate/mine"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/statestore"
)

const countScript = `#!/usr/bin/env bash
set -euo pipefail
if grep -rnE 'echo .*[0-9]+ tests' src; then
  echo "literal test count printed" >&2
  exit 1
fi
`

func exitCode(n int) *int { return &n }

// validatedRepo is a repository whose one script check passed its cases, under a
// temporary home that holds this machine's evidence and harness settings.
func validatedRepo(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("replay needs bash")
	}
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	if _, _, err := checks.Init(root); err != nil {
		t.Fatalf("Init: %v", err)
	}
	write(t, root, ".steer/checks/no-count.sh", countScript)
	cases := checks.CaseFile{Version: checks.CasesVersion, Cases: []checks.Case{
		{Name: "clean", ExitCode: exitCode(0), Provenance: "planted",
			Files: map[string]string{"src/run.sh": "echo \"tests passed\"\n"}},
		{Name: "literal-count", ExitCode: exitCode(1), Diagnostic: "literal test count",
			Provenance: "planted",
			Files:      map[string]string{"src/run.sh": "echo \"42 tests passed\"\n"}},
	}}
	data, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	write(t, root, ".steer/checks/cases/no-count.json", string(data))
	decl := checks.Declaration{ID: "no-count", Kind: checks.KindScript, Script: ".steer/checks/no-count.sh"}
	if run, err := checks.Add(root, decl); err != nil {
		t.Fatalf("Add: %v (run %+v)", err, run)
	}
	return root
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// adopt appends the adoption event an isolated oracle run would have written.
func adopt(t *testing.T, root string) {
	t.Helper()
	identity, err := checks.RepoIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	line, err := json.Marshal(checks.Adoption{
		Version: checks.AdoptionVersion, Action: checks.ActionAdopt,
		Repo: identity, At: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	dir := statestore.Resolve(checks.StateComponent)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "adoptions.jsonl"), append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := checks.AdoptionOf(identity); err != nil || !ok {
		t.Fatalf("the fixture adoption did not take: ok=%v err=%v", ok, err)
	}
}

func arm(t *testing.T) {
	t.Helper()
	if err := claude.RegisterCheckHook(claude.GlobalScope,
		"steer check hook --harness claude-code", checks.RegisteredHookTimeout()); err != nil {
		t.Fatalf("RegisterCheckHook: %v", err)
	}
}

// A bound record names the declared check, the resolved repository, the detector the
// record was registered against, the receipt that validated it, and the adoption and
// registration that run it.
func TestBindFreezesTheCheckItsRepositoryAndItsInstallation(t *testing.T) {
	root := validatedRepo(t)
	adopt(t, root)
	arm(t)

	facts, err := Bind("check", root, "no-count", []harness.ID{harness.ClaudeCode})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	for _, key := range mine.CheckBindingKeys {
		if facts[key] == nil || facts[key] == "" {
			t.Errorf("binding key %s is %v; want the frozen fact", key, facts[key])
		}
	}
	identity, _ := checks.RepoIdentity(root)
	if facts[mine.CheckRepoKey] != identity || facts[mine.CheckKindKey] != checks.KindScript {
		t.Errorf("facts %v; want the resolved root %s and a script binding", facts, identity)
	}
	history, _ := checks.History(root)
	if len(history) == 0 || facts[mine.CheckReceiptKey] != history[len(history)-1].RunID {
		t.Errorf("receipt %v; want the run id of the validated replay", facts[mine.CheckReceiptKey])
	}
	det, _ := checks.DetectorOf(root, checks.Declaration{
		ID: "no-count", Kind: checks.KindScript, Script: ".steer/checks/no-count.sh"})
	if facts[mine.CheckDigestKey] != det.Digest {
		t.Errorf("digest %v; want the current detector digest %s", facts[mine.CheckDigestKey], det.Digest)
	}

	// Another rung binds no check, so its registration reads no manifest at all.
	if facts, err := Bind("review", root, "", nil); err != nil || facts != nil {
		t.Errorf("Bind on the review rung: facts=%v err=%v", facts, err)
	}
}

// Each prerequisite of the binding is refused on its own, and the error names the
// command that supplies it.
func TestBindRefusesAnUnrunnableCheck(t *testing.T) {
	root := validatedRepo(t)

	if _, err := Bind("check", root, "", []harness.ID{harness.ClaudeCode}); err == nil {
		t.Error("a check tier with no --check-id was bound")
	}
	if _, err := Bind("check", root, "not-declared", []harness.ID{harness.ClaudeCode}); err == nil ||
		!strings.Contains(err.Error(), "steer check add") {
		t.Errorf("an undeclared check: %v; want the declaration step named", err)
	}
	if _, err := Bind("check", root, "no-count", []harness.ID{harness.ClaudeCode}); err == nil ||
		!strings.Contains(err.Error(), "steer check adopt") {
		t.Errorf("an unadopted repository: %v; want the adoption step named", err)
	}

	adopt(t, root)
	if _, err := Bind("check", root, "no-count", []harness.ID{harness.ClaudeCode}); err == nil ||
		!strings.Contains(err.Error(), "steer check install") {
		t.Errorf("an unregistered runner: %v; want the registration step named", err)
	}

	arm(t)
	if _, err := Bind("check", root, "no-count", nil); err == nil ||
		!strings.Contains(err.Error(), "--scope-harness") {
		t.Errorf("a record naming no harness: %v; want the scope flag named", err)
	}
	if _, err := Bind("check", root, "no-count", []harness.ID{harness.ClaudeCode, harness.Codex}); err == nil ||
		!strings.Contains(err.Error(), "codex") {
		t.Errorf("a harness with no runner: %v; want codex named", err)
	}

	// An edited detector leaves the receipt stale, and a stale receipt is not proof
	// of the detector a new record would bind.
	write(t, root, ".steer/checks/no-count.sh", countScript+"echo edited\n")
	if _, err := Bind("check", root, "no-count", []harness.ID{harness.ClaudeCode}); err == nil ||
		!strings.Contains(err.Error(), "steer check test") {
		t.Errorf("a stale receipt: %v; want the replay step named", err)
	}
}

const respGoMod = "module example.com/svc\n\ngo 1.21\n"

// A go-test binding registers under the same contract as a script: its receipt
// comes from replaying the named test, and the frozen digest is the Go
// detector's.
func TestBindFreezesAGoTestBinding(t *testing.T) {
	cache, err := exec.Command("go", "env", "GOCACHE").Output()
	if err != nil {
		t.Skipf("go toolchain unavailable: %v", err)
	}
	modcache, _ := exec.Command("go", "env", "GOMODCACHE").Output()
	t.Setenv("GOCACHE", strings.TrimSpace(string(cache)))
	t.Setenv("GOMODCACHE", strings.TrimSpace(string(modcache)))
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	if _, _, err := checks.Init(root); err != nil {
		t.Fatalf("Init: %v", err)
	}
	fixed := "package resp\n\nfunc Response() string { return `{\"id\": 1}` }\n"
	broken := "package resp\n\nfunc Response() string { return `{\"name\": 1}` }\n"
	write(t, root, "svc/go.mod", respGoMod)
	write(t, root, "svc/resp/resp.go", fixed)
	respTest, err := os.ReadFile(filepath.Join("testdata", "resp_test.go.txt"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, root, "svc/resp/resp_test.go", string(respTest))
	goCase := func(name string, exit int, diagnostic, production string) checks.Case {
		return checks.Case{Name: name, ExitCode: exitCode(exit), Diagnostic: diagnostic, Provenance: "planted",
			Files: map[string]string{"svc/go.mod": respGoMod, "svc/resp/resp.go": production}}
	}
	data, err := json.Marshal(checks.CaseFile{Version: checks.CasesVersion, Cases: []checks.Case{
		goCase("fixed", 0, "", fixed), goCase("omits-id", 1, "missing field id", broken)}})
	if err != nil {
		t.Fatal(err)
	}
	write(t, root, ".steer/checks/cases/resp-fields.json", string(data))
	decl := checks.Declaration{ID: "resp-fields", Kind: checks.KindGoTest, Package: "svc/resp", Test: "TestResponse"}
	if run, err := checks.Add(root, decl); err != nil {
		t.Fatalf("Add: %v (cases %+v)", err, run.Cases)
	}
	adopt(t, root)
	arm(t)

	facts, err := Bind("check", root, decl.ID, []harness.ID{harness.ClaudeCode})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	det, err := checks.DetectorOf(root, decl)
	if err != nil {
		t.Fatal(err)
	}
	if facts[mine.CheckKindKey] != checks.KindGoTest || facts[mine.CheckDigestKey] != det.Digest {
		t.Fatalf("facts %v; want a go-test binding frozen at digest %s", facts, det.Digest)
	}
	history, _ := checks.History(root)
	if len(history) == 0 || facts[mine.CheckReceiptKey] != history[len(history)-1].RunID {
		t.Errorf("receipt %v; want the run id of the named test's replay", facts[mine.CheckReceiptKey])
	}
}

// Armed reads both scopes and both harnesses back from the files they were written to.
func TestArmedNamesEveryRegisteredHarness(t *testing.T) {
	validatedRepo(t)
	if armed, err := Armed(""); err != nil || len(armed) != 0 {
		t.Fatalf("armed %v err %v; want nothing registered in a fresh home", armed, err)
	}
	arm(t)
	armed, err := Armed("")
	if err != nil || len(armed) != 1 || armed[0] != harness.ClaudeCode {
		t.Fatalf("armed %v err %v; want claude-code alone", armed, err)
	}
	a, err := RunnerArming(harness.ClaudeCode, claude.GlobalScope, "")
	if err != nil || !a.Registered || a.Timeout != checks.RegisteredHookTimeout() {
		t.Fatalf("arming %+v err %v; want the registered runner and its timeout", a, err)
	}
}
