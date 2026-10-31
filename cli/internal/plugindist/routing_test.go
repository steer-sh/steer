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

var siblingRef = regexp.MustCompile("(?:\\.\\./)+[A-Za-z0-9_-]+/[A-Za-z0-9_./-]*[A-Za-z0-9_-]")

// A skill file that reads a sibling skill's file names a path that exists in the bundle.
func TestSiblingSkillReferencesResolve(t *testing.T) {
	root := repoRoot(t)
	found := 0
	for _, rel := range pluginManifests {
		skills, _ := readJSON(t, filepath.Join(root, rel))["skills"].([]any)
		for _, s := range skills {
			dir, _ := s.(string)
			dir = strings.TrimPrefix(dir, "./")
			prompts, _ := filepath.Glob(filepath.Join(root, dir, "prompts", "*.md"))
			for _, file := range append([]string{filepath.Join(root, dir, "SKILL.md")}, prompts...) {
				raw, err := os.ReadFile(file)
				if err != nil {
					t.Fatalf("read %s: %v", file, err)
				}
				for _, ref := range siblingRef.FindAllString(string(raw), -1) {
					found++
					if _, err := os.Stat(filepath.Join(filepath.Dir(file), ref)); err != nil {
						t.Errorf("%s names %s, which the bundle does not hold: %v", file, ref, err)
					}
				}
			}
		}
	}
	if found == 0 {
		t.Errorf("no skill reads a sibling's file; %s is read by /steer-report as ../steer/prompts/routing.md", routingGuide)
	}
}

var ownRef = regexp.MustCompile(`(?:^|[^.\w])\./([A-Za-z0-9_-][A-Za-z0-9_./-]*[A-Za-z0-9_-])`)

// A skill reads each file beside its SKILL.md through a ./ path, and every ./ path
// a skill file names is a file the skill holds.
func TestSkillOwnFileReferencesResolve(t *testing.T) {
	root := repoRoot(t)
	dirs := map[string]bool{}
	for _, rel := range pluginManifests {
		skills, _ := readJSON(t, filepath.Join(root, rel))["skills"].([]any)
		for _, s := range skills {
			dir, _ := s.(string)
			dirs[filepath.Join(root, strings.TrimPrefix(dir, "./"))] = true
		}
	}
	for dir := range dirs {
		files, _ := filepath.Glob(filepath.Join(dir, "*.md"))
		named := map[string]bool{}
		for _, file := range files {
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("read %s: %v", file, err)
			}
			for _, m := range ownRef.FindAllStringSubmatch(string(raw), -1) {
				named[filepath.Clean(m[1])] = true
				if _, err := os.Stat(filepath.Join(dir, m[1])); err != nil {
					t.Errorf("%s names ./%s, which the skill does not hold: %v", file, m[1], err)
				}
			}
		}
		for _, file := range files {
			if base := filepath.Base(file); base != "SKILL.md" && !named[base] {
				t.Errorf("%s is named by no ./ path in its skill, so no step reads it", file)
			}
		}
	}
}
