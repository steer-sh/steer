//go:build e2e

// Package e2e drives the built steer binary as a subprocess inside a throwaway home,
// with stub reviewer CLIs on PATH. Run it from cli/: go test -tags e2e ./e2e/...
package e2e

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/steer-sh/steer-cli/e2e/stubreviewer/stubcall"
)

var (
	steerBin string
	stubBin  string
)

// providerBinary maps a reviewer provider to the executable name its adapter runs.
var providerBinary = map[string]string{"agy": "agy", "claude-code": "claude", "codex": "codex"}

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "steer-e2e-bin-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	steerBin = filepath.Join(dir, exe("steer"))
	stubBin = filepath.Join(dir, exe("stubreviewer"))
	for _, b := range [][]string{{steerBin, ".."}, {stubBin, "./stubreviewer"}} {
		out, err := exec.Command("go", "build", "-o", b[0], b[1]).CombinedOutput()
		if err != nil {
			fmt.Fprintf(os.Stderr, "build %s: %v\n%s", b[1], err, out)
			os.RemoveAll(dir)
			os.Exit(1)
		}
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func exe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// Sandbox is one case's home, repository and PATH. Nothing in it reads the
// operator's environment except the locations of git, python3, sh and bash.
type Sandbox struct {
	t     *testing.T
	Root  string
	Home  string
	Repo  string
	Stubs string
	env   []string
}

type sandboxOptions struct {
	homeName string
	repoName string
	noStubs  bool
	noBase   bool
	tools    []string
}

// SandboxOption changes one part of a new sandbox.
type SandboxOption func(*sandboxOptions)

// WithHomeName names the home directory, so a case can put shell-special bytes in it.
func WithHomeName(name string) SandboxOption {
	return func(o *sandboxOptions) { o.homeName = name }
}

// WithRepoName names the repository directory.
func WithRepoName(name string) SandboxOption {
	return func(o *sandboxOptions) { o.repoName = name }
}

// WithoutStubs leaves every reviewer CLI off PATH.
func WithoutStubs() SandboxOption {
	return func(o *sandboxOptions) { o.noStubs = true }
}

// WithoutBaseCommit leaves the repository with no commit, so the next one is a root.
func WithoutBaseCommit() SandboxOption {
	return func(o *sandboxOptions) { o.noBase = true }
}

// WithTools links more of the operator's executables into the sandbox PATH.
func WithTools(names ...string) SandboxOption {
	return func(o *sandboxOptions) { o.tools = append(o.tools, names...) }
}

// NewSandbox makes a home, a tool directory, a stub directory and a repository
// with one base commit.
func NewSandbox(t *testing.T, opts ...SandboxOption) *Sandbox {
	t.Helper()
	o := sandboxOptions{homeName: "home", repoName: "repo"}
	for _, opt := range opts {
		opt(&o)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := &Sandbox{t: t, Root: root, Home: filepath.Join(root, o.homeName),
		Repo: filepath.Join(root, o.repoName), Stubs: filepath.Join(root, "stubs")}
	tools := filepath.Join(root, "tools")
	for _, dir := range []string{s.Home, s.Repo, s.Stubs, tools, s.path("tmp"), s.path("stubstate"),
		filepath.Join(s.Home, ".claude"), filepath.Join(s.Home, ".codex")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range append([]string{"git", "python3", "sh", "bash"}, o.tools...) {
		real, err := exec.LookPath(name)
		if err != nil {
			t.Fatalf("e2e needs %s on PATH: %v", name, err)
		}
		mustLink(t, real, filepath.Join(tools, exe(name)))
	}
	mustLink(t, steerBin, filepath.Join(tools, exe("steer")))
	if !o.noStubs {
		for _, name := range providerBinary {
			mustLink(t, stubBin, filepath.Join(s.Stubs, exe(name)))
		}
	}
	gitconfig := filepath.Join(s.Home, ".gitconfig")
	if err := os.WriteFile(gitconfig, []byte("[user]\n\tname = Steer E2E\n\temail = e2e@steer.invalid\n[init]\n\tdefaultBranch = main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.env = []string{
		"HOME=" + s.Home,
		"CODEX_HOME=" + filepath.Join(s.Home, ".codex"),
		"PATH=" + s.Stubs + string(os.PathListSeparator) + tools,
		"TMPDIR=" + s.path("tmp"),
		"LANG=C",
		"TERM=dumb",
		"GIT_CONFIG_GLOBAL=" + gitconfig,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Steer E2E", "GIT_AUTHOR_EMAIL=e2e@steer.invalid",
		"GIT_COMMITTER_NAME=Steer E2E", "GIT_COMMITTER_EMAIL=e2e@steer.invalid",
		"COMMIT_REVIEW_HOST=e2e",
		"STEER_STUB_STATE=" + s.path("stubstate"),
		"STEER_STUB_LOG=" + s.path("stub.jsonl"),
		"STEER_STUB_SCRIPT=" + s.path("stub-script"),
	}
	s.Git("init", "-q")
	if !o.noBase {
		s.Write("README.md", "base\n")
		s.Git("add", "README.md")
		s.Git("commit", "-q", "-m", "base")
	}
	return s
}

func mustLink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func (s *Sandbox) path(rel string) string { return filepath.Join(s.Root, rel) }

// Result is one finished process.
type Result struct {
	Stdout, Stderr string
	Code           int
}

// Output is stdout and stderr together, for a message that may land on either.
func (r Result) Output() string { return r.Stdout + r.Stderr }

// Run runs name in dir with the sandbox environment plus extra, feeding stdin.
func (s *Sandbox) Run(dir, stdin string, extra []string, name string, args ...string) Result {
	s.t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(append([]string{}, s.env...), extra...)
	// Resolve name against the sandbox PATH, not this process's.
	if !strings.ContainsRune(name, os.PathSeparator) {
		for _, dir := range filepath.SplitList(s.Getenv("PATH")) {
			if p := filepath.Join(dir, exe(name)); fileExists(p) {
				cmd.Path = p
				break
			}
		}
	}
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			s.t.Fatalf("run %s %q: %v", name, args, err)
		}
		code = exitErr.ExitCode()
	}
	return Result{Stdout: out.String(), Stderr: errb.String(), Code: code}
}

// Steer runs the built binary from the repository.
func (s *Sandbox) Steer(args ...string) Result {
	s.t.Helper()
	return s.Run(s.Repo, "", nil, "steer", args...)
}

// MustSteer runs the binary and fails the case on a non-zero exit.
func (s *Sandbox) MustSteer(args ...string) Result {
	s.t.Helper()
	r := s.Steer(args...)
	if r.Code != 0 {
		s.t.Fatalf("steer %q exited %d\nstdout:\n%s\nstderr:\n%s", args, r.Code, r.Stdout, r.Stderr)
	}
	return r
}

// Git runs git in the repository and fails the case on a non-zero exit.
func (s *Sandbox) Git(args ...string) string {
	s.t.Helper()
	r := s.Run(s.Repo, "", nil, "git", args...)
	if r.Code != 0 {
		s.t.Fatalf("git %q exited %d: %s", args, r.Code, r.Stderr)
	}
	return r.Stdout
}

// Write writes a file relative to the repository.
func (s *Sandbox) Write(rel, content string) {
	s.t.Helper()
	p := filepath.Join(s.Repo, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		s.t.Fatal(err)
	}
}

// Getenv reads one variable of the sandbox environment.
func (s *Sandbox) Getenv(key string) string {
	for _, kv := range s.env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v
		}
	}
	return ""
}

// Setenv adds or replaces one variable of the sandbox environment.
func (s *Sandbox) Setenv(key, value string) {
	for i, kv := range s.env {
		if k, _, _ := strings.Cut(kv, "="); k == key {
			s.env[i] = key + "=" + value
			return
		}
	}
	s.env = append(s.env, key+"="+value)
}

// Reviewer configures the gate's reviewer through the CLI, as a user would.
func (s *Sandbox) Reviewer(provider, model, effort string, runs, timeout int) {
	s.t.Helper()
	s.MustSteer("config", "set", "reviewer.provider", provider)
	s.MustSteer("config", "set", "reviewer.model", model)
	s.MustSteer("config", "set", "reviewer.effort", effort)
	s.MustSteer("config", "set", "reviewer.runs", fmt.Sprint(runs))
	s.MustSteer("config", "set", "reviewer.timeout", fmt.Sprint(timeout))
}

// Script sets the stub's reply modes, one per call, and resets its call count.
func (s *Sandbox) Script(modes ...string) {
	s.t.Helper()
	if err := os.WriteFile(s.path("stub-script"), []byte(strings.Join(modes, "\n")+"\n"), 0o644); err != nil {
		s.t.Fatal(err)
	}
	if err := os.RemoveAll(s.path("stubstate")); err != nil {
		s.t.Fatal(err)
	}
	if err := os.MkdirAll(s.path("stubstate"), 0o755); err != nil {
		s.t.Fatal(err)
	}
	os.Remove(s.path("stub.jsonl"))
}

// Calls reads every stub call logged since the last Script.
func (s *Sandbox) Calls() []stubcall.Call {
	s.t.Helper()
	f, err := os.Open(s.path("stub.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		s.t.Fatal(err)
	}
	defer f.Close()
	var calls []stubcall.Call
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 1<<20), 64<<20)
	for scan.Scan() {
		var c stubcall.Call
		if err := json.Unmarshal(scan.Bytes(), &c); err != nil {
			s.t.Fatalf("stub log: %v", err)
		}
		calls = append(calls, c)
	}
	return calls
}

// LedgerRows reads the sandbox's commit-review ledger shard.
func (s *Sandbox) LedgerRows() []map[string]any {
	s.t.Helper()
	data, err := os.ReadFile(filepath.Join(s.Home, ".steer", "state", "commit-review", "adjudication.e2e.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		s.t.Fatal(err)
	}
	var rows []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			s.t.Fatalf("ledger row: %v", err)
		}
		rows = append(rows, row)
	}
	return rows
}

// Head is the repository's HEAD commit, or "" before the first commit.
func (s *Sandbox) Head() string {
	s.t.Helper()
	r := s.Run(s.Repo, "", nil, "git", "rev-parse", "-q", "--verify", "HEAD")
	return strings.TrimSpace(r.Stdout)
}

// StageCode stages a new Python file large enough that the gate reviews it.
func (s *Sandbox) StageCode(name string) {
	s.t.Helper()
	var b strings.Builder
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&b, "def %s_%d(x):\n    return x + %d\n", strings.TrimSuffix(name, ".py"), i, i)
	}
	s.Write(name, b.String())
	s.Git("add", name)
}

// RegisteredCommand finds the hook command under rel in the sandbox that runs sub.
func (s *Sandbox) RegisteredCommand(rel, sub string) string {
	s.t.Helper()
	data, err := os.ReadFile(rel)
	if err != nil {
		s.t.Fatal(err)
	}
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		s.t.Fatalf("%s: %v", rel, err)
	}
	var found string
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if c, ok := v["command"].(string); ok && strings.Contains(c, sub) {
				found = c
			}
			for _, x := range v {
				walk(x)
			}
		case []any:
			for _, x := range v {
				walk(x)
			}
		}
	}
	walk(doc)
	if found == "" {
		s.t.Fatalf("%s registers no command for %q:\n%s", rel, sub, data)
	}
	return found
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// WaitForRefresh waits for a detached `steer report refresh` to finish: its last write
// is last_refresh, made under .refresh.lock, which it removes on the way out.
func (s *Sandbox) WaitForRefresh() {
	s.t.Helper()
	dir := filepath.Join(s.Home, ".steer", "state", "steer-report")
	deadline := time.Now().Add(20 * time.Second)
	for !fileExists(filepath.Join(dir, "last_refresh")) || fileExists(filepath.Join(dir, ".refresh.lock")) {
		if time.Now().After(deadline) {
			s.t.Fatalf("the detached refresh did not finish within 20s in %s", dir)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
