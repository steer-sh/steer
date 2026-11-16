// Package harvests builds the resolver's per-harness transcript readers. It sits
// above the adapters, which import resolve, and below the two callers that start
// a resolve: the CLI's `steer commit-gate backfill` and the dashboard's Channels
// panel. Both read the same set, so a harness readable from one is readable from
// the other.
package harvests

import (
	"os"
	"path/filepath"

	"github.com/steer-sh/steer-cli/internal/gate/cc"
	gatecodex "github.com/steer-sh/steer-cli/internal/gate/codex"
	gatepi "github.com/steer-sh/steer-cli/internal/gate/pi"
	"github.com/steer-sh/steer-cli/internal/gate/resolve"
	transcriptpi "github.com/steer-sh/steer-cli/internal/transcript/pi"
)

// All is one harvest per harness whose transcripts are readable here. A harness
// with no tree contributes nothing rather than failing the run.
func All() []resolve.Harvest {
	var out []resolve.Harvest
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, cc.Harvest(filepath.Join(home, ".claude", "projects")))
	}
	if root, err := gatecodex.SessionsRoot(); err == nil {
		out = append(out, gatecodex.Harvest(root))
	}
	if root, err := transcriptpi.New().Root(); err == nil {
		out = append(out, gatepi.Harvest(root))
	}
	return out
}
