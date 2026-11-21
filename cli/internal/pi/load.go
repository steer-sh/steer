package pi

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/steerpath"
)

const (
	// LoadsComponent is the state directory each bridge records its loads in.
	LoadsComponent = "pi-loads"
	loadsToken     = `const STEER_LOADS = "__STEER_LOADS__";`
	loadsLine      = "const STEER_LOADS = "
)

//go:embed bridge_load.ts
var loadRecorderSource string

// bridgeDigest identifies a bridge body together with the load recorder every
// bridge carries.
func bridgeDigest(body string) string {
	sum := sha256.Sum256([]byte(strings.TrimRight(body, "\n") + "\n" + loadRecorderSource))
	return hex.EncodeToString(sum[:])[:8]
}

// withLoadRecorder appends the load recorder to a bridge body, with this machine's
// load directory baked in.
func withLoadRecorder(body string) (string, error) {
	if strings.Count(loadRecorderSource, loadsToken) != 1 {
		return "", fmt.Errorf("Pi load recorder carries no unique load directory placeholder")
	}
	dir, err := filepath.Abs(LoadDir())
	if err != nil {
		return "", err
	}
	dirJSON, _ := json.Marshal(dir)
	recorder := strings.Replace(loadRecorderSource, loadsToken, loadsLine+string(dirJSON)+";", 1)
	return strings.TrimRight(body, "\n") + "\n" + recorder, nil
}

// LoadDir is the directory the bridges write their load records to.
func LoadDir() string { return steerpath.StateDir(LoadsComponent) }

// Load is what the load records say about one bridge file.
type Load struct {
	// Known is false when the records could not be read.
	Known bool
	// Loaded is true when a Pi session loaded this file.
	Loaded bool
	// Current is true when the loaded bytes are the file's bytes now.
	Current bool
}

type loadRecord struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

// LoadOf reads the newest load record for the bridge at path. A bridge Pi never
// loaded, or one whose directory holds no record, is known and not loaded.
func LoadOf(path string) Load {
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return Load{Known: errors.Is(err, fs.ErrNotExist)}
	}
	entries, err := os.ReadDir(LoadDir())
	if errors.Is(err, fs.ErrNotExist) {
		return Load{Known: true}
	}
	if err != nil {
		return Load{}
	}
	content, err := os.ReadFile(target)
	if err != nil {
		return Load{}
	}
	sum := sha256.Sum256(content)
	digest := hex.EncodeToString(sum[:])
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(LoadDir(), e.Name()))
		if err != nil {
			continue
		}
		var rec loadRecord
		if json.Unmarshal(raw, &rec) != nil || rec.Path == "" {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(rec.Path); err != nil || resolved != target {
			continue
		}
		return Load{Known: true, Loaded: true, Current: rec.Digest == digest}
	}
	return Load{Known: true}
}

// Trust is a project's saved trust decision in Pi.
type Trust int

const (
	// TrustUnread means Pi's trust file could not be read.
	TrustUnread Trust = iota
	// TrustUndecided means no saved decision covers the project, so Pi asks.
	TrustUndecided
	// TrustGranted means the nearest saved decision trusts the project.
	TrustGranted
	// TrustDeclined means the nearest saved decision declines the project.
	TrustDeclined
)

// ProjectTrust reads the nearest saved decision for dir in Pi's trust.json, as Pi
// does: from dir's resolved path up through its parents.
func ProjectTrust(dir string) Trust {
	entry, _ := harness.Get(harness.Pi)
	agent, err := entry.ConfigDir(harness.GlobalScope, "")
	if err != nil {
		return TrustUnread
	}
	raw, err := os.ReadFile(filepath.Join(agent, "trust.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return TrustUndecided
	}
	var decisions map[string]*bool
	if err != nil || json.Unmarshal(raw, &decisions) != nil {
		return TrustUnread
	}
	current, err := filepath.Abs(dir)
	if err != nil {
		return TrustUnread
	}
	if resolved, err := filepath.EvalSymlinks(current); err == nil {
		current = resolved
	}
	for {
		if d, ok := decisions[current]; ok && d != nil {
			if *d {
				return TrustGranted
			}
			return TrustDeclined
		}
		parent := filepath.Dir(current)
		if parent == current {
			return TrustUndecided
		}
		current = parent
	}
}

// currentDigest names the body this binary writes for each bridge file.
func currentDigest(name string) string {
	switch name {
	case guardBridgeName:
		return GuardBridgeDigest()
	case gateBridgeName:
		return GateBridgeDigest()
	case checkBridgeName:
		return CheckBridgeDigest()
	}
	return ""
}

// BridgeCurrent reports whether the bridge at path carries the body this binary
// writes, and so its load recorder.
func BridgeCurrent(path string) bool {
	content, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	got, ok := parseBridge(content)
	return ok && got.Bridge == currentDigest(filepath.Base(path))
}

// RefreshBridges rewrites each Steer bridge at scope whose stamp names a body other
// than this binary's, keeping the commands and timeouts it holds, so every bridge
// carries the current body and its load recorder. It returns the rewritten paths, and
// every rewrite that failed, after trying each bridge.
func RefreshBridges(scope, cwd string) ([]string, error) {
	dir, err := ExtensionsDir(scope, cwd)
	if err != nil {
		return nil, err
	}
	var out []string
	var failed []error
	for _, name := range []string{guardBridgeName, gateBridgeName, checkBridgeName} {
		path := filepath.Join(dir, name)
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		got, ok := parseBridge(content)
		if !ok || got.Bridge == currentDigest(name) {
			continue
		}
		switch name {
		case guardBridgeName:
			_, err = RegisterMaster(scope, cwd)
		case gateBridgeName:
			g, b := CommitGateArming(scope, cwd), ResolverArming(scope, cwd)
			if !g.Registered || !b.Registered {
				continue
			}
			_, err = RegisterCommitGateAndResolver(scope, cwd, g.Command, g.Timeout, b.Command, b.Timeout)
		case checkBridgeName:
			c := CheckRunnerArming(scope, cwd)
			if !c.Registered {
				continue
			}
			_, err = RegisterCheckRunner(scope, cwd, c.Command, c.Timeout)
		}
		if err != nil {
			failed = append(failed, err)
			continue
		}
		out = append(out, path)
	}
	return out, errors.Join(failed...)
}
