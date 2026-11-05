package checks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Snapshot overlays stay inside the tree through file and directory links;
// relative links to files within the tree still work.
func TestSnapshotDetectorOverlaysStayInsideTheCaseTree(t *testing.T) {
	for _, link := range []string{"file", "directory", "internal"} {
		t.Run(link, func(t *testing.T) {
			root := checkRepo(t, "exit 0\n")
			gitIn(t, root, "init", "-q")
			outside := t.TempDir()
			writeFile(t, outside, "check.sh", "external sentinel\n")
			if link == "directory" {
				if err := os.Symlink(outside, filepath.Join(root, "detector")); err != nil {
					t.Fatal(err)
				}
			} else {
				writeFile(t, root, "detector/current.sh", "exit 42\n")
				target := filepath.Join(outside, "check.sh")
				if link == "internal" {
					target = "current.sh"
				}
				if err := os.Symlink(target, filepath.Join(root, "detector/check.sh")); err != nil {
					t.Fatal(err)
				}
			}
			sha := commitAll(t, root, "snapshot with detector link")
			if err := os.RemoveAll(filepath.Join(root, "detector")); err != nil {
				t.Fatal(err)
			}
			writeFile(t, root, "detector/check.sh", "exit 0\n")
			_, results, err := Replay(root, Declaration{ID: "safe", Kind: KindScript, Script: "detector/check.sh"}, CaseFile{Cases: []Case{snapshotCase("snapshot", 0, "", sha, "")}})
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(filepath.Join(outside, "check.sh"))
			if err != nil || string(got) != "external sentinel\n" {
				t.Fatalf("external target changed: %q, %v", got, err)
			}
			want := OutcomeInvalid
			if link == "internal" {
				want = OutcomeMatched
			}
			if len(results) != 1 || results[0].Outcome != want {
				t.Fatalf("results %+v; want %s", results, want)
			}
		})
	}
}

// Case writes cannot escape through a link in the case tree.
func TestReplayCaseFilesStayInsideTheCaseTree(t *testing.T) {
	for _, link := range []string{"file", "directory"} {
		t.Run(link, func(t *testing.T) {
			outside := t.TempDir()
			writeFile(t, outside, "input", "external sentinel\n")
			runner := caseRunner{
				protects: func(string) string { return "" },
				install: func(tree string) error {
					if link == "directory" {
						return os.Symlink(outside, filepath.Join(tree, "data"))
					}
					if err := os.Mkdir(filepath.Join(tree, "data"), 0o755); err != nil {
						return err
					}
					return os.Symlink(filepath.Join(outside, "input"), filepath.Join(tree, "data/input"))
				},
				run: func(string, Case, *CaseResult) string { return "" },
			}
			res := replayCase(t.TempDir(), "", runner, Case{Name: "linked", ExitCode: exitCode(0), Files: map[string]string{"data/input": "case input\n"}})
			got, err := os.ReadFile(filepath.Join(outside, "input"))
			if err != nil || string(got) != "external sentinel\n" {
				t.Fatalf("external case target changed: %q, %v", got, err)
			}
			if res.Outcome != OutcomeInvalid || !strings.Contains(res.Reason, "write case file") {
				t.Fatalf("result %+v; want invalid case write", res)
			}
		})
	}
}

// Go test replacement cannot remove external tests or create its generated
// detector in a package directory linked outside the tree after setup.
func TestGoReplayDetectorReplacementStaysInsideTheCaseTree(t *testing.T) {
	root := goCheckRepo(t, readTestdata(t, "resp_test.go.txt"))
	outside := t.TempDir()
	writeFile(t, outside, "existing_test.go", "external sentinel\n")
	_, gd, err := goDetectorOf(root, respCheck)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := goRunner(root, respCheck, gd)
	if err != nil {
		t.Fatal(err)
	}
	tree := t.TempDir()
	if err := os.Mkdir(filepath.Join(tree, "svc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(tree, "svc/resp")); err != nil {
		t.Fatal(err)
	}
	res := CaseResult{}
	if reason := runner.run(tree, Case{}, &res); reason == "" {
		t.Fatal("external package accepted")
	}
	got, err := os.ReadFile(filepath.Join(outside, "existing_test.go"))
	if err != nil || string(got) != "external sentinel\n" {
		t.Fatalf("external test removed or changed: %q, %v", got, err)
	}
	if _, err := os.Lstat(filepath.Join(outside, generatedTestName)); !os.IsNotExist(err) {
		t.Fatalf("generated detector written outside: %v", err)
	}
}
