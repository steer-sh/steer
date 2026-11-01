package tui

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/checks"
	"github.com/steer-sh/steer-cli/internal/checkscope"
	"github.com/steer-sh/steer-cli/internal/gate"
	"github.com/steer-sh/steer-cli/internal/gate/resolve"
	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/status"
)

// TestChannelRowsNameTheirHarness pins that two commit-gate rows of one scope are
// told apart on screen. The posture carries one row per harness, and enter toggles
// the row's own harness, so rows that render identically let a reader arm a
// harness they did not choose.
func TestChannelRowsNameTheirHarness(t *testing.T) {
	m := New(gate.Defaults(), "global")
	m.snap = &status.SystemStatus{
		Posture: status.Posture{
			CommitGate: []status.ScopeArming{
				{Harness: "claude-code", Scope: "global", Registered: true, Effective: true},
				{Harness: "codex", Scope: "global"},
				{Harness: "pi", Scope: "global"},
			},
			Resolver: []status.ScopeArming{{Harness: "codex", Scope: "global"}},
		},
	}

	m.active = panelChannels
	view := m.View()
	for _, want := range []string{"Claude Code", "Codex", "Pi"} {
		if !strings.Contains(view, want) {
			t.Errorf("the channels panel does not name %q", want)
		}
	}

	rows := m.channelRows()
	seen := map[string]bool{}
	for _, r := range rows {
		if seen[r.name()] {
			t.Fatalf("two channel rows share the name %q", r.name())
		}
		seen[r.name()] = true
	}

	// The confirmation names the same row the reader selected.
	m.cursor[panelChannels] = 1
	if got := rows[1].name(); !strings.Contains(got, "Codex") {
		t.Errorf("row name %q does not name its harness", got)
	}
}

// TestTheTurnEndHookShowsOnlyWhenItDisagrees pins the panel's one claim about the
// second hook of the pair. Both are written in one operation, so a row per scope
// per harness repeated the gate rows above it and buried the case that matters.
// The reader meets the hook exactly when it is missing or left over.
func TestTheTurnEndHookShowsOnlyWhenItDisagrees(t *testing.T) {
	panel := func(t *testing.T, review, turnEnd status.ScopeArming) string {
		t.Helper()
		m := New(gate.Defaults(), "global")
		m.snap = &status.SystemStatus{Posture: status.Posture{
			CommitGate: []status.ScopeArming{review},
			Resolver:   []status.ScopeArming{turnEnd},
		}}
		m.active = panelChannels
		return m.View()
	}
	armed := status.ScopeArming{Harness: "claude-code", Scope: "global", Registered: true, Effective: true}
	off := status.ScopeArming{Harness: "claude-code", Scope: "global"}

	// The pair agrees, either way: the panel says nothing about the second hook.
	for _, pair := range [][2]status.ScopeArming{{armed, armed}, {off, off}} {
		view := panel(t, pair[0], pair[1])
		if strings.Contains(view, "resolver") || strings.Contains(view, "turn-end") ||
			strings.Contains(view, "stay open") {
			t.Errorf("an agreeing pair still names the second hook:\n%s", view)
		}
	}

	// The gate is armed and nothing closes its reviews.
	if view := panel(t, armed, off); !strings.Contains(view, "stay open") {
		t.Errorf("a missing turn-end hook is not reported:\n%s", view)
	}

	// The gate is gone and its turn-end hook still fires.
	if view := panel(t, off, armed); !strings.Contains(view, "left over") {
		t.Errorf("a left-over turn-end hook is not reported:\n%s", view)
	}
}

// TestWaitingOutcomesAreAnActionRow pins that the reader acts on the waiting
// reviews here rather than leaving for a terminal. The panel is the loop's write
// surface, so a count it can only report is a count it should be able to clear.
func TestWaitingOutcomesAreAnActionRow(t *testing.T) {
	m := New(gate.Defaults(), "global")
	m.snap = &status.SystemStatus{Posture: status.Posture{
		CommitGate: []status.ScopeArming{{Harness: "claude-code", Scope: "global",
			Registered: true, Effective: true}},
		Resolver: []status.ScopeArming{{Harness: "claude-code", Scope: "global",
			Registered: true, Effective: true}},
		Unresolved: 2,
	}}
	m.active = panelChannels

	// The row is selectable, so the cursor reaches it and enter acts on it.
	rows := m.channelRows()
	if len(rows) != 2 || rows[1].kind != "read-outcomes" || rows[1].waiting != 2 {
		t.Fatalf("the waiting outcomes are not the last selectable row: %+v", rows)
	}
	if m.rowCount(panelChannels) != len(rows) {
		t.Error("the cursor cannot reach the action row")
	}
	view := m.View()
	if !strings.Contains(view, "read 2 review outcomes from your session history") {
		t.Errorf("the action row does not say what enter does:\n%s", view)
	}
	// The panel names no terminal command for work it can do itself.
	if strings.Contains(view, status.BackfillCommand) {
		t.Errorf("the panel still sends the reader to a terminal:\n%s", view)
	}

	// Nothing waiting, no row: a row that asks for nothing has to be read to be
	// skipped.
	m.snap.Posture.Unresolved = 0
	if rows := m.channelRows(); len(rows) != 1 {
		t.Errorf("the action row survives an empty backlog: %+v", rows)
	}
}

// TestTheActionRowLeavingDoesNotStrandTheCursor pins the one panel whose row
// count shrinks under its own action. Every panel indexes its rows by the cursor
// after an emptiness check only, so the recompose has to clamp: a cursor left on
// the row that just went away indexes one past the end on the next enter.
func TestTheActionRowLeavingDoesNotStrandTheCursor(t *testing.T) {
	// An empty state dir makes the recompose find no waiting review, which is what
	// a completed read looks like.
	cfg := gate.Defaults()
	cfg.StateDir = t.TempDir()
	m := New(cfg, "global")
	m.snap = &status.SystemStatus{Posture: status.Posture{
		CommitGate: []status.ScopeArming{{Harness: "claude-code", Scope: "global"}},
		Unresolved: 1,
	}}
	m.active = panelChannels
	m.cursor[panelChannels] = len(m.channelRows()) - 1
	if m.cursor[panelChannels] != 1 {
		t.Fatalf("the action row is not where the cursor was put: %d", m.cursor[panelChannels])
	}

	// This is the panic's precondition exactly: channelsKey indexes the rows at the
	// cursor, so a cursor proven below the row count cannot reach past the end. The
	// keystroke itself is not driven — enter on a channel row writes a real hook
	// registration, and a test may not register hooks on the machine running it.
	m.outcomesRead(outcomesReadMsg{before: 1})
	if n := m.rowCount(panelChannels); m.cursor[panelChannels] >= n {
		t.Fatalf("cursor %d is outside the %d remaining rows", m.cursor[panelChannels], n)
	}
}

// TestTheOutcomeReadReportsWhatMoved pins the note the read writes back. A run
// that read nothing has three causes and only one is benign; a failed write
// reported as an empty result is the one that misleads, because the reader would
// press enter again over a shard nothing can change.
func TestTheOutcomeReadReportsWhatMoved(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  outcomesReadMsg
		left int
		want string
	}{
		{"every review still waiting", outcomesReadMsg{before: 1}, 1,
			"no outcome could be read yet"},
		{"some read, some waiting", outcomesReadMsg{before: 3}, 1, "read 2 · 1 still waiting"},
		{"all read", outcomesReadMsg{before: 2}, 0, "read 2 · every review has its outcome"},
		{"the state root could not be prepared",
			outcomesReadMsg{before: 1, err: errors.New("permission denied")}, 1, "could not read"},
		{"the write failed", outcomesReadMsg{before: 1,
			result: resolve.Result{Err: errors.New("read-only file system")}}, 1,
			"nothing was written"},
		{"another run holds the lock",
			outcomesReadMsg{before: 1, result: resolve.Result{Locked: true}}, 1, "right now"},
	} {
		if got := outcomeNote(tc.msg, tc.left); !strings.Contains(got, tc.want) {
			t.Errorf("%s: note = %q, want it to name %q", tc.name, got, tc.want)
		}
	}

	// The read is marked done however it ended, or the row says "reading…" forever.
	m := New(gate.Defaults(), "global")
	m.reading = true
	m.outcomesRead(outcomesReadMsg{before: 1, result: resolve.Result{Locked: true}})
	if m.reading || m.message == "" {
		t.Errorf("reading = %v, note = %q", m.reading, m.message)
	}
}

// TestTheCheckRunnerIsAnArmingRow pins that the Stop runner is armed where the
// other channels are. Without the row, a runner that was never registered could
// be repaired only from a terminal, and a registered one could not be turned off.
func TestTheCheckRunnerIsAnArmingRow(t *testing.T) {
	m := New(gate.Defaults(), "global")
	m.snap = &status.SystemStatus{
		Posture: status.Posture{
			CommitGate: []status.ScopeArming{{Harness: "claude-code", Scope: "global"}},
		},
		Checks: status.CheckTier{Runner: []status.ScopeArming{
			{Harness: "claude-code", Scope: "global"},
			{Harness: "codex", Scope: "global"},
		}},
	}
	want := []string{
		"commit gate · Claude Code global",
		"check runner · Claude Code global",
		"check runner · Codex global",
	}
	rows := m.channelRows()
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(rows), len(want), rows)
	}
	for i, w := range want {
		if got := rows[i].name(); got != w {
			t.Errorf("row %d is %q, want %q", i, got, w)
		}
	}

	// Enter writes the runner through the same registration `steer check install`
	// writes, and a second enter on the registered row removes it. TestMain points
	// both harness homes at an empty directory.
	for _, row := range rows[1:3] {
		if err := m.toggleChannel(row); err != nil {
			t.Fatalf("arming %s: %v", row.name(), err)
		}
		a := runnerAt(t, row)
		if !a.Registered || a.Timeout != checks.RegisteredHookTimeout() ||
			!strings.HasSuffix(a.Command, "check hook --harness "+row.harness) {
			t.Fatalf("%s after arming = %+v", row.name(), a)
		}
		row.armed = status.ScopeArming{Harness: row.harness, Scope: row.scope, Registered: true}
		if err := m.toggleChannel(row); err != nil {
			t.Fatalf("disarming %s: %v", row.name(), err)
		}
		if runnerAt(t, row).Registered {
			t.Fatalf("%s is still registered after disarming", row.name())
		}
	}
}

// runnerAt reads back the Stop runner registration a row names.
func runnerAt(t *testing.T, row channelRow) checkscope.Arming {
	t.Helper()
	dir, _ := os.Getwd()
	a, err := checkscope.RunnerArming(harness.ID(row.harness), row.scope, dir)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// TestChannelRowsGroupAHarnessTogether pins the panel's sort. The posture is
// emitted scope-major, and a harness's two scopes are the pair a person toggles,
// so the panel sorts the harness above the scope while keeping the commit gate as
// one block a reader compares across harnesses.
func TestChannelRowsGroupAHarnessTogether(t *testing.T) {
	m := New(gate.Defaults(), "global")
	m.snap = &status.SystemStatus{
		Posture: status.Posture{
			CommitGate: []status.ScopeArming{
				{Harness: "claude-code", Scope: "project"},
				{Harness: "codex", Scope: "project"},
				{Harness: "pi", Scope: "project"},
				{Harness: "claude-code", Scope: "global"},
				{Harness: "codex", Scope: "global"},
				{Harness: "pi", Scope: "global"},
			},
		},
	}

	want := []string{
		"commit gate · Claude Code project",
		"commit gate · Claude Code global",
		"commit gate · Codex project",
		"commit gate · Codex global",
		"commit gate · Pi project",
		"commit gate · Pi global",
	}
	rows := m.channelRows()
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d", len(rows), len(want))
	}
	for i, w := range want {
		if got := rows[i].name(); got != w {
			t.Errorf("row %d is %q, want %q", i, got, w)
		}
	}
}
