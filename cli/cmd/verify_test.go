package cmd

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestResolveVerifyMode(t *testing.T) {
	originalStaged := verifyStaged
	originalChanged := verifyChanged
	originalBase := verifyBase
	originalHead := verifyHead
	defer func() {
		verifyStaged = originalStaged
		verifyChanged = originalChanged
		verifyBase = originalBase
		verifyHead = originalHead
	}()

	tests := []struct {
		name    string
		staged  bool
		changed bool
		base    string
		head    string
		want    string
		wantErr bool
	}{
		{
			name:    "staged mode",
			staged:  true,
			changed: false,
			want:    "staged",
		},
		{
			name:    "changed mode",
			staged:  false,
			changed: true,
			base:    "abc",
			head:    "def",
			want:    "changed",
		},
		{
			name:    "missing mode",
			staged:  false,
			changed: false,
			wantErr: true,
		},
		{
			name:    "both modes set",
			staged:  true,
			changed: true,
			wantErr: true,
		},
		{
			name:    "changed missing refs",
			staged:  false,
			changed: true,
			base:    "abc",
			wantErr: true,
		},
		{
			name:    "staged rejects refs",
			staged:  true,
			changed: false,
			base:    "abc",
			head:    "def",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			verifyStaged = tc.staged
			verifyChanged = tc.changed
			verifyBase = tc.base
			verifyHead = tc.head

			got, err := resolveVerifyMode()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got mode=%q", got)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("mode mismatch: got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestVerificationScopes(t *testing.T) {
	got := verificationScopes("project", true)
	want := []string{"project", "global"}
	if !slices.Equal(got, want) {
		t.Fatalf("scope mismatch: got %v, want %v", got, want)
	}

	got = verificationScopes("global", true)
	want = []string{"global"}
	if !slices.Equal(got, want) {
		t.Fatalf("scope mismatch: got %v, want %v", got, want)
	}
}

// TestVerifyCountsAMasterDenyAsABlock runs verification against a master that
// answers the way the generated master does: a PreToolUse deny.
func TestVerifyCountsAMasterDenyAsABlock(t *testing.T) {
	master := filepath.Join(t.TempDir(), "steer-hooks.py")
	script := `import json, sys
sys.stdin.read()
print(json.dumps({"hookSpecificOutput": {"hookEventName": "PreToolUse", "permissionDecision": "deny", "permissionDecisionReason": "no f-string SQL"}}))
`
	if err := os.WriteFile(master, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	result := runMasterHookVerification(master, verifyTarget{Path: "q.py", Content: "x = 1\n"})
	if result.Err != nil {
		t.Fatalf("verification failed: %v", result.Err)
	}
	if !result.Blocked || result.Reason != "no f-string SQL" {
		t.Fatalf("blocked=%v reason=%q, want a block with the master's reason", result.Blocked, result.Reason)
	}
}

// verifyPathNames are names a line-parsed or C-quoted name list misreads.
var verifyPathNames = []string{
	"café.py",
	"日本語.py",
	" leading.py",
	"trailing.py ",
	"new\nline.py",
	`quo"te.py`,
	"dir é/nested.py",
}

// writeVerifyPaths writes each name in dir with its own name as its content.
func writeVerifyPaths(t *testing.T, dir string) {
	t.Helper()
	for _, name := range verifyPathNames {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// assertVerifyTargets requires one target per name, each carrying its own content.
func assertVerifyTargets(t *testing.T, targets []verifyTarget, want map[string]string) {
	t.Helper()
	got := map[string]string{}
	for _, target := range targets {
		got[target.Path] = target.Content
	}
	if len(targets) != len(want) || !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %q, want %q", got, want)
	}
}

// Both modes name every path as git stores it, and read its content by that name.
func TestVerifyTargetsKeepPathsVerbatim(t *testing.T) {
	want := map[string]string{}
	for _, name := range verifyPathNames {
		want[name] = name
	}

	t.Run("staged", func(t *testing.T) {
		repo := hooksScratchRepo(t)
		t.Chdir(repo)
		writeVerifyPaths(t, repo)
		gitIn(t, repo, "add", "-A")

		targets, err := loadStagedTargets()
		if err != nil {
			t.Fatal(err)
		}
		assertVerifyTargets(t, targets, want)
	})

	t.Run("changed", func(t *testing.T) {
		repo := hooksScratchRepo(t)
		t.Chdir(repo)
		gitIn(t, repo, "commit", "-q", "--allow-empty", "-m", "base")
		writeVerifyPaths(t, repo)
		gitIn(t, repo, "add", "-A")
		gitIn(t, repo, "commit", "-q", "-m", "head")

		targets, err := loadChangedTargets("HEAD~1", "HEAD")
		if err != nil {
			t.Fatal(err)
		}
		assertVerifyTargets(t, targets, want)
	})
}

// A rename is verified under its destination alone, including a non-ASCII one.
func TestVerifyTargetsNameARenameByItsDestination(t *testing.T) {
	const content = "one\ntwo\nthree\nfour\nfive\n"
	want := map[string]string{"renamé.py": content}

	setup := func(t *testing.T) string {
		repo := hooksScratchRepo(t)
		t.Chdir(repo)
		if err := os.WriteFile(filepath.Join(repo, "before.py"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		gitIn(t, repo, "add", "-A")
		gitIn(t, repo, "commit", "-q", "-m", "base")
		gitIn(t, repo, "mv", "before.py", "renamé.py")
		return repo
	}

	t.Run("staged", func(t *testing.T) {
		setup(t)
		targets, err := loadStagedTargets()
		if err != nil {
			t.Fatal(err)
		}
		assertVerifyTargets(t, targets, want)
	})

	t.Run("changed", func(t *testing.T) {
		repo := setup(t)
		gitIn(t, repo, "commit", "-q", "-m", "head")
		targets, err := loadChangedTargets("HEAD~1", "HEAD")
		if err != nil {
			t.Fatal(err)
		}
		assertVerifyTargets(t, targets, want)
	})
}

// A name that is not UTF-8 is refused, since JSON would show a guard another name.
// It enters through the index because some filesystems cannot hold it.
func TestVerifyTargetsRefuseAPathThatIsNotUTF8(t *testing.T) {
	setup := func(t *testing.T) string {
		repo := hooksScratchRepo(t)
		t.Chdir(repo)
		blob := filepath.Join(repo, "blob")
		if err := os.WriteFile(blob, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		hash := strings.TrimSpace(gitIn(t, repo, "hash-object", "-w", blob))
		os.Remove(blob)
		gitIn(t, repo, "update-index", "--add", "--cacheinfo", "100644,"+hash+",bad\xff.py")
		return repo
	}
	assertRefused := func(t *testing.T, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), "not valid UTF-8") {
			t.Fatalf("err = %v, want a UTF-8 refusal", err)
		}
	}

	t.Run("staged", func(t *testing.T) {
		setup(t)
		_, err := loadStagedTargets()
		assertRefused(t, err)
	})

	t.Run("changed", func(t *testing.T) {
		repo := setup(t)
		gitIn(t, repo, "commit", "-q", "-m", "head")
		_, err := loadChangedTargets("HEAD~1", "HEAD")
		assertRefused(t, err)
	})
}
