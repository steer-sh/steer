package reviewers

import "testing"

// Every provider Resolve builds must name an executable, or an install reports a
// reviewer as unknown when it could have said "missing" and named the fix.
func TestEveryResolvableProviderNamesAnExecutable(t *testing.T) {
	for _, provider := range []string{"", "agy", "codex", "claude", "claude-code"} {
		if _, err := Resolve(provider, Options{}); err != nil {
			t.Fatalf("Resolve(%q) = %v", provider, err)
		}
		if got := ProbeProvider(provider).State; got == ProbeUnknown {
			t.Errorf("ProbeProvider(%q) = unknown; want a checked state", provider)
		}
	}
}

// An API provider names no executable, so its probe must read the configuration;
// an unknown reading would report a reviewer with no URL as able to run.
func TestEveryProviderProbesToACheckedState(t *testing.T) {
	for _, p := range Providers() {
		if got := ProbeConfig(p.ID, Options{}).State; got == ProbeUnknown {
			t.Errorf("ProbeConfig(%q) = unknown; want a checked state", p.ID)
		}
	}
}

func TestUnknownProviderIsNotAClaimOfAbsence(t *testing.T) {
	p := ProbeProvider("nothing-like-this")
	if p.State != ProbeUnknown || !p.Available() {
		t.Errorf("ProbeProvider(unknown) = %+v, want unknown and available", p)
	}
}
