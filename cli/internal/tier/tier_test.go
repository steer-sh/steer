package tier

import "testing"

// Both ladder spellings are live and both reach a stored record, so both read.
func TestBothSpellingsNameTheSameRung(t *testing.T) {
	pairs := []struct {
		a, b string
	}{
		{"steer", "guard"},
		{"prose", "instruction"},
		{"prompt", "review"},
		{"check", "check"},
	}
	for _, p := range pairs {
		ka, oka := Of(p.a)
		kb, okb := Of(p.b)
		if !oka || !okb || ka != kb {
			t.Errorf("%q and %q read as %q/%q", p.a, p.b, ka, kb)
		}
		if Name(ka) == "" {
			t.Errorf("rung %q has no name", ka)
		}
	}
}

// A value no spelling names is refused, and the empty value names no rung.
func TestAnUnknownTierIsRefused(t *testing.T) {
	for _, v := range []string{"", "none", "hook", "rule", "future-tier"} {
		if Valid(v) {
			t.Errorf("tier %q was accepted", v)
		}
	}
	if len(Accepted()) != len(spellings) {
		t.Errorf("Accepted lists %d of %d spellings", len(Accepted()), len(spellings))
	}
}

// The check rung is the one bound to a repository, and nothing else reads as it.
func TestIsCheckNamesTheRepositoryRung(t *testing.T) {
	if !IsCheck("check") || !IsCheck(" CHECK ") {
		t.Error("the check rung is not read as a check")
	}
	for _, v := range []string{"steer", "guard", "prose", "prompt", "", "checks"} {
		if IsCheck(v) {
			t.Errorf("tier %q is read as a check", v)
		}
	}
}

// The guard rung is the one whose enforcement is per harness, under either name.
func TestIsGuardReadsBothSpellings(t *testing.T) {
	for _, v := range []string{"steer", "guard", " GUARD "} {
		if !IsGuard(v) {
			t.Errorf("tier %q is not read as a guard", v)
		}
	}
	for _, v := range []string{"check", "prose", "prompt", "", "guardrail"} {
		if IsGuard(v) {
			t.Errorf("tier %q is read as a guard", v)
		}
	}
}
