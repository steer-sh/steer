package pi

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
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
)

//go:embed bridge_load.ts
var loadRecorderSource string

// bridgeDigest identifies a bridge body together with the load recorder every
// bridge carries.
func bridgeDigest(body string) string {
	sum := sha256.Sum256([]byte(strings.TrimRight(body, "\n") + "\n" + loadRecorderSource))
	return hex.EncodeToString(sum[:])[:8]
}

// withLoadRecorder appends the load recorder to a bridge body.
func withLoadRecorder(body string) string {
	return strings.TrimRight(body, "\n") + "\n" + loadRecorderSource
}

// LoadDir is the directory the bridges write their load records to. bridge_load.ts
// names the same directory under the home of the user who runs Pi.
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

// LoadOf reads the load record for the bridge at path. A bridge no record names is
// known and not loaded, unless a record could not be read: that one may name it, so
// the answer is unknown.
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
	unread := false
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(LoadDir(), e.Name()))
		if err != nil {
			unread = true
			continue
		}
		var rec loadRecord
		if json.Unmarshal(raw, &rec) != nil || rec.Path == "" || rec.Digest == "" {
			unread = true
			continue
		}
		if resolved, err := filepath.EvalSymlinks(rec.Path); err != nil || resolved != target {
			continue
		}
		return Load{Known: true, Loaded: true, Current: rec.Digest == digest}
	}
	return Load{Known: !unread}
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

// Running is what Pi's records say about whether Pi runs one Steer extension.
type Running int

const (
	// RunningUnknown means a record could not be read, or the extension predates
	// the load recorder and so leaves none.
	RunningUnknown Running = iota
	// RunningCurrent means a Pi session loaded the extension's current bytes.
	RunningCurrent
	// RunningEarlier means a Pi session loaded earlier bytes of the extension.
	RunningEarlier
	// RunningNone means no Pi session has loaded the extension.
	RunningNone
	// RunningDeclined means Pi's trust file declines the project that holds it.
	RunningDeclined
)

// RunsNothing reports a state in which Pi is known to run none of the extension.
func (r Running) RunsNothing() bool {
	return r == RunningNone || r == RunningDeclined
}

// RunningOf reads which bytes of the extension at path Pi's newest session start
// ran. A project extension is declined while Pi's trust declines the project at
// cwd. A Pi process that imported earlier and never ran /reload is invisible to it.
func RunningOf(scope, cwd, path string) Running {
	if path == "" {
		return RunningUnknown
	}
	if scope == harness.ProjectScope {
		root, err := steerpath.ProjectConfigDir(cwd)
		if err != nil {
			return RunningUnknown
		}
		switch ProjectTrust(root) {
		case TrustDeclined:
			return RunningDeclined
		case TrustUnread:
			return RunningUnknown
		}
	}
	if !BridgeCurrent(path) {
		return RunningUnknown
	}
	load := LoadOf(path)
	switch {
	case !load.Known:
		return RunningUnknown
	case !load.Loaded:
		return RunningNone
	case !load.Current:
		return RunningEarlier
	}
	return RunningCurrent
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
