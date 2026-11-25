package pi

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// TestCodemodeOnlyNamesTheFileThatHidesBash checks that the reader names the file
// whose "only" mode is in effect, with a trusted cwd's project settings over the
// agent directory's and an untrusted cwd's ignored.
func TestCodemodeOnlyNamesTheFileThatHidesBash(t *testing.T) {
	for _, tc := range []struct {
		name, global, project, want string
		untrusted                   bool
	}{
		{"no settings", "", "", "", false},
		{"global only", `{"codemode":{"mode":"only"}}`, "", "global", false},
		{"global on", `{"codemode":{"mode":"on"}}`, "", "", false},
		{"project sets only", `{"codemode":{"mode":"on"}}`, `{"codemode":{"mode":"only"}}`, "project", false},
		{"project restores on", `{"codemode":{"mode":"only"}}`, `{"codemode":{"mode":"on"}}`, "", false},
		{"project silent", `{"codemode":{"mode":"only"}}`, `{"theme":"dark"}`, "global", false},
		{"byte-order mark", "\uFEFF" + `{"codemode":{"mode":"only"}}`, "", "global", false},
		{"unreadable project", `{"codemode":{"mode":"only"}}`, `{`, "global", false},
		{"untrusted project ignored", `{"codemode":{"mode":"only"}}`, `{"codemode":{"mode":"on"}}`, "global", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := isolateHome(t)
			project := t.TempDir()
			paths := map[string]string{
				"global":  filepath.Join(home, "pi-home", "settings.json"),
				"project": filepath.Join(project, ".pi", "settings.json"),
			}
			for which, body := range map[string]string{"global": tc.global, "project": tc.project} {
				if body == "" {
					continue
				}
				if err := os.MkdirAll(filepath.Dir(paths[which]), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(paths[which], []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			resolved, err := filepath.EvalSymlinks(project)
			if err != nil {
				t.Fatal(err)
			}
			trust := `{` + strconv.Quote(resolved) + `:` + strconv.FormatBool(!tc.untrusted) + `}`
			if err := os.MkdirAll(filepath.Join(home, "pi-home"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, "pi-home", "trust.json"), []byte(trust), 0o644); err != nil {
				t.Fatal(err)
			}
			got := CodemodeOnly(project)
			if want := paths[tc.want]; got != want {
				t.Fatalf("CodemodeOnly = %q, want %q", got, want)
			}
		})
	}
}
