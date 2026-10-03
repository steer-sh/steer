package checks

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

// generatedTestName is the file a go-test replay writes the detector into.
const generatedTestName = "steer_detector_test.go"

// goReplayEnv keeps replay off the network and on the installed toolchain, so
// a missing module or toolchain fails at once instead of downloading.
var goReplayEnv = []string{"GOPROXY=off", "GOTOOLCHAIN=local", "GOWORK=off", "GOFLAGS="}

// goEvent is one line of `go test -json`.
type goEvent struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
	Output  string `json:"Output"`
}

// goRunner replays a go-test binding: the tree holds the case's files, the
// declared inputs and one generated test file with the tracked declarations,
// and the package's other test files are removed after setup.
func goRunner(root string, d Declaration, gd goDetector) (caseRunner, error) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		return caseRunner{}, fmt.Errorf("replay needs the go toolchain: %w", err)
	}
	inputs := map[string]bool{}
	for _, p := range d.Inputs {
		inputs[p] = true
	}
	src := gd.source()
	return caseRunner{
		detector: d.Inputs,
		protects: func(p string) string {
			if inputs[p] {
				return fmt.Sprintf("the case writes detector file %s", p)
			}
			if path.Dir(p) == d.Package && strings.HasSuffix(p, "_test.go") {
				return fmt.Sprintf("the case writes test file %s into the detector's package", p)
			}
			return ""
		},
		install: func(tree string) error {
			return copyDetectorFiles(root, tree, inputs)
		},
		run: func(tree string, c Case, res *CaseResult) string {
			caseTree, err := os.OpenRoot(tree)
			if err != nil {
				return err.Error()
			}
			defer caseTree.Close()
			pkg := filepath.FromSlash(d.Package)
			pkgDir := filepath.Join(tree, pkg)
			if err := removeTestFiles(caseTree, pkg); err != nil {
				return err.Error()
			}
			if err := caseTree.MkdirAll(pkg, 0o755); err != nil {
				return err.Error()
			}
			if err := caseTree.WriteFile(filepath.Join(pkg, generatedTestName), src, 0o644); err != nil {
				return err.Error()
			}
			_, out, timedOut, err := runCommand(pkgDir, goReplayEnv, goBin,
				"test", "-count=1", "-json", "-run", "^"+d.Test+"$", ".")
			if timedOut {
				return fmt.Sprintf("go test exceeded %s", CaseTimeout)
			}
			if err != nil {
				return fmt.Sprintf("go test could not run: %v", err)
			}
			action, testOut := namedTestResult(out, d.Test)
			sum := sha256.Sum256(out)
			res.OutputDigest = hex.EncodeToString(sum[:])
			switch action {
			case "pass", "fail":
				code := 0
				if action == "fail" {
					code = 1
				}
				res.ObservedExit = &code
				res.Excerpt = excerpt(testOut)
				judge(res, code, testOut, c.Diagnostic)
				return ""
			case "skip":
				res.Excerpt = excerpt(testOut)
				return fmt.Sprintf("the named test %s skipped", d.Test)
			default:
				other := nonTestOutput(out)
				res.Excerpt = excerpt(other)
				if offlineRefusal(other) {
					res.Cause = CauseEnvironment
					return fmt.Sprintf("the named test %s did not run: this host lacks a module or toolchain the case needs", d.Test)
				}
				res.Cause = CauseIncompatible
				return fmt.Sprintf("the named test %s did not run: the case does not build with the current detector", d.Test)
			}
		},
	}, nil
}

func removeTestFiles(tree *os.Root, dir string) error {
	f, err := tree.Open(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	entries, err := f.ReadDir(-1)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), "_test.go") {
			if err := tree.Remove(filepath.Join(dir, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// namedTestResult returns the final action of the named top-level test and the
// output it and its subtests printed. An empty action means it did not run.
func namedTestResult(out []byte, name string) (string, []byte) {
	var action string
	var testOut bytes.Buffer
	scanner := bufio.NewScanner(bytes.NewReader(out))
	scanner.Buffer(make([]byte, 64*1024), outputCap)
	for scanner.Scan() {
		var ev goEvent
		if json.Unmarshal(scanner.Bytes(), &ev) != nil {
			continue
		}
		if ev.Test != name && !strings.HasPrefix(ev.Test, name+"/") {
			continue
		}
		switch {
		case ev.Action == "output":
			testOut.WriteString(ev.Output)
		case ev.Test == name && (ev.Action == "pass" || ev.Action == "fail" || ev.Action == "skip"):
			action = ev.Action
		}
	}
	return action, testOut.Bytes()
}

// offlineRefusal reports go output that names a restriction of goReplayEnv: a
// module the cache lacks or a toolchain other than the installed one.
func offlineRefusal(out []byte) bool {
	return bytes.Contains(out, []byte("GOPROXY=off")) || bytes.Contains(out, []byte("GOTOOLCHAIN=local"))
}

// nonTestOutput keeps what explains a test that never ran: build output and
// package-level output, from JSON events or plain lines.
func nonTestOutput(out []byte) []byte {
	var b bytes.Buffer
	scanner := bufio.NewScanner(bytes.NewReader(out))
	scanner.Buffer(make([]byte, 64*1024), outputCap)
	for scanner.Scan() {
		var ev goEvent
		if json.Unmarshal(scanner.Bytes(), &ev) != nil {
			b.Write(scanner.Bytes())
			b.WriteByte('\n')
			continue
		}
		if ev.Test == "" && ev.Output != "" {
			b.WriteString(ev.Output)
		}
	}
	return b.Bytes()
}
