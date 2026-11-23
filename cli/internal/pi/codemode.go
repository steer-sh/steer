package pi

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/steer-sh/steer-cli/internal/harness"
)

type piSettings struct {
	Codemode struct {
		Mode string `json:"mode"`
	} `json:"codemode"`
}

// CodemodeOnly returns the Pi settings file whose `codemode.mode` of "only" is in
// effect for a Pi started in cwd, or "". While codemode is active, that mode hides
// the top-level bash tool, and the commit gate refuses a commit made inside a
// codemode script. cwd's `.pi/settings.json` overrides the agent directory's only
// where Pi's saved decision trusts cwd; an unreadable file counts as empty.
func CodemodeOnly(cwd string) string {
	var paths []string
	if pi, ok := harness.Get(harness.Pi); ok {
		if dir, err := pi.ConfigDir(harness.GlobalScope, ""); err == nil {
			paths = append(paths, filepath.Join(dir, "settings.json"))
		}
	}
	if ProjectTrust(cwd) == TrustGranted {
		paths = append(paths, filepath.Join(cwd, ".pi", "settings.json"))
	}
	mode, modePath := "", ""
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var s piSettings
		if json.Unmarshal(bytes.TrimPrefix(raw, []byte("\xEF\xBB\xBF")), &s) != nil {
			continue
		}
		if s.Codemode.Mode != "" {
			mode, modePath = s.Codemode.Mode, path
		}
	}
	if mode != "only" {
		return ""
	}
	return modePath
}
