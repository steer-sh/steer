package gate

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func binaryStanza(path, mode string) string {
	return "diff --git a/" + path + " b/" + path + "\n" +
		"index 7fb54f6..8a7524e " + mode + "\n" +
		"Binary files a/" + path + " and b/" + path + " differ\n"
}

func TestIsBinaryOnlyAcceptsOnlyContentChangesToExistingBinaries(t *testing.T) {
	two := binaryStanza("bin/a/steer", "100755") + binaryStanza("bin/b/steer", "100755")
	cases := []struct {
		name  string
		raw   string
		paths []string
		want  bool
	}{
		{"two rebuilt executables", two, []string{"bin/a/steer", "bin/b/steer"}, true},
		{"a path with spaces", binaryStanza("img/a b.png", "100644"), []string{"img/a b.png"}, true},
		{"a changed path the diff omits", binaryStanza("bin/a/steer", "100755"), []string{"bin/a/steer", "go.sum"}, false},
		{"a stanza for an unlisted path", two, []string{"bin/a/steer"}, false},
		{"the same path twice", binaryStanza("x.bin", "100644") + binaryStanza("x.bin", "100644"), []string{"x.bin"}, false},
		{"a symlink mode", binaryStanza("x.bin", "120000"), []string{"x.bin"}, false},
		{"a mode change", "diff --git a/x.bin b/x.bin\nold mode 100644\nnew mode 100755\n" +
			"index 7fb54f6..8a7524e\nBinary files a/x.bin and b/x.bin differ\n", []string{"x.bin"}, false},
		{"an added binary", "diff --git a/x.bin b/x.bin\nnew file mode 100644\n" +
			"index 0000000..8a7524e\nBinary files /dev/null and b/x.bin differ\n", []string{"x.bin"}, false},
		{"an all-zero object id", "diff --git a/x.bin b/x.bin\nindex 0000000..8a7524e 100644\n" +
			"Binary files a/x.bin and b/x.bin differ\n", []string{"x.bin"}, false},
		{"a renamed binary", "diff --git a/x.bin b/y.bin\nsimilarity index 100%\n" +
			"rename from x.bin\nrename to y.bin\n", []string{"y.bin"}, false},
		{"a text hunk", "diff --git a/x.bin b/x.bin\nindex 7fb54f6..8a7524e 100644\n" +
			"--- a/x.bin\n+++ b/x.bin\n@@ -1 +1 @@\n-a\n+b\n", []string{"x.bin"}, false},
		{"a quoted path", "diff --git \"a/\\303\\251.bin\" \"b/\\303\\251.bin\"\nindex 7fb54f6..8a7524e 100644\n" +
			"Binary files \"a/\\303\\251.bin\" and \"b/\\303\\251.bin\" differ\n", []string{"é.bin"}, false},
		{"no trailing newline", strings.TrimSuffix(binaryStanza("x.bin", "100644"), "\n"), []string{"x.bin"}, false},
		{"an empty diff", "", []string{"x.bin"}, false},
	}
	for _, tc := range cases {
		if got := IsBinaryOnly(tc.raw, tc.paths); got != tc.want {
			t.Errorf("%s: IsBinaryOnly = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// binaryRepo commits blob.bin (binary content) and app.py, then stages a new build of
// blob.bin, so the staged change set is one binary content change.
func binaryRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("GIT_EXTERNAL_DIFF", "")
	os.Unsetenv("GIT_EXTERNAL_DIFF")
	repo := buildRepo(t, map[string]string{"blob.bin": "v1\x00\x01\x02", "app.py": lines(40)})
	gitCmd(t, repo, "commit", "-q", "-m", "init")
	writeFileAt(t, repo, "blob.bin", "v2\x00\x03\x04"+lines(40))
	gitCmd(t, repo, "add", "blob.bin")
	return repo
}

func handleStaged(t *testing.T, repo string, reviewer *stubReviewer) (Decision, Config) {
	t.Helper()
	cfg := Defaults()
	cfg.StateDir = t.TempDir()
	d := NewEngine(cfg, reviewer).Handle(context.Background(), Request{
		Repo: repo, Actor: "s", Channel: ChannelPreToolUse, SessionID: "s",
		Spec: DiffSpec{StagedOnly: true}, Message: "chore(bin): rebuild",
	})
	return d, cfg
}

func TestBinaryContentChangeSkipsReview(t *testing.T) {
	repo := binaryRepo(t)
	reviewer := &stubReviewer{replies: []RawRun{findingReply(oneFinding)}}
	d, cfg := handleStaged(t, repo, reviewer)
	if reviewer.calls != 0 {
		t.Fatalf("reviewer ran %d times on a binary-only change", reviewer.calls)
	}
	if d.Kind != ContextOnly || !strings.Contains(d.SystemMessage, "binary") {
		t.Fatalf("decision = %+v, want a ContextOnly receipt naming the binary skip", d)
	}
	raw, err := os.ReadFile(filepath.Join(cfg.StateDir, filepath.Base(cfg.LogFile())))
	if err != nil || !strings.Contains(string(raw), `"phase":"skip-binary"`) {
		t.Fatalf("log = %s (%v), want a skip-binary row", raw, err)
	}
}

func TestBinaryChangeWithAnyOtherChangeIsReviewed(t *testing.T) {
	cases := map[string]func(t *testing.T, repo string){
		"a code edit": func(t *testing.T, repo string) {
			writeFileAt(t, repo, "app.py", lines(41))
			gitCmd(t, repo, "add", "app.py")
		},
		"an added binary": func(t *testing.T, repo string) {
			writeFileAt(t, repo, "new.bin", "\x00\x01")
			gitCmd(t, repo, "add", "new.bin")
		},
		"a mode change": func(t *testing.T, repo string) {
			gitCmd(t, repo, "update-index", "--chmod=+x", "blob.bin")
		},
		"a textconv driver": func(t *testing.T, repo string) {
			writeFileAt(t, repo, ".git/info/attributes", "*.bin diff=dump\n")
			gitCmd(t, repo, "config", "diff.dump.textconv", "od -c")
		},
		// The driver prints exactly the stanza the classifier accepts, so only the
		// configuration check keeps this change set in review.
		"an external diff driver that mimics a binary stanza": func(t *testing.T, repo string) {
			driver := filepath.Join(repo, ".git", "mimic.sh")
			script := "#!/bin/sh\nprintf 'diff --git a/%s b/%s\\nindex 1111111..2222222 100644\\n" +
				"Binary files a/%s and b/%s differ\\n' \"$1\" \"$1\" \"$1\" \"$1\"\n"
			if err := os.WriteFile(driver, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			gitCmd(t, repo, "config", "diff.external", driver)
		},
	}
	if runtime.GOOS == "windows" {
		t.Skip("the external driver is a shell script")
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			repo := binaryRepo(t)
			change(t, repo)
			reviewer := &stubReviewer{replies: []RawRun{findingReply("NO FINDINGS")}}
			d, cfg := handleStaged(t, repo, reviewer)
			raw, _ := os.ReadFile(filepath.Join(cfg.StateDir, filepath.Base(cfg.LogFile())))
			if strings.Contains(string(raw), `"phase":"skip-binary"`) || strings.Contains(d.SystemMessage, "binary") {
				t.Fatalf("%s beside a binary rebuild took the binary skip: %s", name, raw)
			}
		})
	}
}

func TestUnrenderedUntrackedFileMarksTheDiffIncomplete(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a file the current user cannot read")
	}
	repo := binaryRepo(t)
	writeFileAt(t, repo, "secret.bin", "\x00")
	full := filepath.Join(repo, "secret.bin")
	if err := os.Chmod(full, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(full, 0o644) })
	got, ok := GetDiffSized(Defaults(), repo, DiffSpec{IncludeUntracked: true})
	if !ok || got.Complete {
		t.Fatalf("GetDiffSized ok=%v Complete=%v, want an incomplete diff", ok, got.Complete)
	}
	clean, _ := GetDiffSized(Defaults(), binaryRepo(t), DiffSpec{StagedOnly: true})
	if !clean.Complete {
		t.Fatal("a diff with every file rendered must be complete")
	}
}

// A retry whose fixes leave only the binary rebuild is skipped like a first pass.
func TestRetryReducedToABinaryChangeSkipsReview(t *testing.T) {
	repo := binaryRepo(t)
	writeFileAt(t, repo, "app.py", lines(240))
	gitCmd(t, repo, "add", "app.py")
	cfg := Defaults()
	cfg.StateDir = t.TempDir()
	reviewer := &stubReviewer{replies: []RawRun{findingReply(oneFinding)}}
	engine := NewEngine(cfg, reviewer)
	req := Request{Repo: repo, Actor: "s", Channel: ChannelPreToolUse, SessionID: "s",
		Spec: DiffSpec{StagedOnly: true}, Message: "chore(bin): rebuild"}
	if d := engine.Handle(context.Background(), req); d.Kind != Deny {
		t.Fatalf("first pass = %v, want Deny", d.Kind)
	}
	reviewed := reviewer.calls
	gitCmd(t, repo, "reset", "-q", "app.py")
	d := engine.Handle(context.Background(), req)
	if reviewer.calls != reviewed || d.Kind != ContextOnly || !strings.Contains(d.SystemMessage, "binary") {
		t.Fatalf("retry: calls=%d decision=%+v, want the binary skip", reviewer.calls, d)
	}
}
