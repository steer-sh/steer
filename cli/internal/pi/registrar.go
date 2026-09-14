// Package pi adapts Pi's auto-discovered extensions to Steer's guard lifecycle.
package pi

import (
	"bufio"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/hookstore"
	"github.com/steer-sh/steer-cli/internal/instructions"
	"github.com/steer-sh/steer-cli/internal/version"
)

const (
	guardBridgeName = "steer-guards.ts"
	bridgeStamp     = "// steer-bridge: "
	masterLine      = "const STEER_MASTER = "
	masterToken     = `const STEER_MASTER = "__STEER_MASTER__";`
)

//go:embed bridge_guards.ts
var guardBridgeSource string

type stamp struct {
	Bridge  string `json:"bridge"`
	Version string `json:"version"`
	Channel string `json:"channel"`
}

// GuardBridgeDigest identifies the TypeScript body baked into a Pi guard bridge.
func GuardBridgeDigest() string {
	sum := sha256.Sum256([]byte(strings.TrimRight(guardBridgeSource, "\n") + "\n"))
	return hex.EncodeToString(sum[:])[:8]
}

// ExtensionsDir names the auto-discovered extension directory for a scope.
func ExtensionsDir(scope, cwd string) (string, error) {
	entry, _ := harness.Get(harness.Pi)
	dir, err := entry.ConfigDir(scope, cwd)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "extensions"), nil
}

// InstructionFile names Pi's AGENTS.md for a scope.
func InstructionFile(scope, cwd string) (string, error) {
	entry, _ := harness.Get(harness.Pi)
	if scope == harness.ProjectScope {
		return filepath.Join(cwd, entry.InstructionFile), nil
	}
	dir, err := entry.ConfigDir(scope, cwd)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, entry.InstructionFile), nil
}

func bridgeContent(master string) ([]byte, error) {
	header, err := json.Marshal(stamp{
		Bridge: GuardBridgeDigest(), Version: version.Version, Channel: "guards",
	})
	if err != nil {
		return nil, err
	}
	masterJSON, err := json.Marshal(master)
	if err != nil {
		return nil, err
	}
	source := strings.TrimRight(guardBridgeSource, "\n") + "\n"
	if strings.Count(source, masterToken) != 1 {
		return nil, fmt.Errorf("Pi guard bridge carries no unique master placeholder")
	}
	source = strings.Replace(source, masterToken, masterLine+string(masterJSON)+";", 1)
	return []byte(bridgeStamp + string(header) + "\n" + source), nil
}

func parseBridge(content []byte) (stamp, bool) {
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	for i := 0; i < 20 && scanner.Scan(); i++ {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, bridgeStamp) {
			continue
		}
		var got stamp
		if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, bridgeStamp))), &got); err != nil {
			return stamp{}, false
		}
		return got, got.Bridge != "" && got.Channel != ""
	}
	return stamp{}, false
}

func masterFromBridge(content []byte) string {
	if got, ok := parseBridge(content); !ok || got.Channel != "guards" {
		return ""
	}
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, masterLine) || !strings.HasSuffix(line, ";") {
			continue
		}
		encoded := strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(line, masterLine)), ";")
		var path string
		if json.Unmarshal([]byte(encoded), &path) == nil {
			return path
		}
	}
	return ""
}

// RegisterMaster writes Pi's guard extension with the master path baked into it.
func RegisterMaster(scope, cwd string) (string, error) {
	dir, err := ExtensionsDir(scope, cwd)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, guardBridgeName)
	if existing, readErr := os.ReadFile(path); readErr == nil {
		if got, ok := parseBridge(existing); !ok || got.Channel != "guards" {
			return path, fmt.Errorf("refusing to replace unowned Pi extension %s", path)
		}
	} else if !os.IsNotExist(readErr) {
		return path, readErr
	}
	master, err := hookstore.MasterPath(scope)
	if err != nil {
		return path, err
	}
	// A project path stays relative to the repository root: the bridge runs the
	// master with Pi's working directory, which is the directory holding .pi/.
	if scope == harness.ProjectScope && !filepath.IsAbs(master) {
		master = filepath.ToSlash(master)
	} else if master, err = filepath.Abs(master); err != nil {
		return path, err
	}
	content, err := bridgeContent(master)
	if err != nil {
		return path, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return path, err
	}
	if err := writeBridge(path, content); err != nil {
		return path, err
	}
	return path, nil
}

func writeBridge(path string, content []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".steer-bridge.*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// RemoveMaster deletes only the guard bridge Steer stamped as its own.
func RemoveMaster(scope, cwd string) (string, error) {
	dir, err := ExtensionsDir(scope, cwd)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, guardBridgeName)
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return path, nil
	}
	if err != nil {
		return path, err
	}
	if got, ok := parseBridge(content); !ok || got.Channel != "guards" {
		return path, fmt.Errorf("refusing to remove unowned Pi extension %s", path)
	}
	return path, os.Remove(path)
}

// Registrar implements the shared guard lifecycle for Pi.
type Registrar struct{}

func (Registrar) Harness() harness.Harness {
	h, _ := harness.Get(harness.Pi)
	return h
}

func (Registrar) RegisterMaster(scope string) error {
	_, err := RegisterMaster(scope, ".")
	return err
}

func (Registrar) RemoveMaster(scope string) error {
	_, err := RemoveMaster(scope, ".")
	return err
}

func (Registrar) SetRules(rules []string, scope string) error {
	path, err := InstructionFile(scope, ".")
	if err != nil {
		return err
	}
	return instructions.SetRules(path, rules)
}

func (Registrar) InstructionPath(scope string) (string, error) {
	return InstructionFile(scope, ".")
}

func (Registrar) MasterRegistered(scope string) (bool, error) {
	path, err := (Registrar{}).RegisteredMasterPath(scope)
	return path != "", err
}

// RegisteredMasterPath reads the master path baked into Pi's guard extension.
func (Registrar) RegisteredMasterPath(scope string) (string, error) {
	dir, err := ExtensionsDir(scope, ".")
	if err != nil {
		return "", err
	}
	content, err := os.ReadFile(filepath.Join(dir, guardBridgeName))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return masterFromBridge(content), nil
}

var _ harness.Registrar = Registrar{}
