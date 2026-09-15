package plugindist

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/steer-sh/steer-cli/internal/tier"
)

const routingGuide = "skills/steer/prompts/routing.md"

// The routing guide names each rung the way the status display does, one table row each.
func TestRoutingGuideNamesEveryRung(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), routingGuide))
	if err != nil {
		t.Fatalf("read %s: %v", routingGuide, err)
	}
	for _, k := range []tier.Kind{tier.Check, tier.Guard, tier.Instruction, tier.Review} {
		row := "\n| " + tier.Name(k) + " |"
		if !strings.Contains(string(raw), row) {
			t.Errorf("%s has no table row for the %s rung, named %q", routingGuide, k, tier.Name(k))
		}
	}
}

var siblingRef = regexp.MustCompile("\\.\\./[A-Za-z0-9_-]+/[A-Za-z0-9_./-]*[A-Za-z0-9_-]")

// A skill that reads a sibling skill's file names a path that exists in the bundle.
func TestSiblingSkillReferencesResolve(t *testing.T) {
	root := repoRoot(t)
	found := 0
	for _, rel := range pluginManifests {
		skills, _ := readJSON(t, filepath.Join(root, rel))["skills"].([]any)
		for _, s := range skills {
			dir, _ := s.(string)
			dir = strings.TrimPrefix(dir, "./")
			raw, err := os.ReadFile(filepath.Join(root, dir, "SKILL.md"))
			if err != nil {
				t.Fatalf("read %s: %v", dir, err)
			}
			for _, ref := range siblingRef.FindAllString(string(raw), -1) {
				found++
				if _, err := os.Stat(filepath.Join(root, dir, ref)); err != nil {
					t.Errorf("%s/SKILL.md names %s, which the bundle does not hold: %v", dir, ref, err)
				}
			}
		}
	}
	if found == 0 {
		t.Errorf("no skill reads a sibling's file; %s is read by /steer-report as ../steer/prompts/routing.md", routingGuide)
	}
}
