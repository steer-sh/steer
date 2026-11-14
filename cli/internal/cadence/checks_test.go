package cadence

import (
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/testhome"
)

// A reported nonpassing check needs its result repaired, while an absent
// executor record needs the oracle's managed line restored.
func TestIntegrationWorkDistinguishesNonpassingAndMissingResults(t *testing.T) {
	for _, missing := range [][]string{nil, {"absent"}} {
		work := integrationWork("/repo", checks.OracleRun{
			IntegrationFailure: true, Missing: missing,
			Reason: "declared checks without a passing result: good (unavailable)",
		})
		if !strings.Contains(work.Detail, "good (unavailable)") {
			t.Fatalf("work %+v; want the nonpassing result", work)
		}
		if len(missing) == 0 {
			if work.Fix != "steer check run" || strings.Contains(work.Detail, "managed line") {
				t.Fatalf("work %+v; want inspection of results, without a wiring diagnosis", work)
			}
		} else if work.Fix != "steer check init" || !strings.Contains(work.Detail, "without running absent") {
			t.Fatalf("work %+v; want the missing executor remedy", work)
		}
	}
}

// A repository declaring checks that this machine never authorized, holding a
// declaration nothing replayed, reports both steps with the command that clears
// each. A caller naming no repository reports none.
func TestCheckWorkReportsAdoptionAndUnprovenChecks(t *testing.T) {
	testhome.Set(t, t.TempDir())
	root := t.TempDir()
	if _, _, err := checks.Init(root); err != nil {
		t.Fatalf("Init: %v", err)
	}
	m, err := checks.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	m.Checks = append(m.Checks, checks.Declaration{
		ID: "no-count", Kind: checks.KindScript, Script: ".steer/checks/no-count.sh"})
	if err := checks.Save(root, m); err != nil {
		t.Fatal(err)
	}

	work := checkWork(root)
	if len(work) != 2 {
		t.Fatalf("work %+v; want the adoption step and the unproven check", work)
	}
	if work[0].Kind != CheckNotAdopted || work[0].Fix != "steer check adopt" {
		t.Errorf("first step %+v; want the adoption step", work[0])
	}
	if work[1].Kind != CheckNeverValidated || work[1].CheckID != "no-count" ||
		work[1].Fix != "steer check test no-count" {
		t.Errorf("second step %+v; want the unproven check named", work[1])
	}
	for _, w := range work {
		if w.Repo == "" || w.Detail == "" {
			t.Errorf("step %+v names no repository or says nothing", w)
		}
	}

	if got := checkWork(""); len(got) != 0 {
		t.Errorf("a caller naming no repository got %+v", got)
	}
}

// A refresh carries the repository's open check steps in the queue it writes.
func TestRefreshCarriesTheCheckSteps(t *testing.T) {
	testhome.Set(t, t.TempDir())
	root := t.TempDir()
	if _, _, err := checks.Init(root); err != nil {
		t.Fatalf("Init: %v", err)
	}
	q, err := Refresh(Options{GateStateDir: t.TempDir(), RepoRoot: root})
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if q.Version != QueueVersion {
		t.Fatalf("queue version %d; want %d", q.Version, QueueVersion)
	}
	if len(q.Checks) != 1 || q.Checks[0].Kind != CheckNotAdopted {
		t.Fatalf("queue checks %+v; want the adoption step", q.Checks)
	}
}
