package mine

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steer-sh/steer-cli/internal/testhome"
)

// TestConcurrentInventoryMutations exercises independent CLI processes sharing
// an inventory, including two writers assigning IDs from the same class slug.
func TestConcurrentInventoryMutations(t *testing.T) {
	if operation := os.Getenv("STEER_INVENTORY_TEST_OPERATION"); operation != "" {
		state := os.Getenv("STEER_INVENTORY_TEST_STATE")
		if err := os.WriteFile(filepath.Join(state, operation+".ready"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		var err error
		switch {
		case strings.HasPrefix(operation, "register"):
			err = Register(io.Discard, state, RegisterOpts{
				Prose: testProse, Class: "concurrent class", Match: "concurrent",
				Tier: "guard", HookID: "absent-test-guard", HookScope: "global",
				ScopeAll: true, Force: true,
			})
		case strings.HasPrefix(operation, "reject"):
			err = Reject(io.Discard, state, RejectOpts{
				Prose: testProse, Class: "concurrent class", Match: "concurrent",
				ScopeAll: true, Force: true,
			})
		case operation == "scope":
			err = ScopeSet(io.Discard, state, "seed", ScopeSetOpts{ScopeAll: true, Force: true})
		case operation == "retire":
			err = Retire(io.Discard, state, "seed", "concurrent retirement", "removed")
		case operation == "describe":
			err = Describe(io.Discard, state, "seed", DescribeOpts{Problem: "concurrent explanation"})
		}
		if err != nil {
			t.Fatal(err)
		}
		return
	}

	testhome.Set(t, t.TempDir())
	t.Chdir(t.TempDir())
	state := t.TempDir()
	seed := Record{"id": "seed", "class": "seed class", "match": "seed", "status": "active"}
	testProse.writeInto(seed)
	if err := Save(state, []Record{seed}); err != nil {
		t.Fatal(err)
	}
	release, err := lockInventory(state)
	if err != nil {
		t.Fatal(err)
	}
	held := true
	defer func() {
		if held {
			release()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	type worker struct {
		operation string
		output    *bytes.Buffer
		done      chan error
	}
	var workers []worker
	for _, operation := range []string{"register-1", "register-2", "reject-1", "reject-2", "scope", "retire", "describe"} {
		output := new(bytes.Buffer)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestConcurrentInventoryMutations$")
		cmd.Env = append(os.Environ(), "STEER_INVENTORY_TEST_OPERATION="+operation, "STEER_INVENTORY_TEST_STATE="+state)
		cmd.Stdout, cmd.Stderr = output, output
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		workers = append(workers, worker{operation, output, done})
	}
	for _, worker := range workers {
		for {
			if _, err := os.Stat(filepath.Join(state, worker.operation+".ready")); err == nil {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("writers did not start before timeout")
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	// Give contenders time to reach the held transaction before changing its data.
	time.Sleep(300 * time.Millisecond)
	for _, worker := range workers {
		select {
		case err := <-worker.done:
			t.Fatalf("%s finished while another process held the transaction: %v\n%s", worker.operation, err, worker.output)
		default:
		}
	}
	if err := save(state, []Record{seed, {"id": "committed-during-contention"}}); err != nil {
		t.Fatal(err)
	}
	release()
	held = false
	for _, worker := range workers {
		if err := <-worker.done; err != nil {
			t.Errorf("%s: %v\n%s", worker.operation, err, worker.output)
		}
	}
	records, err := Load(state)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 6 {
		t.Fatalf("successful concurrent writes lost records: got %d, want 6", len(records))
	}
	seen := make(map[string]bool)
	for _, record := range records {
		id := str(record["id"])
		if seen[id] {
			t.Errorf("concurrent writers assigned duplicate ID %q", id)
		}
		seen[id] = true
	}
	if !seen["committed-during-contention"] {
		t.Error("a waiting writer overwrote a committed record")
	}
	record := findRecord(records, "seed")
	if record == nil || str(record["status"]) != "removed" || record["scope"] == nil || str(record[ProblemKey]) != "concurrent explanation" {
		t.Errorf("concurrent updates to one record were lost: %v", record)
	}
}
