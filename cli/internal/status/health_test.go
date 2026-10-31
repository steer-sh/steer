package status

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/gate/reviewers"
	"github.com/steer-sh/steer-cli/internal/mods"
)

func healthyFixture() *SystemStatus {
	build := BuildIdentity{Version: "0.1.4", Commit: "aaa"}
	gate := ScopeArming{Harness: "codex", Scope: "global", Registered: true, Effective: true,
		Resolvable: ResolveResolvable, Trust: TrustApproved}
	return &SystemStatus{Posture: Posture{
		CommitGate: []ScopeArming{gate},
		Resolver:   []ScopeArming{gate},
		Masters: []MasterRuntime{{Harness: "codex", Scope: "global", Path: "/m", State: MasterCurrent,
			Trust: TrustApproved}},
		Plugins:          []PluginCopy{{Harness: "codex", Path: "/p", State: PluginSameBinary}},
		GuardInterpreter: InterpreterReading{Found: true},
		Builds:           Builds{Running: build, Hooks: build},
	}}
}

func availableProbe() reviewers.Probe { return reviewers.Probe{State: reviewers.ProbeAvailable} }

func TestHealthyVerdictNamesVersionAndAgreement(t *testing.T) {
	h := HealthOf(healthyFixture(), availableProbe())
	if h.Verdict != VerdictHealthy || h.Headline != "steer 0.1.4 — healthy" || len(h.Issues) != 0 {
		t.Fatalf("health = %+v", h)
	}
	joined := strings.Join(h.Ready, " ")
	for _, want := range []string{"Commit review and guards are ready.", "Hooks and plugins run the same build."} {
		if !strings.Contains(joined, want) {
			t.Errorf("ready = %q, want %q", joined, want)
		}
	}
}

// Each required reading can take the healthy verdict away.
func TestHealthVerdictPerReading(t *testing.T) {
	for name, tc := range map[string]struct {
		change func(*SystemStatus)
		probe  reviewers.Probe
		want   string
		title  string
	}{
		"hook not approved": {func(s *SystemStatus) { s.Posture.CommitGate[0].Trust = TrustMissing }, availableProbe(), VerdictAttention, "need approval"},
		"hash differs":      {func(s *SystemStatus) { s.Posture.Masters[0].Trust = TrustModified }, availableProbe(), VerdictAttention, "needs checking"},
		"resolver off":      {func(s *SystemStatus) { s.Posture.Resolver[0].Trust = TrustDisabled }, availableProbe(), VerdictAttention, "turned off"},
		"guards off":        {func(s *SystemStatus) { s.Posture.Masters[0].Trust = TrustDisabled }, availableProbe(), VerdictAttention, "turned off"},
		"approval unread":   {func(s *SystemStatus) { s.Posture.Masters[0].Trust = TrustUnknown }, availableProbe(), VerdictUnchecked, "could not check"},
		"reviewer unknown":  {func(*SystemStatus) {}, reviewers.Probe{State: reviewers.ProbeUnknown, Provider: "x"}, VerdictUnchecked, "could not check the reviewer"},
		"reviewer missing":  {func(*SystemStatus) {}, reviewers.Probe{State: reviewers.ProbeMissing, Executable: "x"}, VerdictAttention, "does not resolve"},
		"stale guards":      {func(s *SystemStatus) { s.Posture.Masters[0].State = MasterStale }, availableProbe(), VerdictAttention, "out of date"},
		"detached guards":   {func(s *SystemStatus) { s.Posture.Masters[0].State = MasterDetached }, availableProbe(), VerdictAttention, "runs none"},
		"rules drifted": {func(s *SystemStatus) {
			s.Posture.Rules = []RulesPresence{{Harness: "codex", Scope: "global", State: RulesDrifted}}
		}, availableProbe(), VerdictAttention, "instructions"},
		"rules unread": {func(s *SystemStatus) {
			s.Posture.Rules = []RulesPresence{{Harness: "codex", Scope: "global", State: RulesUnknown}}
		}, availableProbe(), VerdictUnchecked, "instructions"},
		"sources unread": {func(s *SystemStatus) {
			s.Posture.Masters[0].Sources = &claude.SourceComparison{State: claude.ComparisonUnavailable, Reason: "bad"}
		}, availableProbe(), VerdictUnchecked, "could not compare"},
		"hooks build unread": {func(s *SystemStatus) { s.Posture.Builds.Hooks = BuildIdentity{} }, availableProbe(), VerdictUnchecked, "build the hooks run"},
		"gate unconfirmed": {func(s *SystemStatus) {
			s.Posture.CommitGate[0] = ScopeArming{Harness: "pi", Scope: "global", Registered: true, Effective: true, Resolvable: ResolveResolvable, Trust: TrustUnknown}
		}, availableProbe(), VerdictUnchecked, "cannot be verified"},
		"store unread":   {func(s *SystemStatus) { s.StoreProblems = []StoreProblem{{Scope: "project", Error: "bad"}} }, availableProbe(), VerdictAttention, "guard list"},
		"team unread":    {func(s *SystemStatus) { s.Team.Error = "bad" }, availableProbe(), VerdictAttention, "lockfile"},
		"checks unread":  {func(s *SystemStatus) { s.Checks.Error = "bad" }, availableProbe(), VerdictUnchecked, "checks"},
		"checks unrun":   {func(s *SystemStatus) { s.Checks.Adopted = true }, availableProbe(), VerdictAttention, "no harness runs them"},
		"no interpreter": {func(s *SystemStatus) { s.Posture.GuardInterpreter = InterpreterReading{Command: GuardInterpreter} }, availableProbe(), VerdictAttention, "not on this PATH"},
		"hooks behind": {func(s *SystemStatus) {
			s.Posture.Plugins[0] = PluginCopy{Harness: "codex", Path: "/p", State: PluginDifferentBinary, Version: "0.1.5", Commit: "b"}
		}, availableProbe(), VerdictAttention, "behind"},
		"plugin build read": {func(s *SystemStatus) { s.Posture.Plugins[0].State = PluginUnknown }, availableProbe(), VerdictUnchecked, "plugin's build"},
	} {
		t.Run(name, func(t *testing.T) {
			s := healthyFixture()
			tc.change(s)
			h := HealthOf(s, tc.probe)
			if h.Verdict != tc.want {
				t.Fatalf("verdict = %s, want %s: %+v", h.Verdict, tc.want, h.Issues)
			}
			titles := ""
			for _, issue := range h.Issues {
				titles += issue.Title + "\n"
			}
			if !strings.Contains(titles, tc.title) {
				t.Errorf("issues %q do not name %q", titles, tc.title)
			}
			if len(h.Ready) != 0 {
				t.Errorf("a %s verdict still claims ready: %q", h.Verdict, h.Ready)
			}
		})
	}
}

// A development build's difference leaves configuration healthy and offers no repair.
func TestHealthDevDifferenceIsInformation(t *testing.T) {
	s := healthyFixture()
	s.Posture.Builds.Running = BuildIdentity{Version: "dev", Commit: "0403d11"}
	h := HealthOf(s, availableProbe())
	if h.Verdict != VerdictHealthy || !strings.Contains(h.Headline, "steer dev (0403d11) — configuration healthy; builds differ") {
		t.Fatalf("health = %+v", h)
	}
	if len(h.Issues) != 1 || h.Issues[0].Fix != "" || !strings.Contains(strings.Join(h.Issues[0].Detail, " "), "CLI: dev (0403d11)") {
		t.Errorf("issues = %+v", h.Issues)
	}
}

// One visit to /hooks clears every hook of a scope, so they make one issue.
func TestHealthGroupsApprovalIssues(t *testing.T) {
	s := healthyFixture()
	s.Posture.CommitGate[0].Trust = TrustMissing
	s.Posture.Masters[0].Trust = TrustMissing
	h := HealthOf(s, availableProbe())
	count := 0
	for _, issue := range h.Issues {
		if strings.Contains(issue.Title, "need approval") {
			count++
			if !strings.Contains(strings.Join(issue.Detail, " "), "commit review, guards") {
				t.Errorf("detail = %q", issue.Detail)
			}
		}
	}
	if count != 1 {
		t.Errorf("%d approval issues, want 1: %+v", count, h.Issues)
	}
}

// An unlisted harness's unreadable approval cannot be cleared, so it blocks nothing.
func TestHealthIgnoresUnlistedTrustUnknown(t *testing.T) {
	s := healthyFixture()
	s.Posture.Masters = append(s.Posture.Masters, MasterRuntime{Harness: "pi", Scope: "global", Path: "/x", State: MasterCurrent, Trust: TrustUnknown})
	if h := HealthOf(s, availableProbe()); h.Verdict != VerdictHealthy {
		t.Errorf("health = %+v", h)
	}
}

// The git channel counts as review with no harness hook.
func TestReadinessCountsPreCommit(t *testing.T) {
	s := &SystemStatus{Posture: Posture{PreCommit: true}}
	r := ReadinessOf(s, availableProbe())
	if !r.Reviewing || len(r.Steps) != 0 {
		t.Errorf("readiness = %+v", r)
	}
}

// A copy read as dev never names an install, even when its bytes differ.
func TestBuildIssuesOfferNoRepairForADevDifference(t *testing.T) {
	copyRow := PluginCopy{Harness: "codex", Path: "/p", State: PluginDifferentBinary, Version: "dev", Commit: "b"}
	build := BuildIdentity{Version: "0.1.4", Commit: "a"}
	issues := BuildIssues([]PluginCopy{copyRow}, build, build)
	if len(issues) != 1 || issues[0].Severity != SeverityInfo || issues[0].Fix != "" {
		t.Errorf("issues = %+v", issues)
	}
}

// A project registration's relative path resolves at the repository root, from any
// working directory and through a linked spelling of the same directory.
func TestRegisteredPathResolvesAcrossSpellings(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".steer", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	master := filepath.Join(root, ".steer", "hooks", "steer-hooks.py")
	if err := os.WriteFile(master, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	if !sameFilePath(filepath.Join(link, ".steer", "hooks", "steer-hooks.py"), master) {
		t.Error("a linked spelling read as another file")
	}
	if sameFilePath(filepath.Join(root, "other.py"), master) {
		t.Error("two files read as one")
	}
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Join(root, "sub"))
	if got := resolveRegistered(".steer/hooks/steer-hooks.py", "project"); !sameFilePath(got, master) {
		t.Errorf("resolved = %s, want %s", got, master)
	}
	if got := resolveRegistered("/abs/m.py", "project"); got != "/abs/m.py" {
		t.Errorf("an absolute path changed to %s", got)
	}
}

// An uninspected mod matters wherever a Claude Code hook is registered, not only the gate.
func TestHealthReadsModsBesideAnyClaudeHook(t *testing.T) {
	s := healthyFixture()
	s.Posture.Masters = append(s.Posture.Masters, MasterRuntime{Harness: "claude-code", Scope: "global", Path: "/g", State: MasterCurrent, Trust: TrustNotApplicable})
	s.Posture.Mods = []mods.Exposure{{ID: "m", State: mods.StateUninspected}}
	if h := HealthOf(s, availableProbe()); h.Verdict != VerdictUnchecked {
		t.Errorf("health = %+v", h)
	}
}

// Collect carries the caller's pre-commit reading into the posture readiness reads.
func TestCollectCarriesPreCommit(t *testing.T) {
	isolate(t)
	s, err := Collect(Options{GateStateDir: t.TempDir(), WindowDays: 7, PreCommit: true})
	if err != nil {
		t.Fatal(err)
	}
	if !s.Posture.PreCommit {
		t.Error("the pre-commit reading was dropped")
	}
}

// With two copies ahead, only the highest offers an install, so following it cannot be
// undone by a lower one.
func TestBuildIssuesOfferOnlyTheNewestInstall(t *testing.T) {
	hooks := BuildIdentity{Version: "0.1.4", Commit: "a"}
	copies := []PluginCopy{
		{Harness: "claude-code", Path: "/c", State: PluginDifferentBinary, Version: "0.1.6", Commit: "c"},
		{Harness: "codex", Path: "/x", State: PluginDifferentBinary, Version: "0.1.5", Commit: "b"},
	}
	issues := BuildIssues(copies, hooks, hooks)
	if len(issues) != 1 || !strings.Contains(issues[0].Fix, "/c/") {
		t.Errorf("issues = %+v", issues)
	}
}

// The install comes before a rebuild, which would otherwise embed the old runtime.
func TestHealthPutsTheInstallFirst(t *testing.T) {
	s := healthyFixture()
	s.Posture.Masters[0].State = MasterStale
	s.Posture.Plugins[0] = PluginCopy{Harness: "codex", Path: "/p", State: PluginDifferentBinary, Version: "0.1.5", Commit: "b"}
	h := HealthOf(s, availableProbe())
	if len(h.Issues) < 2 || !strings.HasPrefix(h.Issues[0].Title, hooksBehindPrefix) {
		t.Errorf("issues = %+v", h.Issues)
	}
}
