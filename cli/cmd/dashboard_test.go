package cmd

import (
	"errors"
	"os"
	"testing"
)

// Under a pipe the dashboard refuses at once and names the read an agent uses,
// rather than drawing nowhere.
func TestDashboardRefusesWithoutATerminal(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if err := requireTerminal(r.Fd(), w.Fd()); !errors.Is(err, errNoTerminal) {
		t.Fatalf("requireTerminal over a pipe = %v; want the no-terminal refusal", err)
	}
}
