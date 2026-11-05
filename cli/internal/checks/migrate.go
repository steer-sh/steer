package checks

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// PersonalGateScript is the personal Stop hook that ran check.sh before this
// tier existed. Steer recognises it by the script name in a hook command.
const PersonalGateScript = "check-gate.py"

// PersonalApprovalsName is the list the personal gate read to decide which
// repositories it was allowed to run.
const PersonalApprovalsName = "check-gate-approved.txt"

// IsPersonalGateCommand reports whether a hook command runs the personal gate.
func IsPersonalGateCommand(command string) bool {
	return strings.Contains(command, PersonalGateScript)
}

// PersonalApprovalsPath is where the personal gate keeps its approval list.
func PersonalApprovalsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", PersonalApprovalsName), nil
}

// PersonalApprovals reads the approval list in file order, expanding a leading
// ~ and dropping blanks, comments and repeats. A missing list is no approvals.
func PersonalApprovals() ([]string, error) {
	path, err := PersonalApprovalsPath()
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var paths []string
	seen := map[string]bool{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		expanded := expandHome(line)
		if seen[expanded] {
			continue
		}
		seen[expanded] = true
		paths = append(paths, expanded)
	}
	return paths, scanner.Err()
}

func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/"))
}
