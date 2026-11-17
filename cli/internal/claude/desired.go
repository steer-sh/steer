package claude

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/steer-sh/steer-cli/internal/harness"
	"github.com/steer-sh/steer-cli/internal/instructions"
)

// DriftHarness means the store is correct but a target harness does not carry
// the registration or the rule. One store serves every harness, so store-only
// drift checks report a hook as installed that a second harness never sees.
const DriftHarness = "harness"

// DesiredHook is the target state for one managed hook that Steer itself owns.
type DesiredHook struct {
	HookName string
	FileName string
	Rule     string
	Content  []byte
	Metadata HookInstallMetadata
}

// ContentSHA256 returns the hex sha256 of content.
func ContentSHA256(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// DesiredFileContent returns the exact bytes a desired hook should have on
// disk after install (metadata header applied).
func DesiredFileContent(desired DesiredHook) ([]byte, error) {
	metadata, err := normalizeInstallMetadata(desired.Metadata)
	if err != nil {
		return nil, err
	}
	return applyMetadataHeader(desired.Content, metadata.Source, metadata.PromptVersion, metadata.HookVersion), nil
}

// TargetDrift reports whether any target harness is missing this hook's master
// registration or its rule line. The guard store is shared, so a store that
// matches proves nothing about a harness that was never registered into.
func TargetDrift(scope, rule string, targets []harness.Registrar) (bool, string, error) {
	for _, target := range harness.Targets(targets, Registrar{}) {
		registered, err := target.MasterRegistered(scope)
		if err != nil {
			return false, "", err
		}
		if !registered {
			return true, DriftHarness, nil
		}
		path, err := target.InstructionPath(scope)
		if err != nil {
			return false, "", err
		}
		present, err := instructions.HasRule(path, rule)
		if err != nil {
			return false, "", err
		}
		if !present {
			return true, DriftHarness, nil
		}
	}
	return false, "", nil
}
