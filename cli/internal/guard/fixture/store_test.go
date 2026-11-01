package fixture

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func oneCase(hook string) File {
	return File{Version: Version, Hook: hook, Cases: []Case{{
		Name:    "case",
		Expect:  ExpectAllow,
		Origin:  OriginSynthetic,
		Payload: map[string]any{"tool_name": "Bash"},
	}}}
}

// A hook outside the guard-name grammar is refused by name, and Save writes nothing.
func TestSaveRefusesAHookThatIsNotAGuardName(t *testing.T) {
	for _, hook := range []string{"../x", "a/b", `a\b`, "..", ".hidden", "-flag", "two words"} {
		t.Run(hook, func(t *testing.T) {
			root := t.TempDir()
			store := filepath.Join(root, "store")
			err := Save(store, oneCase(hook))
			if err == nil {
				t.Fatalf("Save accepted hook %q", hook)
			}
			if !strings.Contains(err.Error(), fmt.Sprintf("%q", hook)) {
				t.Errorf("error %q does not name %q", err, hook)
			}
			entries, err := os.ReadDir(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Errorf("Save left %v behind", entries)
			}
		})
	}
}

// pins existing behaviour
// Every name the installer accepts as a guard id stays storable and loadable.
func TestRealGuardNamesRoundTrip(t *testing.T) {
	for _, hook := range []string{"alpha", "no-direct-main-hook", "v1.2_guard", "foo..bar", "steer-hooks"} {
		t.Run(hook, func(t *testing.T) {
			store := t.TempDir()
			if err := Save(store, oneCase(hook)); err != nil {
				t.Fatal(err)
			}
			if _, found, err := Load(store, hook); err != nil || !found {
				t.Fatalf("Load = found %v, err %v", found, err)
			}
		})
	}
}

// A document under one guard's name that claims another guard is refused: the
// master pass would credit or blame the wrong objector.
func TestLoadRefusesADocumentNamingAnotherHook(t *testing.T) {
	store := t.TempDir()
	if err := Save(store, oneCase("beta")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(Path(store, "beta"), Path(store, "alpha")); err != nil {
		t.Fatal(err)
	}
	_, _, err := Load(store, "alpha")
	if err == nil {
		t.Fatal("Load accepted alpha.json naming hook beta")
	}
	for _, want := range []string{"alpha", "beta"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// A case file path must name a file inside the case directory. Dots are refused
// only as a whole path element, so two keys cannot reach one file through "..".
func TestCaseFilePaths(t *testing.T) {
	cases := []struct {
		path string
		ok   bool
	}{
		{"foo..bar.go", true},
		{"dir/..hidden/a.go", true},
		{"dir/a.go", true},
		{"", false},
		{".", false},
		{"./", false},
		{"dir/..", false},
		{"..", false},
		{"../x", false},
		{"dir/../../x", false},
		{"dir/../x", false},
		{"/etc/passwd", false},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			file := oneCase("alpha")
			file.Cases[0].Files = map[string]string{c.path: "content"}
			err := file.Validate()
			if c.ok && err != nil {
				t.Fatalf("refused %q: %v", c.path, err)
			}
			if !c.ok && (err == nil || !strings.Contains(err.Error(), `"`+c.path+`"`)) {
				t.Fatalf("path %q: err = %v, want a refusal naming it", c.path, err)
			}
		})
	}
}
