package gate

import (
	"path"
	"sort"
	"strings"
)

// Extensions whose content is a RECORD, not a SPECIFICATION. A commit touching only
// these skips the gate. Deliberately short: anything not listed is reviewed, so the
// failure mode of an omission is a wasted review rather than a missed defect.
var proseSuffixes = map[string]bool{
	".md": true, ".markdown": true, ".rst": true, ".txt": true, ".adoc": true, ".org": true,
}

// Paths that look like prose but are executed, so they are reviewed like code.
var proseExceptions = map[string]bool{"CLAUDE.md": true, "AGENTS.md": true}

// Generated artifacts whose hunks no reviewer can evaluate. Their PATHS are still
// reported to the reviewer; only the hunks are dropped. One package-lock.json exceeds
// MaxDiffChars by itself, and the degraded mode that follows costs the real code in
// the same commit its diff text — so this filter protects the budget rather than
// saving tokens.
var noiseBasenames = map[string]bool{
	"bun.lock": true, "bun.lockb": true, "package-lock.json": true, "yarn.lock": true,
	"pnpm-lock.yaml": true, "Cargo.lock": true, "go.sum": true, "poetry.lock": true,
	"Pipfile.lock": true, "flake.lock": true, "uv.lock": true, "composer.lock": true,
	"Gemfile.lock": true, "mix.lock": true, "packages.lock.json": true,
}

var noiseSuffixes = []string{".min.js", ".min.css", ".bundle.js", ".map"}

// OS metadata files, dropped from the review WITHOUT being named. noiseBasenames is
// the wrong home: that filter names what it drops, and a named .DS_Store is what
// three reviewers keep raising as a finding.
var osCruftBasenames = map[string]bool{
	".DS_Store": true, "Thumbs.db": true, "desktop.ini": true, "ehthumbs.db": true,
}

// Directories. Git lists the files inside them, never the directory itself, so these
// match a path component rather than the basename.
var osCruftDirs = map[string]bool{
	".Spotlight-V100": true, ".Trashes": true, ".fseventsd": true, "__MACOSX": true,
}

// IsNoise reports whether a path is a generated artifact.
func IsNoise(p string) bool {
	name := path.Base(strings.ReplaceAll(p, "\\", "/"))
	if noiseBasenames[name] {
		return true
	}
	for _, suffix := range noiseSuffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// IsOSCruft reports whether a path is OS metadata, which never reaches a reviewer in
// any form.
func IsOSCruft(p string) bool {
	parts := strings.Split(strings.ReplaceAll(p, "\\", "/"), "/")
	name := parts[len(parts)-1]
	if osCruftBasenames[name] || strings.HasPrefix(name, "._") {
		return true
	}
	for _, dir := range parts[:len(parts)-1] {
		if osCruftDirs[dir] {
			return true
		}
	}
	return false
}

// IsProseOnly reports whether every changed path is a record rather than a
// specification. An empty list returns false so an unreadable file list can never
// silently skip the gate.
func IsProseOnly(paths []string) bool {
	if len(paths) == 0 {
		return false
	}
	for _, p := range paths {
		name := path.Base(strings.ReplaceAll(p, "\\", "/"))
		if proseExceptions[name] {
			return false
		}
		if !proseSuffixes[strings.ToLower(path.Ext(name))] {
			return false
		}
	}
	return true
}

// dropCruft removes OS metadata from a path list, preserving order.
func dropCruft(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if !IsOSCruft(p) {
			out = append(out, p)
		}
	}
	return out
}

func sortedUnique(items []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if !seen[item] {
			seen[item] = true
			out = append(out, item)
		}
	}
	sort.Strings(out)
	return out
}
