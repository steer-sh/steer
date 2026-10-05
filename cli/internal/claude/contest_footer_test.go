package claude

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var footerFiring = regexp.MustCompile(`firing: ([0-9a-f]{32})`)

// tempHome points HOME at an empty directory, so no stable steer executable is
// installed unless the test puts one there.
func tempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

// The firing the footer names is the row the master wrote, so a contest of it
// resolves; the footer offers the contest before removal and makes removal the
// user's decision.
func TestABlockFooterNamesItsFiringAndTheContest(t *testing.T) {
	withProjectTempDir(t)
	tempHome(t)
	requirePython3(t)
	installGuard(t, "marker-hook", markerHookContent)

	reason := masterReason(t, runMasterWithArgs(t, `{"tool_name":"Bash","tool_input":{"command":"echo FORBIDDEN"}}`))
	match := footerFiring.FindStringSubmatch(reason)
	if match == nil {
		t.Fatalf("footer names no firing: %s", reason)
	}
	id := match[1]
	rows := readAudit(t)
	if len(rows) != 1 || rows[0]["firing_id"] != id {
		t.Fatalf("footer firing %s is not the row the master wrote: %v", id, rows)
	}
	if rows[0]["contest_protocol"] != float64(1) {
		t.Fatalf("row carries no contest protocol: %v", rows[0])
	}
	for _, want := range []string{
		"steer hook: marker-hook · firing: " + id,
		"steer contest " + id + " '<task-specific reason>'",
		"The block stays.",
		"Removing this hook needs the user's approval: steer remove marker-hook",
	} {
		if !strings.Contains(reason, want) {
			t.Errorf("footer lacks %q:\n%s", want, reason)
		}
	}
	if strings.Index(reason, " contest ") > strings.Index(reason, " remove ") {
		t.Errorf("footer offers removal before the contest:\n%s", reason)
	}
	if strings.Contains(reason, "--scope global") {
		t.Errorf("a project guard's removal names the global scope:\n%s", reason)
	}
}

// `steer remove` reads the project scope by default, so a global guard's footer
// must name the scope that holds it.
func TestAGlobalGuardFooterNamesTheGlobalRemoval(t *testing.T) {
	withProjectTempDir(t)
	tempHome(t)
	requirePython3(t)
	if _, err := InstallManagedHook(GlobalScope, InstallManagedHookInput{
		HookName: "marker-hook",
		FileName: "marker-hook.py",
		Rule:     "no FORBIDDEN",
		Content:  []byte(markerHookContent),
		Metadata: HookInstallMetadata{Source: SourceGenerated},
	}); err != nil {
		t.Fatal(err)
	}
	master, err := GetMasterHookPath(GlobalScope)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("python3", master)
	cmd.Stdin = strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"echo FORBIDDEN"}}`)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("global master failed: %v", err)
	}
	reason := masterReason(t, strings.TrimSpace(string(out)))
	if !strings.Contains(reason, "steer remove marker-hook --scope global") {
		t.Fatalf("global guard's removal omits its scope:\n%s", reason)
	}
}

// A blocked agent may not have steer on its PATH, so the footer names the stable
// executable when one is installed.
func TestTheFooterNamesTheStableExecutableWhenInstalled(t *testing.T) {
	withProjectTempDir(t)
	home := tempHome(t)
	requirePython3(t)
	stable := filepath.Join(home, ".steer", "bin", "steer")
	if err := os.MkdirAll(filepath.Dir(stable), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stable, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	installGuard(t, "marker-hook", markerHookContent)

	reason := masterReason(t, runMasterWithArgs(t, `{"tool_name":"Bash","tool_input":{"command":"echo FORBIDDEN"}}`))
	if !strings.Contains(reason, stable+" contest ") || !strings.Contains(reason, stable+" remove marker-hook") {
		t.Fatalf("footer does not name the stable executable %s:\n%s", stable, reason)
	}
}

// An agent pastes the footer's commands into a shell, so a stable executable
// under a home that holds a space must still run as one word.
func TestTheFooterCommandsRunUnderAHomeWithASpace(t *testing.T) {
	withProjectTempDir(t)
	home := filepath.Join(t.TempDir(), "ho me")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	requirePython3(t)
	stable := filepath.Join(home, ".steer", "bin", "steer")
	if err := os.MkdirAll(filepath.Dir(stable), 0o755); err != nil {
		t.Fatal(err)
	}
	argsFile := filepath.Join(t.TempDir(), "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> '" + argsFile + "'\n"
	if err := os.WriteFile(stable, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	installGuard(t, "marker-hook", markerHookContent)

	reason := masterReason(t, runMasterWithArgs(t, `{"tool_name":"Bash","tool_input":{"command":"echo FORBIDDEN"}}`))
	id := footerFiring.FindStringSubmatch(reason)
	if id == nil {
		t.Fatalf("footer names no firing: %s", reason)
	}
	contest := regexp.MustCompile(`record why: (.*'<task-specific reason>')`).FindStringSubmatch(reason)
	removal := regexp.MustCompile(`(?m)approval: (.*)$`).FindStringSubmatch(reason)
	if contest == nil || removal == nil {
		t.Fatalf("footer lacks a contest or a removal command:\n%s", reason)
	}
	for _, command := range []string{contest[1], removal[1]} {
		if out, err := exec.Command("sh", "-c", command).CombinedOutput(); err != nil {
			t.Fatalf("footer command %q failed: %v\n%s", command, err, out)
		}
	}
	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	want := "contest\n" + id[1] + "\n<task-specific reason>\nremove\nmarker-hook\n"
	if string(got) != want {
		t.Fatalf("the footer commands passed %q, want %q", got, want)
	}
}

// A firing identifier that cannot be allocated still blocks and still writes its
// row. The footer then offers no contest, so the row claims no contest protocol.
func TestAFailedFiringIdentifierStillBlocksWithoutAContest(t *testing.T) {
	withProjectTempDir(t)
	tempHome(t)
	requirePython3(t)
	installGuard(t, "marker-hook", markerHookContent)

	site := t.TempDir()
	if err := os.WriteFile(filepath.Join(site, "sitecustomize.py"), []byte(`import os
def _fails(n):
    raise OSError("no entropy")
os.urandom = _fails
`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PYTHONPATH", site)

	reason := masterReason(t, runMasterWithArgs(t, `{"tool_name":"Bash","tool_input":{"command":"echo FORBIDDEN"}}`))
	if strings.Contains(reason, "firing:") || strings.Contains(reason, " contest ") {
		t.Fatalf("a footer with no firing identifier offers a contest:\n%s", reason)
	}
	rows := readAudit(t)
	if len(rows) != 1 {
		t.Fatalf("expected 1 audit row, got %d", len(rows))
	}
	if _, ok := rows[0]["firing_id"]; ok {
		t.Fatalf("a row with no allocated identifier carries one: %v", rows[0])
	}
	if _, ok := rows[0]["contest_protocol"]; ok {
		t.Fatalf("a row whose footer offered no contest claims a contest protocol: %v", rows[0])
	}
}
