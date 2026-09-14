package gate

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Tags structuring the review prompt. Untrusted text lands between them — the message,
// the diff, whole unchanged files — and any of those can close a section early, turning
// everything after it into instructions.
var promptBoundaryTags = []string{"commit-message", "diff", "unchanged-callers", addendumTag}

var boundaryTagRe = regexp.MustCompile(
	`(?i)</?(?:` + strings.Join(promptBoundaryTags, "|") + `)(?:\s[^>]*)?>`)

// NeutralizeTags defuses prompt boundary tags in untrusted text, preserving what it
// said. Neutralised, not deleted: removing text from a hunk is its own way to
// manufacture a finding about code that does not look like what shipped. `<` becomes
// `&lt;`, which cannot close a section and leaves the real line legible.
func NeutralizeTags(text string) string {
	return boundaryTagRe.ReplaceAllStringFunc(text, func(m string) string {
		return "&lt;" + m[1:]
	})
}

// Symbol definitions the diff may introduce or remove. Module-level bindings only for
// the assignment forms: `^\s*(\w+)\s*=` also matched every indented local, and the js
// form did the same one language over, turning function-scratch names into search
// terms that match most of a real repo.
var defPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\bdef\s+(\w+)`),                                               // python
	regexp.MustCompile(`\bclass\s+(\w+)`),                                             // python, js, java, ...
	regexp.MustCompile(`\bfunction\s+(\w+)`),                                          // js
	regexp.MustCompile(`\b(?:func|fn)\s+(\w+)`),                                       // go, rust
	regexp.MustCompile(`^(?:export\s+(?:default\s+)?)?(?:const|let|var)\s+(\w+)\s*=`), // js/ts
	regexp.MustCompile(`^(\w+)\s*=[^=]`),
}

// ChangedSymbols returns the names the diff defines or redefines, plus the changed
// modules' own basenames. Selection is mechanical, so no human judgement about which
// file matters reaches the reviewer.
//
// Both + and - lines are scanned: a symbol REMOVED or RENAMED is the case that breaks a
// caller. Basenames catch an import that names the file rather than the symbol.
func ChangedSymbols(diff string, paths []string) map[string]bool {
	names := map[string]bool{}
	for _, line := range strings.Split(diff, "\n") {
		if line == "" || (line[0] != '+' && line[0] != '-') {
			continue
		}
		if len(line) >= 3 && (line[:3] == "+++" || line[:3] == "---") {
			continue
		}
		body := line[1:]
		for _, pat := range defPatterns {
			for _, m := range pat.FindAllStringSubmatch(body, -1) {
				if len(m[1]) > 2 { // 1-2 char names match everything
					names[m[1]] = true
				}
			}
		}
	}
	for _, p := range paths {
		if stem := Stem(p); len(stem) > 2 {
			names[stem] = true
		}
	}
	return names
}

// Stem is the basename without its extension, following Python's splitext rule rather
// than filepath.Ext: a LEADING dot is part of the name, not an extension separator. Go's
// Ext reads all of `.gitignore` as the extension and leaves an empty stem, which drops
// every dotfile out of the symbol set the caller attachment searches on.
func Stem(p string) string {
	base := filepath.Base(strings.ReplaceAll(p, "\\", "/"))
	dot := strings.LastIndexByte(base, '.')
	for i := 0; i < dot; i++ {
		if base[i] != '.' {
			return base[:dot]
		}
	}
	return base
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// wordRe matches `symbol` on a word boundary, with the symbol taken literally.
func wordRe(symbol string) *regexp.Regexp {
	return regexp.MustCompile(`\b` + regexp.QuoteMeta(symbol) + `\b`)
}

// indexedReader returns a reader for a file's index content, the version a file
// outside the diff lands with. A file whose working copy matches the index is read
// from disk; one with unstaged edits is read from its index blob. cwd is the work-tree
// root, so paths are root-relative, as `git grep` prints them there.
func indexedReader(cwd string) func(path string) ([]byte, error) {
	dirty := map[string]bool{}
	if out, ok := Git(cwd, gitMedium, "diff", "--name-only", "--relative", "-z"); ok {
		for _, path := range strings.Split(out, "\x00") {
			dirty[path] = path != ""
		}
	}
	return func(path string) ([]byte, error) {
		if !dirty[path] {
			return os.ReadFile(filepath.Join(cwd, path))
		}
		blob, ok := Git(cwd, gitMedium, "cat-file", "blob", ":./"+path)
		if !ok {
			return nil, fmt.Errorf("no index blob for %s", path)
		}
		return []byte(blob), nil
	}
}

// ReferencingFiles lists tracked files that mention a changed symbol and are NOT
// themselves in the diff.
//
// `git grep --cached -w -F` over the index: word-boundary so `save` does not match
// `autosave`, fixed-string so a symbol containing regex metacharacters cannot blow up
// the search, the index so an unstaged edit that does not land cannot match. It runs
// at the work-tree root, so its paths compare with the root-relative `changed`.
func ReferencingFiles(cfg Config, cwd string, symbols map[string]bool, changed []string) []string {
	top := Toplevel(cwd)
	return referencingFiles(cfg, top, symbols, changed, indexedReader(top))
}

func referencingFiles(cfg Config, cwd string, symbols map[string]bool, changed []string,
	read func(string) ([]byte, error)) []string {
	if len(symbols) == 0 {
		return nil
	}
	args := []string{"grep", "--cached", "-l", "-w", "-F", "-I"}
	for _, s := range sortedKeys(symbols) {
		args = append(args, "-e", s)
	}
	out, ok := Git(cwd, gitMedium, args...)
	if !ok { // no matches exits 1; treat as empty
		return nil
	}
	changedSet := map[string]bool{}
	for _, p := range changed {
		changedSet[p] = true
	}
	var hits []string
	for _, p := range nonEmptyLines(out) {
		if !changedSet[p] {
			hits = append(hits, p)
		}
	}

	// Rank by how many distinct changed symbols each file mentions — the closest
	// mechanical proxy for how coupled a file is to the change — then prefer smaller
	// files so the budget buys more of them.
	type scored struct {
		path     string
		mentions int
		size     int
	}
	ranked := make([]scored, 0, len(hits))
	for _, p := range hits {
		text, err := read(p)
		if err != nil {
			ranked = append(ranked, scored{path: p})
			continue
		}
		mentions := 0
		for symbol := range symbols {
			if wordRe(symbol).Match(text) {
				mentions++
			}
		}
		ranked = append(ranked, scored{path: p, mentions: mentions, size: len(text)})
	}
	sort.SliceStable(ranked, func(a, b int) bool {
		if ranked[a].mentions != ranked[b].mentions {
			return ranked[a].mentions > ranked[b].mentions
		}
		return ranked[a].size < ranked[b].size
	})
	limit := cfg.MaxContextFiles
	if len(ranked) < limit {
		limit = len(ranked)
	}
	out2 := make([]string, 0, limit)
	for _, item := range ranked[:limit] {
		out2 = append(out2, item.path)
	}
	return out2
}

// BuildContext renders the unchanged referencing files as one block, whole-file where
// the budget allows. Truncation is a budget backstop and says so in the text, so the
// reviewer never mistakes an elision for the end of a file.
func BuildContext(cfg Config, cwd string, symbols map[string]bool, changed []string) string {
	cwd = Toplevel(cwd)
	read := indexedReader(cwd)
	files := referencingFiles(cfg, cwd, symbols, changed, read)
	if len(files) == 0 {
		return ""
	}
	var chunks []string
	spent := 0
	for _, path := range files {
		raw, err := read(path)
		if err != nil {
			continue
		}
		text := string(raw)
		var hits []string
		for _, symbol := range sortedKeys(symbols) {
			if wordRe(symbol).MatchString(text) {
				hits = append(hits, symbol)
			}
		}
		// Whole unchanged files are the largest untrusted block in the prompt, and the
		// one closing tag that matters is this section's own.
		text = NeutralizeTags(text)
		if len(text) > cfg.MaxContextFileChars {
			text = text[:cfg.MaxContextFileChars] +
				fmt.Sprintf("\n... [truncated at %d chars]\n", cfg.MaxContextFileChars)
		}
		if spent+len(text) > cfg.MaxContextChars {
			break
		}
		spent += len(text)
		chunks = append(chunks, fmt.Sprintf("--- %s (unchanged; references: %s) ---\n%s",
			path, strings.Join(hits, ", "), text))
	}
	if len(chunks) == 0 {
		return ""
	}
	return "\n<unchanged-callers>\nThese files are NOT part of the commit. They are " +
		"included in full because each references a name the diff changes.\n\n" +
		strings.Join(chunks, "\n\n") + "\n</unchanged-callers>\n"
}
