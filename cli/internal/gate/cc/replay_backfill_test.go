package cc

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/steer-sh/steer-cli/internal/gate"
)

// TestReplayBackfill runs backfill_ledger.py beside the Go resolver over the real local
// shard and the real transcripts, and requires the resolved rows to match by-parse
// (field equality, not output bytes). The live shard is already resolved, so a plain
// replay would no-op on both sides; the reset strips the resolver's stamps from every
// ledger-owing row so both must harvest, join and re-stamp from scratch. Gated on
// COMMIT_GATE_REPLAY_BACKFILL so the turn-end gate never runs it.
func TestReplayBackfill(t *testing.T) {
	if os.Getenv("COMMIT_GATE_REPLAY_BACKFILL") == "" {
		t.Skip("COMMIT_GATE_REPLAY_BACKFILL not set")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(home, ".claude", "state", "commit-review")
	// The resolver rules on this machine's shard and no other, so the replay operates on
	// exactly that shard on both sides.
	hostID := gate.HostID()
	shardName := "adjudication." + hostID + ".jsonl"
	localShard := filepath.Join(state, shardName)
	if _, err := os.Stat(localShard); err != nil {
		t.Skip("no local shard to replay")
	}
	projects := filepath.Join(home, ".claude", "projects")
	driver := driverPath(t)

	reset, owing := resetShard(t, localShard)
	if owing == 0 {
		t.Skip("no ledger-owing rows to resolve")
	}
	pyDir := seedShard(t, shardName, reset)
	goDir := seedShard(t, shardName, reset)

	// Python side: the driver repoints backfill_ledger.py at the copy.
	cmd := exec.Command("python3", driver, "--state", pyDir)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("driver: %v\n%s", err, errBuf.String())
	}

	// Go side: same reset shard, same transcripts, same wall clock band. HostID makes
	// RunBackfill read the seeded shard rather than an empty-host name.
	cfg := gate.Config{StateDir: goDir, HostID: hostID, StaleAfter: 30 * time.Minute}
	opts := BackfillOptions{Apply: true, Quiet: true}
	RunBackfill(cfg, projects, opts, nil, func() float64 { return float64(time.Now().Unix()) })

	pyRows := readShard(t, filepath.Join(pyDir, shardName))
	goRows := readShard(t, filepath.Join(goDir, shardName))
	if len(pyRows) != len(goRows) {
		t.Fatalf("row count: python %d, go %d", len(pyRows), len(goRows))
	}
	restamped := 0
	for i := range pyRows {
		if !reflect.DeepEqual(pyRows[i], goRows[i]) {
			t.Errorf("row %d differs\n python: %v\n     go: %v", i, pyRows[i], goRows[i])
		}
		if pyTruthy(goRows[i]["backfilled"]) {
			restamped++
		}
	}
	// Guard against a silently vacuous replay (e.g. the resolver reading an empty-host
	// shard name and writing nothing). A row still inside its open window is skipped and
	// not re-stamped, so require a majority rather than all.
	if restamped*2 < owing {
		t.Fatalf("resolver re-stamped %d of %d owing rows — replay did not exercise resolution", restamped, owing)
	}
}

// resetShard strips the resolver's stamps (backfilled, ledger_found) from every
// ledger-owing row of a shard, so a resolver run must recompute them. It returns the
// reset shard bytes and the count of rows it reset.
func resetShard(t *testing.T, path string) ([]byte, int) {
	t.Helper()
	rows := readShard(t, path)
	owing := 0
	var sb bytes.Buffer
	for _, row := range rows {
		if expectsLedger(row) {
			delete(row, "backfilled")
			delete(row, "ledger_found")
			owing++
		}
		line, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		sb.Write(line)
		sb.WriteByte('\n')
	}
	return sb.Bytes(), owing
}

// seedShard writes shard bytes into a fresh temp state dir under the given name.
func seedShard(t *testing.T, name string, data []byte) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// driverPath locates replay_backfill.py beside this test source.
func driverPath(t *testing.T) string {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(self), "replay_backfill.py")
}
