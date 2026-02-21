package cmd

import (
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
