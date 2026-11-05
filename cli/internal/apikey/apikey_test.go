//go:build !windows

package apikey

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/testhome"
)

func newHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	testhome.Set(t, home)
	return home
}

func TestWriteMakesAPrivateFileThatFindReads(t *testing.T) {
	home := newHome(t)
	path, err := Write("sk-test-123\n")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".steer", "credentials", "reviewer-api-key"); path != want {
		t.Fatalf("Write path = %s, want %s", path, want)
	}
	info, _ := os.Stat(path)
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("key file mode = %04o, want 0600", mode)
	}
	dirInfo, _ := os.Stat(filepath.Dir(path))
	if mode := dirInfo.Mode().Perm(); mode != 0o700 {
		t.Errorf("credentials dir mode = %04o, want 0700", mode)
	}
	key, st := Find("")
	if key != "sk-test-123" || st.Source != SourceFile {
		t.Fatalf("Find = %q, %+v; want the stored key from the file", key, st)
	}
}

func TestWriteNarrowsAnExistingWideDirectory(t *testing.T) {
	home := newHome(t)
	dir := filepath.Join(home, ".steer", "credentials")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Write("sk-test"); err != nil {
		t.Fatal(err)
	}
	if key, st := Find(""); key != "sk-test" {
		t.Fatalf("Find after Write into a 0755 dir = %q, %+v", key, st)
	}
}

func TestFindPrefersANonEmptyVariable(t *testing.T) {
	newHome(t)
	if _, err := Write("from-file"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STEER_TEST_KEY", "from-env")
	if key, st := Find("STEER_TEST_KEY"); key != "from-env" || st.Label() != "env STEER_TEST_KEY" {
		t.Fatalf("Find with the variable set = %q, %s", key, st.Label())
	}
	t.Setenv("STEER_TEST_KEY", "  ")
	if key, st := Find("STEER_TEST_KEY"); key != "from-file" || st.Source != SourceFile {
		t.Fatalf("Find with a blank variable = %q, %s; want the file", key, st.Label())
	}
}

func TestFindRefusesAnUnsafeKeyFile(t *testing.T) {
	cases := map[string]func(t *testing.T, path string){
		"group-readable file": func(t *testing.T, path string) {
			if err := os.Chmod(path, 0o640); err != nil {
				t.Fatal(err)
			}
		},
		"world-readable directory": func(t *testing.T, path string) {
			if err := os.Chmod(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
		},
		"symlink": func(t *testing.T, path string) {
			target := filepath.Join(t.TempDir(), "elsewhere")
			if err := os.WriteFile(target, []byte("sk-linked"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, spoil := range cases {
		t.Run(name, func(t *testing.T) {
			newHome(t)
			path, err := Write("sk-test")
			if err != nil {
				t.Fatal(err)
			}
			spoil(t, path)
			key, st := Find("")
			if key != "" || st.Source != SourceMissing {
				t.Fatalf("Find = %q, %+v; want the file refused", key, st)
			}
			if !strings.Contains(st.Problem, path) && !strings.Contains(st.Problem, filepath.Dir(path)) {
				t.Errorf("Problem %q does not name the path", st.Problem)
			}
			if !strings.Contains(st.Missing(), st.Problem) {
				t.Errorf("Missing() %q drops the refusal", st.Missing())
			}
		})
	}
}

func TestMissingNamesWhatWasLookedFor(t *testing.T) {
	newHome(t)
	t.Setenv("STEER_TEST_KEY", "")
	key, st := Find("STEER_TEST_KEY")
	if key != "" || st.Label() != "missing" {
		t.Fatalf("Find = %q, %s; want missing", key, st.Label())
	}
	msg := st.Missing()
	for _, want := range []string{"$STEER_TEST_KEY", st.Path, SetCommand} {
		if !strings.Contains(msg, want) {
			t.Errorf("Missing() = %q, want it to name %q", msg, want)
		}
	}
}

func TestWriteRefusesAMultiLineKey(t *testing.T) {
	newHome(t)
	if _, err := Write("one\ntwo"); err == nil {
		t.Fatal("Write accepted a key with a line break")
	}
	if _, removed, _ := Remove(); removed {
		t.Fatal("a refused Write left a key file")
	}
}

func TestRemoveDeletesTheFile(t *testing.T) {
	newHome(t)
	if _, err := Write("sk-test"); err != nil {
		t.Fatal(err)
	}
	if _, removed, err := Remove(); err != nil || !removed {
		t.Fatalf("Remove = %v, %v", removed, err)
	}
	if key, _ := Find(""); key != "" {
		t.Fatalf("Find after Remove = %q", key)
	}
}

// A symlinked credentials directory would carry the key wherever it points, so
// Write refuses it before it writes anything there.
func TestWriteRefusesASymlinkedCredentialsDirectory(t *testing.T) {
	home := newHome(t)
	elsewhere := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".steer"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(home, ".steer", "credentials")); err != nil {
		t.Fatal(err)
	}
	if _, err := Write("sk-test"); err == nil {
		t.Fatal("Write stored the key through a symlinked directory")
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Errorf("Write left %d file(s) at the symlink's target", len(entries))
	}
}

// A relative HOME would resolve the key file inside the working directory.
func TestARelativeHomeHasNoKeyFile(t *testing.T) {
	testhome.Set(t, ".")
	if _, err := Write("sk-test"); err == nil {
		t.Fatal("Write accepted a relative home")
	}
}
