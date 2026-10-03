package pi

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/harness"
)

func TestProjectBridgeMutationsRefuseLinkedConfiguration(t *testing.T) {
	operations := []struct {
		name string
		file string
		run  func(string) error
	}{
		{"register guards", guardBridgeName, func(project string) error {
			_, err := RegisterMaster(harness.ProjectScope, project)
			return err
		}},
		{"remove guards", guardBridgeName, func(project string) error {
			_, err := RemoveMaster(harness.ProjectScope, project)
			return err
		}},
		{"register gate", gateBridgeName, func(project string) error {
			_, err := RegisterCommitGateAndResolver(harness.ProjectScope, project, "steer commit-gate review-pi", 360, "steer commit-gate backfill", 30)
			return err
		}},
		{"remove gate", gateBridgeName, func(project string) error {
			_, err := RemoveCommitGateAndResolver(harness.ProjectScope, project)
			return err
		}},
	}
	for _, linkKind := range []string{"config directory", "extensions directory", "bridge file"} {
		for _, op := range operations {
			t.Run(linkKind+"/"+op.name, func(t *testing.T) {
				isolateHome(t)
				project, external := t.TempDir(), t.TempDir()
				outside := filepath.Join(external, "extensions")
				if err := os.Mkdir(outside, 0o755); err != nil {
					t.Fatal(err)
				}
				guard, err := bridgeContent("/operator/global-master.py")
				if err != nil {
					t.Fatal(err)
				}
				gate, err := gateBridgeContent("/operator/steer commit-gate review-pi", 360, "/operator/steer commit-gate backfill", 30)
				if err != nil {
					t.Fatal(err)
				}
				originals := map[string][]byte{guardBridgeName: guard, gateBridgeName: gate}
				for name, content := range originals {
					if err := os.WriteFile(filepath.Join(outside, name), content, 0o644); err != nil {
						t.Fatal(err)
					}
				}
				config := filepath.Join(project, ".pi")
				link, target := config, external
				switch linkKind {
				case "extensions directory":
					if err := os.Mkdir(config, 0o755); err != nil {
						t.Fatal(err)
					}
					link, target = filepath.Join(config, "extensions"), outside
				case "bridge file":
					extensions := filepath.Join(config, "extensions")
					if err := os.MkdirAll(extensions, 0o755); err != nil {
						t.Fatal(err)
					}
					link, target = filepath.Join(extensions, op.file), filepath.Join(outside, op.file)
				}
				if err := os.Symlink(target, link); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
				if err := op.run(project); err == nil || !strings.Contains(err.Error(), "symlink") {
					t.Fatalf("mutation error = %v, want project symlink refusal", err)
				}
				for name, original := range originals {
					got, err := os.ReadFile(filepath.Join(outside, name))
					if err != nil || !bytes.Equal(got, original) {
						t.Fatalf("external %s changed: %v", name, err)
					}
				}
				if got, err := os.Readlink(link); err != nil || got != target {
					t.Fatalf("project link changed: %q, %v", got, err)
				}
				if files, err := os.ReadDir(outside); err != nil || len(files) != 2 {
					t.Fatalf("external directory gained temporary files: %v, %v", files, err)
				}
			})
		}
	}
}

// TestGlobalBridgeMutationsKeepOperatorLinkedDirectories pins existing behaviour.
func TestGlobalBridgeMutationsKeepOperatorLinkedDirectories(t *testing.T) {
	for _, linkKind := range []string{"config directory", "extensions directory"} {
		t.Run(linkKind, func(t *testing.T) {
			isolateHome(t)
			config, external := filepath.Join(t.TempDir(), "pi-home"), t.TempDir()
			link := config
			if linkKind == "extensions directory" {
				if err := os.Mkdir(config, 0o755); err != nil {
					t.Fatal(err)
				}
				link = filepath.Join(config, "extensions")
			}
			if err := os.Symlink(external, link); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			t.Setenv("PI_CODING_AGENT_DIR", config)
			guard, err := RegisterMaster(harness.GlobalScope, ".")
			if err != nil {
				t.Fatal(err)
			}
			gate, err := RegisterCommitGateAndResolver(harness.GlobalScope, ".", "steer commit-gate review-pi", 360, "steer commit-gate backfill", 30)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := RemoveMaster(harness.GlobalScope, "."); err != nil {
				t.Fatal(err)
			}
			if _, err := RemoveCommitGateAndResolver(harness.GlobalScope, "."); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{guard, gate} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("global bridge was not removed: %v", err)
				}
			}
			if got, err := os.Readlink(link); err != nil || got != external {
				t.Fatalf("operator directory link changed: %q, %v", got, err)
			}
		})
	}
}
