package gate

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func lockTestConfig(t *testing.T) Config {
	t.Helper()
	return Config{StateDir: t.TempDir(), HostID: "testhost"}
}

func shardText(t *testing.T, cfg Config) string {
	t.Helper()
	data, err := os.ReadFile(cfg.LogFile())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestLogWaitsForTheShardLock(t *testing.T) {
	cfg := lockTestConfig(t)
	cfg.LogLockWait = time.Minute
	release, err := LockLog(cfg)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- NewLedger(cfg).Log(Row{"phase": "chain-end"}) }()
	select {
	case <-done:
		t.Fatal("Log appended while another writer held the shard lock")
	case <-time.After(150 * time.Millisecond):
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(shardText(t, cfg), "chain-end") {
		t.Fatal("the row never reached the shard")
	}
}

func TestLogAppendsWhenTheLockStaysHeld(t *testing.T) {
	cfg := lockTestConfig(t)
	cfg.LogLockWait = 20 * time.Millisecond
	release, err := LockLog(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	l := NewLedger(cfg)
	if err := l.Log(Row{"phase": "chain-end"}); err != nil {
		t.Fatalf("a busy lock must not fail the append: %v", err)
	}
	text := shardText(t, cfg)
	if !strings.Contains(text, "chain-end") || !strings.Contains(text, "ledger_lock") {
		t.Fatalf("the row must land and name the busy lock: %s", text)
	}
}

func TestLogAppendsWhenTheLockCannotOpen(t *testing.T) {
	cfg := lockTestConfig(t)
	if err := os.Mkdir(cfg.LogFile()+".lock", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := NewLedger(cfg).Log(Row{"phase": "chain-end"}); err != nil {
		t.Fatalf("an unusable lock must not fail the append: %v", err)
	}
	if text := shardText(t, cfg); !strings.Contains(text, "ledger_lock") {
		t.Fatalf("the row must name the unusable lock: %s", text)
	}
}

func TestLogStillReportsAFailedAppend(t *testing.T) {
	cfg := lockTestConfig(t)
	if err := os.Mkdir(cfg.LogFile(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := NewLedger(cfg).Log(Row{"phase": "deny"}); err == nil {
		t.Fatal("an append that cannot open the shard must return its error")
	}
	release, err := LockLog(cfg)
	if err != nil {
		t.Fatalf("Log must release the lock on failure: %v", err)
	}
	release()
}

func TestLockLogBoundsItsWait(t *testing.T) {
	cfg := lockTestConfig(t)
	cfg.LogLockWait = 20 * time.Millisecond
	release, err := LockLog(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := LockLog(cfg); err == nil || !strings.Contains(err.Error(), ErrLogLockBusy.Error()) {
		t.Fatalf("a held lock must end the wait with ErrLogLockBusy; got %v", err)
	}
}

// A killed holder runs no release; the OS must free the lock for the next writer.
func TestLockFreedWhenItsHolderIsKilled(t *testing.T) {
	const env = "STEER_TEST_LOCK_HOLDER"
	if dir := os.Getenv(env); dir != "" {
		release, err := LockLog(Config{StateDir: dir, HostID: "testhost"})
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		defer release()
		fmt.Println("held")
		time.Sleep(time.Minute)
		return
	}
	cfg := lockTestConfig(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestLockFreedWhenItsHolderIsKilled$")
	cmd.Env = append(os.Environ(), env+"="+cfg.StateDir)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	line, _ := bufio.NewReader(out).ReadString('\n')
	if strings.TrimSpace(line) != "held" {
		t.Fatalf("the holder did not take the lock: %q", line)
	}
	cfg.LogLockWait = 20 * time.Millisecond
	if _, err := LockLog(cfg); err == nil {
		t.Fatal("the live holder's lock must exclude this process")
	}
	cmd.Process.Kill()
	cmd.Wait()
	cfg.LogLockWait = 10 * time.Second
	release, err := LockLog(cfg)
	if err != nil {
		t.Fatalf("a killed holder must not keep the lock: %v", err)
	}
	release()
	if _, err := os.Stat(filepath.Join(cfg.StateDir, filepath.Base(cfg.LogFile())+".lock")); err != nil {
		t.Fatalf("the lock file stays so every writer locks one inode: %v", err)
	}
}

// A rewrite that replaces the shard after an unlocked write leaves the row on an
// unlinked file, so Log writes it again.
func TestUnlockedAppendFollowsAReplacedShard(t *testing.T) {
	cfg := lockTestConfig(t)
	cfg.LogLockWait = 20 * time.Millisecond
	release, err := LockLog(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	afterAppend = func() {
		afterAppend = func() {}
		tmp := cfg.LogFile() + ".tmp"
		if err := os.WriteFile(tmp, []byte("{\"phase\":\"deny\"}\n"), 0o644); err != nil {
			t.Error(err)
			return
		}
		if err := os.Rename(tmp, cfg.LogFile()); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { afterAppend = func() {} })
	if err := NewLedger(cfg).Log(Row{"phase": "chain-end"}); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(shardText(t, cfg), "chain-end"); n != 1 {
		t.Fatalf("the current shard holds the row %d times, want once:\n%s", n, shardText(t, cfg))
	}
}
