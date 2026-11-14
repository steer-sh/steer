package cmd

import (
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/report"
)

// The two scope flags select different sessions, so passing both is refused
// rather than silently resolved.
func TestScanScopeRefusesBothFlags(t *testing.T) {
	_, err := scanScope(true, true)
	if err == nil {
		t.Fatal("--repo-only with --project-only must be refused")
	}
	if !strings.Contains(err.Error(), "--repo-only") || !strings.Contains(err.Error(), "--project-only") {
		t.Errorf("the error must name both flags: %v", err)
	}
}

// A repository scope resolves its checkout here, because evidence gathering
// fails soft and would otherwise print an empty machine.
func TestScanScopeRefusesRepositoryScopeOutsideARepository(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := scanScope(true, false); err == nil {
		t.Fatal("--repo-only outside a repository must be refused")
	}
}

func TestScanScopeMapsTheFlags(t *testing.T) {
	scope, err := scanScope(false, true)
	if err != nil || scope != report.ScopeProject {
		t.Errorf("scanScope(project) = %v, %v", scope, err)
	}
	scope, err = scanScope(false, false)
	if err != nil || scope != report.ScopeAll {
		t.Errorf("scanScope(default) = %v, %v", scope, err)
	}
}
