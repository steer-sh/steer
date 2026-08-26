package mine

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestInterventionsRoundTrip pins the byte-identity the tracked interventions.jsonl
// depends on: parse every live line, re-emit it through the Go writer, and assert the
// bytes are unchanged. A reformat here is a whole-file diff on the next repo sync.
func TestInterventionsRoundTrip(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	path := filepath.Join(home, ".claude", "state", "commit-review", "interventions.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("no live interventions.jsonl: %v", err)
	}
	for i, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.UseNumber()
		var rec map[string]any
		if err := dec.Decode(&rec); err != nil {
			t.Fatalf("line %d: parse: %v", i+1, err)
		}
		if got := pyDumps(rec, 0); got != line {
			t.Errorf("line %d not byte-identical:\n python: %s\n     go: %s", i+1, line, got)
		}
	}
}

// The read-path and write-path differential replays run mine.py beside the Go reader
// over the real corpus. Both are gated on COMMIT_GATE_REPLAY_MINE, so the turn-end gate
// never runs them; they read private corpus content and commit none of it.

func replayEnv(t *testing.T) (minePy, corpus string) {
	t.Helper()
	if os.Getenv("COMMIT_GATE_REPLAY_MINE") == "" {
		t.Skip("COMMIT_GATE_REPLAY_MINE not set")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	minePy = filepath.Join(home, ".claude", "hooks", "commit-review", "mine.py")
	corpus = filepath.Join(home, ".claude", "state", "commit-review")
	if _, err := os.Stat(minePy); err != nil {
		t.Skipf("no mine.py: %v", err)
	}
	return minePy, corpus
}

// runPy runs mine.py under a state directory and returns stdout.
func runPy(t *testing.T, minePy, stateDir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("python3", append([]string{minePy}, args...)...)
	cmd.Env = append(os.Environ(), "COMMIT_REVIEW_STATE="+stateDir)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("mine.py %v: %v\n%s", args, err, errBuf.String())
	}
	return out.String()
}

func goOut(t *testing.T, fn func(w *bytes.Buffer) error) string {
	t.Helper()
	var buf bytes.Buffer
	if err := fn(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func decodeAny(t *testing.T, s string) any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("decode: %v\n%s", err, s)
	}
	return v
}

func TestReplayMine(t *testing.T) {
	minePy, corpus := replayEnv(t)

	// --json outputs: decoded and deep-compared, numbers as json.Number so 0.0 and 0
	// stay distinct.
	jsonCases := []struct {
		name string
		args []string
		fn   func(w *bytes.Buffer) error
	}{
		{"findings accepted", []string{"findings", "--kind", "accepted", "--json"},
			func(w *bytes.Buffer) error { return Findings(w, corpus, FindingsOpts{Kind: "accepted", JSON: true}) }},
		{"findings rejected", []string{"findings", "--kind", "rejected", "--json"},
			func(w *bytes.Buffer) error { return Findings(w, corpus, FindingsOpts{Kind: "rejected", JSON: true}) }},
		{"findings both", []string{"findings", "--kind", "both", "--json"},
			func(w *bytes.Buffer) error { return Findings(w, corpus, FindingsOpts{Kind: "both", JSON: true}) }},
		{"findings strict-joins", []string{"findings", "--kind", "accepted", "--strict-joins", "--json"},
			func(w *bytes.Buffer) error {
				return Findings(w, corpus, FindingsOpts{Kind: "accepted", StrictJoins: true, JSON: true})
			}},
		{"findings include-empty", []string{"findings", "--kind", "accepted", "--include-empty", "--json"},
			func(w *bytes.Buffer) error {
				return Findings(w, corpus, FindingsOpts{Kind: "accepted", IncludeEmpty: true, JSON: true})
			}},
		{"score all", []string{"score", "--all", "--json"},
			func(w *bytes.Buffer) error {
				return Score(w, corpus, ScoreOpts{All: true, MinAfter: MinAfterChains, JSON: true})
			}},
		{"list", []string{"list", "--json"},
			func(w *bytes.Buffer) error { return List(w, corpus, true) }},
	}
	for _, tc := range jsonCases {
		py := decodeAny(t, runPy(t, minePy, corpus, tc.args...))
		go_ := decodeAny(t, goOut(t, tc.fn))
		if !reflect.DeepEqual(py, go_) {
			t.Errorf("%s: JSON differs", tc.name)
		}
	}

	// Text outputs: byte for byte.
	textCases := []struct {
		name string
		args []string
		fn   func(w *bytes.Buffer) error
	}{
		{"findings accepted text", []string{"findings", "--kind", "accepted"},
			func(w *bytes.Buffer) error { return Findings(w, corpus, FindingsOpts{Kind: "accepted"}) }},
		{"findings both text", []string{"findings", "--kind", "both"},
			func(w *bytes.Buffer) error { return Findings(w, corpus, FindingsOpts{Kind: "both"}) }},
		{"score text", []string{"score"},
			func(w *bytes.Buffer) error { return Score(w, corpus, ScoreOpts{MinAfter: MinAfterChains}) }},
		{"list text", []string{"list"},
			func(w *bytes.Buffer) error { return List(w, corpus, false) }},
	}
	for _, tc := range textCases {
		py := runPy(t, minePy, corpus, tc.args...)
		go_ := goOut(t, tc.fn)
		if py != go_ {
			t.Errorf("%s: text differs\n python:\n%s\n go:\n%s", tc.name, py, go_)
		}
	}

	// match text for every recorded class regex.
	records, err := Load(corpus)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range records {
		regex := str(r["match"])
		kind := RecordKindFilter(r)
		py := runPy(t, minePy, corpus, "match", regex, "--kind", kind)
		go_ := goOut(t, func(w *bytes.Buffer) error { return Match(w, corpus, "", regex, kind, false) })
		if py != go_ {
			t.Errorf("match %q: text differs\n python:\n%s\n go:\n%s", regex, py, go_)
		}
	}
}

// TestReplayMineWrite runs one register/reject/supersede sequence through both
// implementations on a copied corpus and compares the resulting interventions.jsonl
// byte for byte. It is the half the read-only replay cannot cover. Retire is excluded:
// its retired_ts is now(), which two separately timed processes cannot agree on.
func TestReplayMineWrite(t *testing.T) {
	minePy, corpus := replayEnv(t)

	seed := func() string {
		dir := t.TempDir()
		matches, _ := filepath.Glob(filepath.Join(corpus, "adjudication*.jsonl"))
		matches = append(matches, filepath.Join(corpus, "interventions.jsonl"))
		for _, src := range matches {
			data, err := os.ReadFile(src)
			if err != nil {
				continue
			}
			if err := os.WriteFile(filepath.Join(dir, filepath.Base(src)), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	pyDir, goDir := seed(), seed()

	// register (forced past any baseline/overlap refusal), reject, then supersede.
	runPy(t, minePy, pyDir, "register", "--class", "Replay Probe", "--match", "the",
		"--tier", "check", "--target", "check.sh", "--installed-ts", "1780000000", "--force")
	runPy(t, minePy, pyDir, "reject", "--class", "Replay Refusal", "--match", "flag",
		"--reason", "r", "--reopen", "o", "--rejected-ts", "1780000001", "--force")
	runPy(t, minePy, pyDir, "register", "--class", "Replay Probe v2", "--match", "the",
		"--tier", "check", "--target", "check.sh", "--installed-ts", "1780000002",
		"--supersedes", "replay-probe", "--force")

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(Register(bytesDiscard(), goDir, RegisterOpts{Class: "Replay Probe", Match: "the",
		Tier: "check", Target: "check.sh", InstalledTS: 1780000000, Force: true}))
	must(Reject(bytesDiscard(), goDir, RejectOpts{Class: "Replay Refusal", Match: "flag",
		Reason: "r", Reopen: "o", RejectedTS: 1780000001, Force: true}))
	must(Register(bytesDiscard(), goDir, RegisterOpts{Class: "Replay Probe v2", Match: "the",
		Tier: "check", Target: "check.sh", InstalledTS: 1780000002, Supersedes: "replay-probe", Force: true}))

	pyFile, _ := os.ReadFile(filepath.Join(pyDir, "interventions.jsonl"))
	goFile, _ := os.ReadFile(filepath.Join(goDir, "interventions.jsonl"))
	if !bytes.Equal(pyFile, goFile) {
		t.Errorf("interventions.jsonl differs after the write sequence\n python:\n%s\n go:\n%s", pyFile, goFile)
	}
}

func bytesDiscard() *bytes.Buffer { return &bytes.Buffer{} }
