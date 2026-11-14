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

// A double-quoted `$(cat <<'TAG' ...)` has a fixed value: the heredoc body without
// its trailing newlines. An unquoted substitution splits into words, an unquoted tag
// expands, and any other command runs, so each of those has none. Static stays false
// for every substitution.
func TestQuotedCatHeredocSubstitutionHasAFixedValue(t *testing.T) {
	cases := []struct {
		src    string
		static bool
		value  string
	}{
		{"x -m \"$(cat <<'EOF'\nsubject\n\nbody $HOME\nEOF\n)\"", true, "subject\n\nbody $HOME"},
		{"x -m \"pre $(cat <<\"EOF\"\nsubject\nEOF\n)\"", true, "pre subject"},
		{"x -m $(cat <<'EOF'\nsubject\nEOF\n)", false, ""},
		{"x -m \"$(cat <<EOF\nsubject\nEOF\n)\"", false, ""},
		{"x -m \"$(cat msg.txt)\"", false, ""},
		{"x -m \"$(sh <<'EOF'\necho subject\nEOF\n)\"", false, ""},
		{"x -m \"$(cat <<'EOF' | tr a b\nsubject\nEOF\n)\"", false, ""},
	}
	for _, tc := range cases {
		p, err := Parse(tc.src)
		if err != nil {
			t.Fatalf("%q: %v", tc.src, err)
		}
		word := p.Calls[0].Args[2]
		if word.Static {
			t.Errorf("%q: a substitution must not read as Static", tc.src)
		}
		got, fixed := FixedValue(word)
		if fixed != tc.static || got != tc.value {
			t.Errorf("%q: fixed %v value %q, want %v %q", tc.src, fixed, got, tc.static, tc.value)
		}
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

// A static word is the value after quote removal; a heredoc body keeps its backslashes.
func TestStaticWordRemovesQuotingButHeredocKeepsBackslashes(t *testing.T) {
	p, err := Parse("cat my\\ file \"a\\\"b\\x\" $'x\\ty' <<'EOF'\na\\$b \\x\nEOF")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"cat", "my file", `a"b\x`, "x\ty"}
	for i, arg := range p.Calls[0].Args {
		if !arg.Static || arg.Value != want[i] {
			t.Errorf("arg %d = %#v, want %q", i, arg, want[i])
		}
	}
	if got := p.Calls[0].Redirects[0].Heredoc; got != "a\\$b \\x\n" {
		t.Errorf("heredoc = %q", got)
	}
	// expand.Literal and bash disagree on octal overflow and control escapes.
	p, err = Parse(`x $'a\400b' $'a\cAb'`)
	if err != nil {
		t.Fatal(err)
	}
	for _, arg := range p.Calls[0].Args[1:] {
		if arg.Static {
			t.Errorf("%s is static: %q", arg.Raw, arg.Value)
		}
	}
}
