package cmd

import (
	"strings"
	"testing"
)

func TestBuildSteerPreCommitScript_ProjectScopeDefault(t *testing.T) {
	script := buildSteerPreCommitScript(false)
	if !strings.Contains(script, steerPreCommitMarker) {
		t.Fatalf("expected marker %q in script", steerPreCommitMarker)
	}
	if !strings.Contains(script, "steer verify --staged --scope project") {
		t.Fatalf("expected project verify command in script")
	}
	if strings.Contains(script, "--include-global") {
		t.Fatalf("did not expect --include-global in default script")
	}
}

func TestBuildSteerPreCommitScript_WithGlobal(t *testing.T) {
	script := buildSteerPreCommitScript(true)
	if !strings.Contains(script, "steer verify --staged --scope project --include-global") {
		t.Fatalf("expected include-global verify command in script")
	}
}
