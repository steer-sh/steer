package tui

import (
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/status"
)

// inventory holds one class per action section, in an inventory order that is
// not the render order: the first item renders last.
func inventory() []status.ScoredIntervention {
	return []status.ScoredIntervention{
		{ID: "a", Class: "settled-class", Status: "active", Verdict: "effective"},
		{ID: "b", Class: "waiting-class", Status: "active", Verdict: "collecting"},
		{ID: "c", Class: "harmful-class", Status: "active", Verdict: "harmful"},
	}
}

// The cursor indexes the order the panel shows, so an arrow key walks the list
// down the screen. Keying on the inventory index made the pointer jump between
// sections.
func TestClassesCursorWalksTheRenderedOrder(t *testing.T) {
	m := New(gate.Defaults(), "global")
	m.snap = &status.SystemStatus{Interventions: inventory()}
	m.active = panelClasses

	classes := []string{"harmful-class", "waiting-class", "settled-class"}
	for pos, want := range classes {
		m.cursor[panelClasses] = pos
		out := m.viewClasses()
		marked := ""
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, pointer(true)) {
				marked += strings.TrimSpace(line)
			}
		}
		if marked == "" {
			t.Fatalf("cursor %d marked no line:\n%s", pos, out)
		}
		if !strings.Contains(marked, want) {
			t.Errorf("cursor %d points at %q, want %q", pos, marked, want)
		}
	}
}

// The retire wizard opens on the class the pointer marks, not on the one sharing
// its inventory index.
func TestClassesRetireTargetsThePointedClass(t *testing.T) {
	items := inventory()
	ord := classOrder(items, status.ChainRate{})
	if len(ord) != len(items) {
		t.Fatalf("classOrder returned %d positions for %d items", len(ord), len(items))
	}
	if got := items[ord[0]].ID; got != "c" {
		t.Errorf("position 0 is %q, want the harmful class %q", got, "c")
	}
	if got := items[ord[2]].ID; got != "a" {
		t.Errorf("position 2 is %q, want the settled class %q", got, "a")
	}
}

// A proven rule is never headed as a repair.
func TestClassesDoNotHeadAnEffectiveRuleAsARepair(t *testing.T) {
	m := New(gate.Defaults(), "global")
	m.snap = &status.SystemStatus{Interventions: inventory()}
	out := m.viewClasses()
	repair := strings.Index(out, status.ActionHeading[status.ActionRepair])
	if repair >= 0 {
		t.Fatalf("a repair section rendered over an inventory holding no repair:\n%s", out)
	}
	if !strings.Contains(out, status.ActionHeading[status.ActionSettled]) {
		t.Errorf("the effective class rendered under no settled heading:\n%s", out)
	}
}
