package reviewers

import "testing"

// Every provider Resolve builds must name an executable, or an install reports a
// reviewer as unknown when it could have said "missing" and named the fix.
func TestEveryResolvableProviderNamesAnExecutable(t *testing.T) {
	for _, provider := range []string{"", "agy", "codex", "claude", "claude-code"} {
		if _, err := Resolve(provider, "", ""); err != nil {
			t.Fatalf("Resolve(%q) = %v", provider, err)
		}
		if got := ProbeProvider(provider).State; got == ProbeUnknown {
			t.Errorf("ProbeProvider(%q) = unknown; want a checked state", provider)
		}
	}
}

func TestUnknownProviderIsNotAClaimOfAbsence(t *testing.T) {
	p := ProbeProvider("nothing-like-this")
	if p.State != ProbeUnknown || !p.Available() {
		t.Errorf("ProbeProvider(unknown) = %+v, want unknown and available", p)
	}
}
