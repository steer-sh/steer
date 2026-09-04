package shellsyntax

import (
	"testing"

	"mvdan.cc/sh/v3/syntax"
)

func TestParseCallsAcrossShellStructure(t *testing.T) {
	p, err := Parse(`echo "git commit"; (cd /repo && git -c "x;y" commit -m "real message"); out=$(go test ./...)`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"echo", "cd", "git", "go"}
	if len(p.Calls) != len(want) {
		t.Fatalf("calls = %#v", p.Calls)
	}
	for i, verb := range want {
		if got := p.Calls[i].Args[0].Value; got != verb {
			t.Errorf("call %d verb = %q, want %q", i, got, verb)
		}
	}
	if p.Calls[1].Scope == 0 || p.Calls[1].Scope != p.Calls[2].Scope {
		t.Errorf("subshell calls must share a non-root scope: %#v", p.Calls)
	}
	if p.Calls[3].Scope == 0 || p.Calls[3].Scope == p.Calls[2].Scope {
		t.Errorf("command substitution needs its own scope: %#v", p.Calls)
	}
	if got := p.Calls[2].Args[2]; !got.Static || got.Value != "x;y" {
		t.Errorf("quoted option value = %#v", got)
	}
}

func TestParseKeepsTheScopeAncestry(t *testing.T) {
	p, err := Parse(`cd /outer && (cd inner && echo $(git status))`)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Calls) != 4 {
		t.Fatalf("calls = %#v", p.Calls)
	}
	if len(p.Calls[0].Scopes) != 0 || len(p.Calls[1].Scopes) != 1 ||
		len(p.Calls[2].Scopes) != 1 || len(p.Calls[3].Scopes) != 2 {
		t.Fatalf("scope ancestry = %#v", p.Calls)
	}
}

func TestParseRedirectAndHeredoc(t *testing.T) {
	p, err := Parse("cat > 'message file' <<'MSG'\nsubject\nMSG\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Calls) != 1 || len(p.Calls[0].Redirects) != 2 {
		t.Fatalf("calls = %#v", p.Calls)
	}
	redirect := p.Calls[0].Redirects[0]
	if redirect.Op != syntax.RdrOut || !redirect.Target.Static || redirect.Target.Value != "message file" {
		t.Errorf("output redirect = %#v", redirect)
	}
	heredoc := p.Calls[0].Redirects[1]
	if heredoc.Op != syntax.Hdoc || !heredoc.HeredocStatic || heredoc.Heredoc != "subject\n" {
		t.Errorf("heredoc = %#v", heredoc)
	}
}

func TestDynamicWordKeepsAComparableKey(t *testing.T) {
	p, err := Parse(`cat > "$D/msg.txt"`)
	if err != nil {
		t.Fatal(err)
	}
	got := p.Calls[0].Redirects[0].Target
	if got.Static || got.Key() != "$D/msg.txt" {
		t.Errorf("dynamic target = %#v, key %q", got, got.Key())
	}
}

func TestParsePartialKeepsATruncatedHeredocInvocation(t *testing.T) {
	p, err := ParsePartial("python3 - <<'PY'")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Calls) != 1 || p.Calls[0].Args[0].Value != "python3" {
		t.Fatalf("calls = %#v", p.Calls)
	}
}
