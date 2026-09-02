package cc

import (
	"reflect"
	"testing"
)

func TestStagedPathspecs(t *testing.T) {
	cases := []struct {
		cmd  string
		want []string
		why  string
	}{
		{"git add cli/cmd && git commit -F -", []string{"cli/cmd"},
			"the reported defect: one subtree staged, the whole repo reviewed"},
		{"git add a.go b.go && git commit -m x", []string{"a.go", "b.go"}, "every named path"},
		{"git add -- -weird.go && git commit -m x", []string{"-weird.go"},
			"past `--` a leading dash is a path, not an option"},
		{"git add 'my file.go' && git commit -m x", []string{"my file.go"},
			"a quoted path holding a space is one token"},
		{"git add cli && git add docs && git commit -m x", []string{"cli", "docs"},
			"two staging steps both name paths"},
		{"git add . && git commit -m x", []string{"."}, "`.` is a pathspec git resolves itself"},
		{"git add -A && git commit -m x", nil, "no pathspec means the whole repo"},
		{"git add && git commit -m x", nil, "no pathspec means the whole repo"},
		{"git add -A cli && git commit -m x", []string{"cli"}, "-A still obeys a pathspec"},
		{"git add $FILES && git commit -m x", nil, "a variable expands to text this does not have"},
		{"git add \"$(ls)\" && git commit -m x", nil, "a substitution expands to text this does not have"},
		{"git add --pathspec-from-file=list && git commit -m x", nil,
			"the paths live in a file, so the command text does not name them"},
		{"git -C /other add cli && git commit -m x", nil,
			"the paths resolve against a directory the plan does not read"},
		{"git commit -am x", nil, "no staging step at all"},
		{"git add cli; git commit -m x", []string{"cli"}, "`;` separates, it does not join"},
	}
	for _, tc := range cases {
		got := StagedPathspecs(tc.cmd)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("StagedPathspecs(%q) = %v, want %v — %s", tc.cmd, got, tc.want, tc.why)
		}
	}
}

func TestDiffSpecForCarriesPathspecsOnlyWithUntracked(t *testing.T) {
	spec := DiffSpecFor("git add cli/cmd && git commit -F -")
	if spec.StagedOnly || !spec.IncludeUntracked {
		t.Fatalf("a staging step reads the working tree and opts into untracked files: %+v", spec)
	}
	if !reflect.DeepEqual(spec.UntrackedPathspecs, []string{"cli/cmd"}) {
		t.Errorf("UntrackedPathspecs = %v", spec.UntrackedPathspecs)
	}
	// -a stages tracked files only, so there is no untracked half to narrow.
	if spec := DiffSpecFor("git commit -am x"); spec.IncludeUntracked || spec.UntrackedPathspecs != nil {
		t.Errorf("commit -a: %+v", spec)
	}
}
