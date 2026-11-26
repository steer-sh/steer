package status

import (
	"fmt"

	"github.com/steer-sh/steer-cli/internal/claude"
	"github.com/steer-sh/steer-cli/internal/steerpath"
)

// SleepRefusal is why hook cannot sleep, naming the rebuild that lets it, or ""
// when every registered master that runs hook honors sleep records of version.
// It reads the global and the working directory's project registrations. A master
// whose guards cannot be read counts as running hook.
func SleepRefusal(hook string, version int) string {
	rows := MasterRuntimes(claude.GlobalScope, claude.ProjectScope)
	for _, m := range rows {
		if m.Path == "" {
			continue
		}
		id, err := claude.ReadMasterFile(resolveRegistered(m.Path, m.Scope))
		if id.SleepVersion == version {
			continue
		}
		if _, runs := id.Hooks[hook]; err == nil && !runs {
			continue
		}
		if m.Scope == claude.ProjectScope {
			root, _ := steerpath.ProjectRootOf("")
			return fmt.Sprintf("the project guard runner in %s predates sleep — run there: %s", root, RebuildRepair(rows, m.Scope))
		}
		return fmt.Sprintf("the %s guard runner predates sleep — run: %s", m.Scope, RebuildRepair(rows, m.Scope))
	}
	return ""
}
