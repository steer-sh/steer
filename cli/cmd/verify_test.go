package cmd

import (
	"os"
	"path/filepath"
	"slices"
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
