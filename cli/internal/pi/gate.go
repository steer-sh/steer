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
	gateBridgeName  = "steer-commit-gate.ts"
	reviewLine      = "const STEER_REVIEW = "
	backfillLine    = "const STEER_BACKFILL = "
	reviewToken     = `const STEER_REVIEW = "__STEER_REVIEW__";`
	backfillToken   = `const STEER_BACKFILL = "__STEER_BACKFILL__";`
	reviewTimeout   = "__STEER_REVIEW_TIMEOUT_MS__"
	backfillTimeout = "__STEER_BACKFILL_TIMEOUT_MS__"
)

//go:embed bridge_gate.ts
var gateBridgeSource string

// GateBridgeDigest identifies the TypeScript body baked into a Pi commit-gate bridge.
func GateBridgeDigest() string {
	sum := sha256.Sum256([]byte(strings.TrimRight(gateBridgeSource, "\n") + "\n"))
	return hex.EncodeToString(sum[:])[:8]
}

// GateArming is what one Pi extension says about the review and resolver pair.
type GateArming struct {
	Registered bool
	Timeout    int
	Path       string
	Command    string
}

func gateBridgeContent(review string, reviewSeconds int, backfill string, backfillSeconds int) ([]byte, error) {
	header, err := json.Marshal(stamp{
		Bridge: GateBridgeDigest(), Version: version.Version, Channel: "commit-gate",
	})
	if err != nil {
		return nil, err
	}
	reviewJSON, _ := json.Marshal(review)
	backfillJSON, _ := json.Marshal(backfill)
	source := strings.TrimRight(gateBridgeSource, "\n") + "\n"
	for token, replacement := range map[string]string{
		reviewToken:     reviewLine + string(reviewJSON) + ";",
		backfillToken:   backfillLine + string(backfillJSON) + ";",
		reviewTimeout:   strconv.Itoa(reviewSeconds * 1000),
		backfillTimeout: strconv.Itoa(backfillSeconds * 1000),
	} {
		if strings.Count(source, token) != 1 {
			return nil, fmt.Errorf("Pi commit-gate bridge carries no unique %q placeholder", token)
		}
		source = strings.Replace(source, token, replacement, 1)
	}
	return []byte(bridgeStamp + string(header) + "\n" + source), nil
}

// RegisterCommitGateAndResolver writes both Pi handlers in one extension replacement.
func RegisterCommitGateAndResolver(scope, cwd, review string, reviewSeconds int,
	backfill string, backfillSeconds int) (string, error) {
	if !strings.Contains(review, "commit-gate review-pi") {
		return "", fmt.Errorf("refusing to register %q as Pi's commit gate", review)
	}
	if !strings.Contains(backfill, "commit-gate backfill") {
		return "", fmt.Errorf("refusing to register %q as Pi's resolver", backfill)
	}
	dir, err := ExtensionsDir(scope, cwd)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, gateBridgeName)
	if err := refuseLinkedBridge(scope, path); err != nil {
		return path, err
	}
	if existing, readErr := os.ReadFile(path); readErr == nil {
		if got, ok := parseBridge(existing); !ok || got.Channel != "commit-gate" {
			return path, fmt.Errorf("refusing to replace unowned Pi extension %s", path)
		}
	} else if !os.IsNotExist(readErr) {
		return path, readErr
	}
	content, err := gateBridgeContent(review, reviewSeconds, backfill, backfillSeconds)
	if err != nil {
		return path, err
	}
	return path, writeBridge(scope, path, content)
}

// RemoveCommitGateAndResolver removes only Steer's stamped Pi gate extension.
// It reports whether the extension was there.
func RemoveCommitGateAndResolver(scope, cwd string) (string, bool, error) {
	dir, err := ExtensionsDir(scope, cwd)
	if err != nil {
		return "", false, err
	}
	path := filepath.Join(dir, gateBridgeName)
	if err := refuseLinkedBridge(scope, path); err != nil {
		return path, false, err
	}
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return path, false, nil
	}
	if err != nil {
		return path, false, err
	}
	if got, ok := parseBridge(content); !ok || got.Channel != "commit-gate" {
		return path, false, fmt.Errorf("refusing to remove unowned Pi extension %s", path)
	}
	if err := os.Remove(path); err != nil {
		return path, false, err
	}
	return path, true, nil
}

// CommitGateArming reads the review half of Pi's atomic extension registration.
func CommitGateArming(scope, cwd string) GateArming {
	return gateArming(scope, cwd, reviewLine, "STEER_REVIEW_TIMEOUT_MS")
}

// ResolverArming reads the turn-end half of Pi's atomic extension registration.
func ResolverArming(scope, cwd string) GateArming {
	return gateArming(scope, cwd, backfillLine, "STEER_BACKFILL_TIMEOUT_MS")
}

func gateArming(scope, cwd, commandLine, timeoutName string) GateArming {
	dir, err := ExtensionsDir(scope, cwd)
	if err != nil {
		return GateArming{}
	}
	path := filepath.Join(dir, gateBridgeName)
	content, err := os.ReadFile(path)
	if err != nil {
		return GateArming{Path: path}
	}
	if got, ok := parseBridge(content); !ok || got.Channel != "commit-gate" {
		return GateArming{Path: path}
	}
	command := bridgeString(content, commandLine)
	timeoutMS := bridgeInt(content, "const "+timeoutName+" = ")
	return GateArming{
		Registered: command != "", Timeout: timeoutMS / 1000, Path: path, Command: command,
	}
}

func bridgeString(content []byte, prefix string) string {
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, ";") {
			continue
		}
		var value string
		encoded := strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(line, prefix)), ";")
		if json.Unmarshal([]byte(encoded), &value) == nil {
			return value
		}
	}
	return ""
}

func bridgeInt(content []byte, prefix string) int {
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) && strings.HasSuffix(line, ";") {
			value := strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(line, prefix)), ";")
			n, _ := strconv.Atoi(value)
			return n
		}
	}
	return 0
}
