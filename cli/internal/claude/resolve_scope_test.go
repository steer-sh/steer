package claude

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// twoStores points HOME at one temp directory and the working directory at
// another, so the global and project guard stores resolve to different trees.
// It returns both hook directories.
func twoStores(t *testing.T) (projectDir, globalDir string) {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	repo := t.TempDir()
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	projectDir, err = GetHooksDir(ProjectScope)
	if err != nil {
		t.Fatal(err)
	}
	globalDir, err = GetHooksDir(GlobalScope)
	if err != nil {
		t.Fatal(err)
	}
	if strings.EqualFold(projectDir, globalDir) {
		t.Fatalf("the two stores resolved to one directory: %s", projectDir)
	}
	return projectDir, globalDir
}

func writeHook(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".py"), []byte("import sys\nsys.exit(0)\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestAMissNamesTheScopeHoldingTheHook: the default scope is project, so a
// global guard answered `steer test <hook>` with a bare "no hooks found" and the
// operator had no way to learn --scope global exists.
func TestAMissNamesTheScopeHoldingTheHook(t *testing.T) {
	_, globalDir := twoStores(t)
	writeHook(t, globalDir, "no-bare-python")

	_, err := ResolveHook(ProjectScope, "no-bare-python")
	if err == nil {
		t.Fatal("the project store holds no hook, so the lookup must miss")
	}
	if !errors.Is(err, ErrHookInOtherScope) {
		t.Fatalf("a hook in the other scope must carry ErrHookInOtherScope: %v", err)
	}
	if !strings.Contains(err.Error(), "--scope global") {
		t.Fatalf("the miss must name the flag that reaches it: %v", err)
	}
}

// TestAMissFromTheGlobalStoreNamesTheProjectScope: the hint is symmetric, so an
// operator who passed --scope global for a project guard learns the same way.
func TestAMissFromTheGlobalStoreNamesTheProjectScope(t *testing.T) {
	projectDir, _ := twoStores(t)
	writeHook(t, projectDir, "no-bare-python")

	_, err := ResolveHook(GlobalScope, "no-bare-python")
	if !errors.Is(err, ErrHookInOtherScope) {
		t.Fatalf("a hook in the other scope must carry ErrHookInOtherScope: %v", err)
	}
	if !strings.Contains(err.Error(), "--scope project") {
		t.Fatalf("the miss must name the flag that reaches it: %v", err)
	}
}

// TestAnEmptyStoreNamesTheScopeItRead: with nothing installed anywhere, the
// reader still learns which store was read and that a second one exists.
func TestAnEmptyStoreNamesTheScopeItRead(t *testing.T) {
	twoStores(t)

	_, err := ResolveHook(ProjectScope, "no-bare-python")
	if err == nil {
		t.Fatal("an empty store must miss")
	}
	if errors.Is(err, ErrHookInOtherScope) {
		t.Fatalf("no store holds the hook, so nothing may point at a scope: %v", err)
	}
	if !strings.Contains(err.Error(), "project") || !strings.Contains(err.Error(), "--scope global") {
		t.Fatalf("the miss must name the store it read and the other one: %v", err)
	}
}

// TestAnUnknownNameBesideInstalledHooksNamesTheStore: a store holding other
// guards reports the miss against that store rather than pointing elsewhere.
func TestAnUnknownNameBesideInstalledHooksNamesTheStore(t *testing.T) {
	projectDir, _ := twoStores(t)
	writeHook(t, projectDir, "no-bare-python")

	_, err := ResolveHook(ProjectScope, "some-other-guard")
	if err == nil {
		t.Fatal("an unknown identifier must miss")
	}
	if errors.Is(err, ErrHookInOtherScope) {
		t.Fatalf("no store holds this identifier: %v", err)
	}
	if !strings.Contains(err.Error(), "project") {
		t.Fatalf("the miss must name the store it read: %v", err)
	}
}

// TestTheReadScopeWinsOverTheOtherOne: both stores holding the name must resolve
// to the one asked for, or the hint would reach a guard the operator did not name.
func TestTheReadScopeWinsOverTheOtherOne(t *testing.T) {
	projectDir, globalDir := twoStores(t)
	writeHook(t, projectDir, "no-bare-python")
	writeHook(t, globalDir, "no-bare-python")

	hook, err := ResolveHook(GlobalScope, "no-bare-python")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hook.Path, globalDir) {
		t.Fatalf("the global read resolved to %s, outside %s", hook.Path, globalDir)
	}
}
