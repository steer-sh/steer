package pi

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/steer-sh/steer-cli/internal/version"
)

const (
	checkBridgeName  = "steer-checks.ts"
	checkChannel     = "checks"
	checkLine        = "const STEER_CHECK = "
	checkToken       = `const STEER_CHECK = "__STEER_CHECK__";`
	checkTimeoutName = "STEER_CHECK_TIMEOUT_MS"
	checkTimeout     = "__STEER_CHECK_TIMEOUT_MS__"
)

//go:embed bridge_checks.ts
var checkBridgeSource string

// CheckBridgeDigest identifies the TypeScript body baked into a Pi check bridge.
func CheckBridgeDigest() string {
	sum := sha256.Sum256([]byte(strings.TrimRight(checkBridgeSource, "\n") + "\n"))
	return hex.EncodeToString(sum[:])[:8]
}

func checkBridgeContent(command string, seconds int) ([]byte, error) {
	header, err := json.Marshal(stamp{
		Bridge: CheckBridgeDigest(), Version: version.Version, Channel: checkChannel,
	})
	if err != nil {
		return nil, err
	}
	commandJSON, _ := json.Marshal(command)
	source := strings.TrimRight(checkBridgeSource, "\n") + "\n"
	for token, replacement := range map[string]string{
		checkToken:   checkLine + string(commandJSON) + ";",
		checkTimeout: strconv.Itoa(seconds * 1000),
	} {
		if strings.Count(source, token) != 1 {
			return nil, fmt.Errorf("Pi check bridge carries no unique %q placeholder", token)
		}
		source = strings.Replace(source, token, replacement, 1)
	}
	return []byte(bridgeStamp + string(header) + "\n" + source), nil
}

func checkBridgePath(scope, cwd string) (string, error) {
	dir, err := ExtensionsDir(scope, cwd)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, checkBridgeName)
	return path, refuseLinkedBridge(scope, path)
}

// RegisterCheckRunner writes Pi's extension that runs command at the settle
// boundary, Pi's counterpart of a root Stop.
func RegisterCheckRunner(scope, cwd, command string, seconds int) (string, error) {
	if !strings.Contains(command, "check hook --harness pi") {
		return "", fmt.Errorf("refusing to register %q as Pi's check runner", command)
	}
	path, err := checkBridgePath(scope, cwd)
	if err != nil {
		return path, err
	}
	if existing, readErr := os.ReadFile(path); readErr == nil {
		if got, ok := parseBridge(existing); !ok || got.Channel != checkChannel {
			return path, fmt.Errorf("refusing to replace unowned Pi extension %s", path)
		}
	} else if !os.IsNotExist(readErr) {
		return path, readErr
	}
	content, err := checkBridgeContent(command, seconds)
	if err != nil {
		return path, err
	}
	return path, writeBridge(scope, path, content)
}

// RemoveCheckRunner removes only Steer's stamped Pi check extension. It reports
// whether the extension was there.
func RemoveCheckRunner(scope, cwd string) (string, bool, error) {
	path, err := checkBridgePath(scope, cwd)
	if err != nil {
		return path, false, err
	}
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return path, false, nil
	}
	if err != nil {
		return path, false, err
	}
	if got, ok := parseBridge(content); !ok || got.Channel != checkChannel {
		return path, false, fmt.Errorf("refusing to remove unowned Pi extension %s", path)
	}
	if err := os.Remove(path); err != nil {
		return path, false, err
	}
	return path, true, nil
}

// CheckRunnerArming reads Pi's check extension at one scope.
func CheckRunnerArming(scope, cwd string) GateArming {
	dir, err := ExtensionsDir(scope, cwd)
	if err != nil {
		return GateArming{}
	}
	path := filepath.Join(dir, checkBridgeName)
	content, err := os.ReadFile(path)
	if err != nil {
		return GateArming{Path: path}
	}
	if got, ok := parseBridge(content); !ok || got.Channel != checkChannel {
		return GateArming{Path: path}
	}
	command := bridgeString(content, checkLine)
	return GateArming{
		Registered: command != "", Path: path, Command: command,
		Timeout: bridgeInt(content, "const "+checkTimeoutName+" = ") / 1000,
	}
}
