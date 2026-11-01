package checks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const shebangDetector = "#!/usr/bin/env python3\nimport sys\nsys.exit(0)\n"

// A snapshot holding the detector at another mode takes the current detector's
// mode, so an executable shebang script runs under its own interpreter.
func TestASnapshotOverlayTakesTheDetectorsCurrentMode(t *testing.T) {
	root := checkRepo(t, shebangDetector)
	gitIn(t, root, "init", "-q")
	path := filepath.Join(root, noCount.Script)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	sha := commitAll(t, root, "detector not yet executable")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	_, results, err := Replay(root, noCount, CaseFile{Cases: []Case{snapshotCase("fixed", 0, "", sha, "")}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Outcome != OutcomeMatched {
		t.Fatalf("results %+v; want the script run under python3 and matched", results)
	}
}

// A setup that edits the detector's bytes or mode tests another detector, so
// the case proves nothing, whatever the check then does.
func TestASetupCannotChangeTheDetector(t *testing.T) {
	for name, tc := range map[string]struct {
		mode  os.FileMode
		setup string
	}{
		"bytes": {0o755, `printf 'echo literal test count printed; exit 1\n' > .steer/checks/no-count.sh`},
		"mode":  {0o644, "chmod +x .steer/checks/no-count.sh"},
	} {
		t.Run(name, func(t *testing.T) {
			root := checkRepo(t, "#!/bin/sh\necho literal test count printed\nexit 1\n")
			if err := os.Chmod(filepath.Join(root, noCount.Script), tc.mode); err != nil {
				t.Fatal(err)
			}
			c := Case{Name: "edits", ExitCode: exitCode(1), Diagnostic: "literal test count", Provenance: "planted", Setup: tc.setup}
			_, results, err := Replay(root, noCount, CaseFile{Cases: []Case{c}})
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 1 || results[0].Outcome != OutcomeInvalid ||
				!strings.Contains(results[0].Reason, "change detector file "+noCount.Script) {
				t.Fatalf("results %+v; want invalid naming the detector file", results)
			}
		})
	}
}

// A case file that reaches a detector by a path protects does not name, such as
// another spelling on a case-insensitive filesystem, makes the case invalid.
func TestACaseFileCannotChangeTheDetector(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "detector.sh", "exit 1\n")
	ran := false
	runner := caseRunner{
		detector: []string{"detector.sh"},
		protects: func(string) string { return "" },
		install:  func(tree string) error { writeFile(t, tree, "detector.sh", "exit 1\n"); return nil },
		run: func(string, Case, *CaseResult) string {
			ran = true
			return ""
		},
	}
	res := replayCase(root, "", runner, Case{Name: "aliased", ExitCode: exitCode(0), Files: map[string]string{"detector.sh": "exit 0\n"}})
	if ran || res.Outcome != OutcomeInvalid || !strings.Contains(res.Reason, "change detector file detector.sh") {
		t.Fatalf("result %+v, ran %v; want invalid before the check runs", res, ran)
	}
}

// A patch that edits the detector is named as the cause, even when the setup
// after it would fail.
func TestAPatchEditingTheDetectorIsNamedBeforeSetupRuns(t *testing.T) {
	root, fixed := snapshotRepo(t)
	writeFile(t, root, literalPatch, patchOf(t, root, noCount.Script, "exit 1\n"))
	c := snapshotCase("literal-count", 1, "literal test count", fixed, literalPatch)
	c.Setup = "exit 3"
	c.patch = []byte(patchOf(t, root, noCount.Script, "exit 1\n"))
	_, results, err := Replay(root, noCount, CaseFile{Cases: []Case{c}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || !strings.Contains(results[0].Reason, "the patch "+literalPatch+" changes detector file") {
		t.Fatalf("results %+v; want the patch named", results)
	}
}

// A directory input digests nothing, so it is refused by name and covers no
// path beneath it.
func TestADirectoryInputIsRefusedAndCoversNothing(t *testing.T) {
	root := checkRepo(t, noCountScript)
	writeFile(t, root, "data/a.json", "{}")
	d := noCount
	d.Inputs = []string{"data"}
	if _, err := DetectorOf(root, d); err == nil || !strings.Contains(err.Error(), "detector input data is a directory: declare each file it holds") {
		t.Fatalf("DetectorOf = %v; want the directory refused by name", err)
	}
	if inputCovers(d, "data/a.json") {
		t.Error("a directory input covers data/a.json; want it uncovered")
	}
	if !inputCovers(Declaration{Inputs: []string{"data/a.json"}}, "data") {
		t.Error("a file input does not cover the directory literal that holds it")
	}
}
