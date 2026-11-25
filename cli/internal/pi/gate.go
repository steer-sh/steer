package pi

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/steer-sh/steer-cli/internal/gate/cc"
	"github.com/steer-sh/steer-cli/internal/version"
)

const (
	gateBridgeName  = "steer-commit-gate.ts"
	reviewLine      = "const STEER_REVIEW = "
	backfillLine    = "const STEER_BACKFILL = "
	reviewToken     = `const STEER_REVIEW = "__STEER_REVIEW__";`
	backfillToken   = `const STEER_BACKFILL = "__STEER_BACKFILL__";`
	guidanceToken   = `const STEER_GUIDANCE = "__STEER_GUIDANCE__";`
	reviewTimeout   = "__STEER_REVIEW_TIMEOUT_MS__"
	backfillTimeout = "__STEER_BACKFILL_TIMEOUT_MS__"
)

// CommitGuidance is the commit form a Pi session is taught. Pi runs every
// tool_call handler of an assistant message before any of its calls, so the gate
// refuses a commit that shares its message with a call that can change files.
const CommitGuidance = cc.CommitShapeGuidance + "\n" +
	"- In Pi, make the commit the only tool call in its assistant message, as a " +
	"top-level bash call and never from inside a codemode script: Pi runs the calls " +
	"of one message at the same time."

//go:embed bridge_gate.ts
var gateBridgeSource string

// GateBridgeDigest identifies the TypeScript body baked into a Pi commit-gate bridge.
func GateBridgeDigest() string { return bridgeDigest(gateBridgeSource) }

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
	guidanceJSON, _ := json.Marshal(CommitGuidance)
	source := withLoadRecorder(gateBridgeSource)
	for token, replacement := range map[string]string{
		reviewToken:     reviewLine + string(reviewJSON) + ";",
		backfillToken:   backfillLine + string(backfillJSON) + ";",
		guidanceToken:   "const STEER_GUIDANCE = " + string(guidanceJSON) + ";",
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
