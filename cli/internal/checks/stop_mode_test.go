package checks

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// A mode-only edit to a dirty detector changes the tree judged by Stop.
func TestStopModeOnlyDetectorChangeBlocksAgain(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable permission bits are not available on Windows")
	}
	for _, tracked := range []bool{false, true} {
		name := "untracked"
		if tracked {
			name = "tracked-dirty"
		}
		t.Run(name, func(t *testing.T) {
			root, req := stopRepo(t, map[string]string{"a": "#!/bin/sh\nexit 1\n"}, "")
			script := filepath.Join(root, ".steer/checks/a.sh")
			if err := os.Chmod(script, 0o644); err != nil {
				t.Fatal(err)
			}
			if tracked {
				gitIn(t, root, "add", ".")
				gitIn(t, root, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-qm", "baseline")
				writeFile(t, root, ".steer/checks/a.sh", "#!/bin/sh\n# dirty detector\nexit 1\n")
			}
			first := Stop(req)
			if first.Disposition != DispositionBlock || first.Run == nil {
				t.Fatalf("first Stop %+v; want a block", first)
			}
			if repeat := Stop(req); repeat.Disposition != DispositionYield {
				t.Fatalf("unchanged Stop %+v; want yield", repeat)
			}
			if err := os.Chmod(script, 0o755); err != nil {
				t.Fatal(err)
			}
			changed := Stop(req)
			if changed.Run == nil || changed.Run.Tree == first.Run.Tree {
				t.Fatalf("mode-only edit retained tree signature; before %+v, after %+v", first.Run, changed.Run)
			}
			if changed.Disposition != DispositionBlock {
				t.Fatalf("Stop after chmod %+v; want a new block", changed)
			}
			if repeat := Stop(req); repeat.Disposition != DispositionYield {
				t.Fatalf("unchanged executable Stop %+v; want yield", repeat)
			}
			if err := os.Chmod(script, 0o644); err != nil {
				t.Fatal(err)
			}
			if back := Stop(req); back.Disposition != DispositionBlock || back.Run.Tree != first.Run.Tree {
				t.Fatalf("restored mode Stop %+v; want original tree and a new block", back)
			}
		})
	}
}
