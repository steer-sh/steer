package gate

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
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

// replaceShard renames content over the shard and returns the replaced file, still open.
func replaceShard(t *testing.T, cfg Config, content string) *os.File {
	t.Helper()
	old, err := os.Open(cfg.LogFile())
	if err != nil {
		t.Fatal(err)
	}
	tmp := cfg.LogFile() + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, cfg.LogFile()); err != nil {
		t.Fatal(err)
	}
	return old
}

// A rewrite that read the shard after the unlocked write carried the row, so Log must
// not write it again.
func TestUnlockedAppendCarriedByARewriteIsNotRepeated(t *testing.T) {
	cfg := lockTestConfig(t)
	cfg.LogLockWait = 20 * time.Millisecond
	release, err := LockLog(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	afterAppend = func() {
		afterAppend = func() {}
		data, err := os.ReadFile(cfg.LogFile())
		if err != nil {
			t.Error(err)
			return
		}
		replaceShard(t, cfg, "{\"phase\":\"deny\"}\n"+string(data)).Close()
	}
	t.Cleanup(func() { afterAppend = func() {} })
	if err := NewLedger(cfg).Log(Row{"phase": "chain-end"}); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(shardText(t, cfg), "chain-end"); n != 1 {
		t.Fatalf("the current shard holds the row %d times, want once:\n%s", n, shardText(t, cfg))
	}
}

// A rewrite that has renamed but not yet carried the replaced file's growth still holds
// the lock; Log must wait for it before deciding the row is missing.
func TestUnlockedAppendWaitsForTheRewriteToCarry(t *testing.T) {
	cfg := lockTestConfig(t)
	cfg.LogLockWait = 300 * time.Millisecond
	release, err := LockLog(cfg)
	if err != nil {
		t.Fatal(err)
	}
	carried := make(chan struct{})
	afterAppend = func() {
		afterAppend = func() {}
		old := replaceShard(t, cfg, "{\"phase\":\"deny\"}\n")
		go func() {
			defer close(carried)
			defer release()
			defer old.Close()
			time.Sleep(50 * time.Millisecond)
			data, err := io.ReadAll(old)
			if err != nil {
				t.Error(err)
				return
			}
			f, err := os.OpenFile(cfg.LogFile(), os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				t.Error(err)
				return
			}
			defer f.Close()
			f.Write(data)
		}()
	}
	t.Cleanup(func() { afterAppend = func() {} })
	if err := NewLedger(cfg).Log(Row{"phase": "chain-end"}); err != nil {
		t.Fatal(err)
	}
	<-carried
	if n := strings.Count(shardText(t, cfg), "chain-end"); n != 1 {
		t.Fatalf("the current shard holds the row %d times, want once:\n%s", n, shardText(t, cfg))
	}
}

// A Log whose second wait for the shard lock runs out while a rewrite sits between its
// rename and its carry must still wait for the carry before writing the row again.
func TestUnlockedAppendOutwaitedTwiceWaitsForTheCarry(t *testing.T) {
	cfg := lockTestConfig(t)
	cfg.LogLockWait = time.Millisecond
	release, err := LockLog(cfg)
	if err != nil {
		t.Fatal(err)
	}
	carried := make(chan struct{})
	afterAppend = func() {
		afterAppend = func() {}
		releaseSwap, err := LockSwap(cfg)
		if err != nil {
			t.Error(err)
			close(carried)
			return
		}
		old := replaceShard(t, cfg, "{\"phase\":\"deny\"}\n")
		go func() {
			defer close(carried)
			defer release()
			defer releaseSwap()
			defer old.Close()
			time.Sleep(100 * time.Millisecond)
			data, err := io.ReadAll(old)
			if err != nil {
				t.Error(err)
				return
			}
			f, err := os.OpenFile(cfg.LogFile(), os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				t.Error(err)
				return
			}
			defer f.Close()
			f.Write(data)
		}()
	}
	t.Cleanup(func() { afterAppend = func() {} })
	if err := NewLedger(cfg).Log(Row{"phase": "chain-end"}); err != nil {
		t.Fatal(err)
	}
	<-carried
	if n := strings.Count(shardText(t, cfg), "chain-end"); n != 1 {
		t.Fatalf("the current shard holds the row %d times, want once:\n%s", n, shardText(t, cfg))
	}
}

// Two rows with equal fields are two rows: a second one that a rewrite missed must not
// be taken for the first.
func TestUnlockedAppendKeepsAnEqualRow(t *testing.T) {
	cfg := lockTestConfig(t)
	cfg.LogLockWait = 20 * time.Millisecond
	release, err := LockLog(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	row := func() Row { return Row{"ts": 1, "phase": "skip-empty"} }
	if err := NewLedger(cfg).Log(row()); err != nil {
		t.Fatal(err)
	}
	before := shardText(t, cfg)
	afterAppend = func() {
		afterAppend = func() {}
		replaceShard(t, cfg, before).Close()
	}
	t.Cleanup(func() { afterAppend = func() {} })
	if err := NewLedger(cfg).Log(row()); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(shardText(t, cfg), "skip-empty"); n != 2 {
		t.Fatalf("the current shard holds %d equal rows, want 2:\n%s", n, shardText(t, cfg))
	}
}

// A rewrite that carried the row may have stamped it, so the line differs; the row_id
// still names it.
func TestUnlockedAppendKnowsItsRestampedRow(t *testing.T) {
	cfg := lockTestConfig(t)
	cfg.LogLockWait = 20 * time.Millisecond
	release, err := LockLog(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	afterAppend = func() {
		afterAppend = func() {}
		data, err := os.ReadFile(cfg.LogFile())
		if err != nil {
			t.Error(err)
			return
		}
		stamped := strings.Replace(string(data), "{", `{"backfilled":true,`, 1)
		replaceShard(t, cfg, stamped).Close()
	}
	t.Cleanup(func() { afterAppend = func() {} })
	if err := NewLedger(cfg).Log(Row{"phase": "retry"}); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(shardText(t, cfg), `"phase":"retry"`); n != 1 {
		t.Fatalf("the current shard holds the row %d times, want once:\n%s", n, shardText(t, cfg))
	}
}

// A carry cut short leaves a torn line holding the row_id; that is no copy of the row.
func TestUnlockedAppendIgnoresATornCopy(t *testing.T) {
	cfg := lockTestConfig(t)
	cfg.LogLockWait = 20 * time.Millisecond
	release, err := LockLog(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	afterAppend = func() {
		afterAppend = func() {}
		data, err := os.ReadFile(cfg.LogFile())
		if err != nil {
			t.Error(err)
			return
		}
		torn := strings.TrimSpace(string(data))
		replaceShard(t, cfg, torn[:len(torn)-1]+"\n").Close()
	}
	t.Cleanup(func() { afterAppend = func() {} })
	if err := NewLedger(cfg).Log(Row{"phase": "chain-end"}); err != nil {
		t.Fatal(err)
	}
	whole := 0
	for _, line := range strings.Split(shardText(t, cfg), "\n") {
		if strings.Contains(line, "chain-end") && json.Valid([]byte(line)) {
			whole++
		}
	}
	if whole != 1 {
		t.Fatalf("the shard holds %d whole copies of the row, want 1:\n%s", whole, shardText(t, cfg))
	}
}
