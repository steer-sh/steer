package gate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
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

const (
	// maxSearchTerms caps the terms one context search sends to git grep.
	maxSearchTerms = 256
	// commonTermDivisor: a term in more than this fraction of the indexed files is
	// vocabulary rather than a name, and selects nothing.
	commonTermDivisor = 2
	// maxListedTerms caps the matched terms a file's header names.
	maxListedTerms = 12
)

// contextScanBytes caps the bytes one context search reads from candidate files, and
// contextScanTime the time it spends reading them.
var (
	contextScanBytes = 32 << 20
	contextScanTime  = gitMedium
)

// wordRunRe matches a word as `git grep -w` delimits one: ASCII letters, digits and
// underscores.
var wordRunRe = regexp.MustCompile(`[A-Za-z0-9_]+`)

// addIdentifiers adds text's identifier-like words of three or more characters to
// into. A shorter word matches nearly everything; one starting with a digit is a number.
func addIdentifiers(text string, into map[string]bool) {
	for _, word := range wordRunRe.FindAllString(text, -1) {
		if len(word) > 2 && (word[0] < '0' || word[0] > '9') {
			into[word] = true
		}
	}
}

// scopeLine reports a line's indentation and whether it can open a scope: after the
// indentation it starts with a letter, `_` or `$`, git's default funcname rule.
// blank is true for a line with nothing but whitespace.
func scopeLine(line string) (indent int, opens, blank bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if strings.TrimSpace(trimmed) == "" {
		return 0, false, true
	}
	c := trimmed[0]
	opens = c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
	return len(line) - len(trimmed), opens, false
}

// enclosingLine is one line of a change's indentation ancestry.
type enclosingLine struct {
	indent int
	text   string
}

// hunkFuncname is the text git prints after a hunk header's closing `@@`.
func hunkFuncname(header string) string {
	if end := strings.Index(header[2:], "@@"); end >= 0 {
		return strings.TrimSpace(header[2+end+2:])
	}
	return ""
}

// ChangedTerms returns the context search terms: the identifier-like words on the
// diff's + and - lines and on the lines enclosing each change, plus the changed files'
// basenames. A change's enclosing lines are each earlier hunk line that opens a scope
// and is less indented than the one after it, down to column 0, or the hunk header's
// funcname text before any column-0 line; they name a function whose body alone changed.
func ChangedTerms(diff string, paths []string) map[string]bool {
	terms := map[string]bool{}
	inHunk := false
	var enclosing []enclosingLine
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			inHunk = false
		case strings.HasPrefix(line, "@@"):
			inHunk = true
			enclosing = append(enclosing[:0], enclosingLine{text: hunkFuncname(line)})
		case !inHunk || line == "" || (line[0] != ' ' && line[0] != '+' && line[0] != '-'):
		default:
			body := line[1:]
			indent, opens, blank := scopeLine(body)
			if blank {
				continue
			}
			for len(enclosing) > 0 && enclosing[len(enclosing)-1].indent >= indent {
				enclosing = enclosing[:len(enclosing)-1]
			}
			if line[0] != ' ' {
				addIdentifiers(body, terms)
				for _, outer := range enclosing {
					addIdentifiers(outer.text, terms)
				}
			}
			if opens {
				enclosing = append(enclosing, enclosingLine{indent: indent, text: body})
			}
		}
	}
	for _, p := range paths {
		// A pattern holding a newline is several patterns to git grep.
		if stem := Stem(p); len(stem) > 2 && !strings.ContainsAny(stem, "\n\x00") {
			terms[stem] = true
		}
	}
	return terms
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

// indexedFiles reads a file's index content, the version a file outside the diff
// lands with. A file whose working copy matches the index is read from disk; one with
// unstaged edits from its index blob. cwd is the work-tree root, so paths are
// root-relative, as `git grep` prints them there.
type indexedFiles struct {
	cwd   string
	dirty map[string]bool
}

func newIndexedFiles(cwd string) indexedFiles {
	dirty := map[string]bool{}
	if out, ok := Git(cwd, gitMedium, "diff", "--name-only", "--relative", "-z"); ok {
		for _, path := range strings.Split(out, "\x00") {
			dirty[path] = path != ""
		}
	}
	return indexedFiles{cwd: cwd, dirty: dirty}
}

func (f indexedFiles) read(path string) ([]byte, error) {
	if !f.dirty[path] {
		return os.ReadFile(filepath.Join(f.cwd, path))
	}
	blob, ok := Git(f.cwd, gitMedium, "cat-file", "blob", ":./"+path)
	if !ok {
		return nil, fmt.Errorf("no index blob for %s", path)
	}
	return []byte(blob), nil
}

// sizes returns the byte length read would return for each of paths it can size,
// without reading them: one stat per clean file, one `git cat-file --batch-check`
// for every dirty one.
func (f indexedFiles) sizes(paths []string) map[string]int {
	out := make(map[string]int, len(paths))
	var dirty []string
	for _, path := range paths {
		switch {
		case !f.dirty[path]:
			if n, ok := fileSize(filepath.Join(f.cwd, path)); ok {
				out[path] = int(n)
			}
		case !strings.Contains(path, "\n"):
			dirty = append(dirty, path)
		}
	}
	if len(dirty) == 0 {
		return out
	}
	var stdin strings.Builder
	for _, path := range dirty {
		stdin.WriteString(":./" + path + "\n")
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitMedium)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "cat-file", "--batch-check=%(objectsize)")
	cmd.Dir = f.cwd
	cmd.Stdin = strings.NewReader(stdin.String())
	reply, err := cmd.Output()
	if err != nil {
		return out
	}
	for i, line := range strings.Split(strings.TrimSuffix(string(reply), "\n"), "\n") {
		if n, err := strconv.Atoi(line); err == nil && i < len(dirty) {
			out[dirty[i]] = n
		}
	}
	return out
}

// searchTerms orders terms longest first, then lexically, and keeps the first
// maxSearchTerms: past the cap, a longer word is the likelier rare one.
func searchTerms(terms map[string]bool) []string {
	list := sortedKeys(terms)
	sort.SliceStable(list, func(a, b int) bool { return len(list[a]) > len(list[b]) })
	if len(list) > maxSearchTerms {
		list = list[:maxSearchTerms]
	}
	return list
}

// termSet marks which of the search terms a file contains, by index.
type termSet [maxSearchTerms / 64]uint64

func (s *termSet) add(i int)      { s[i/64] |= 1 << (i % 64) }
func (s *termSet) has(i int) bool { return s[i/64]&(1<<(i%64)) != 0 }

// errSearchTimedOut reports that the candidate search ran past its time limit.
var errSearchTimedOut = errors.New("timed out")

// grepCandidates lists the indexed files outside skip that contain any of terms as a
// whole word, in git's path order. It costs one `git grep -l`, which stops reading a
// file at its first match.
func grepCandidates(cwd string, terms []string, skip map[string]bool) ([]string, error) {
	args := []string{"grep", "--cached", "-l", "-z", "-w", "-F", "-I"}
	for _, term := range terms {
		args = append(args, "-e", term)
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitMedium)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = cwd
	var out strings.Builder
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		switch {
		case ctx.Err() != nil:
			return nil, errSearchTimedOut
		case errors.As(err, &exit) && exit.ExitCode() == 1:
			return nil, nil // no match
		default:
			return nil, err
		}
	}
	var paths []string
	for _, path := range nulFields(out.String()) {
		if !skip[path] {
			paths = append(paths, path)
		}
	}
	return paths, nil
}

// isWordByte is the byte class `git grep -w` treats as part of a word.
func isWordByte(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// containsWord reports whether text holds term with no word byte on either side.
func containsWord(text []byte, term string) bool {
	for from := 0; from <= len(text)-len(term); {
		at := bytes.Index(text[from:], []byte(term))
		if at < 0 {
			return false
		}
		at += from
		end := at + len(term)
		if (at == 0 || !isWordByte(text[at-1])) && (end == len(text) || !isWordByte(text[end])) {
			return true
		}
		from = at + 1
	}
	return false
}

// termMatcher marks which search terms a file contains as whole words. A term that is
// one word is found by splitting the file into words; any other (a stem such as
// `check-gate`) by a bounded substring search.
type termMatcher struct {
	words map[string]int
	other []int
	terms []string
}

func newTermMatcher(terms []string) termMatcher {
	m := termMatcher{words: map[string]int{}, terms: terms}
	for i, term := range terms {
		if wordRunRe.FindString(term) == term {
			m.words[term] = i
		} else {
			m.other = append(m.other, i)
		}
	}
	return m
}

func (m termMatcher) match(text []byte) termSet {
	var set termSet
	for i := 0; i < len(text); {
		if !isWordByte(text[i]) {
			i++
			continue
		}
		j := i
		for j < len(text) && isWordByte(text[j]) {
			j++
		}
		if k, ok := m.words[string(text[i:j])]; ok {
			set.add(k)
		}
		i = j
	}
	for _, k := range m.other {
		if containsWord(text, m.terms[k]) {
			set.add(k)
		}
	}
	return set
}

// termScan is what the search learned: per scanned file, its terms and size, and how
// much of the candidate list it covered.
type termScan struct {
	hits       map[string]*termSet
	sizes      map[string]int
	candidates int
	scanned    int
	unread     int
	err        error
}

// scanTerms finds the candidate files, then reads them smallest first and marks the
// terms each contains, until the next file would take the bytes read past budget. The
// cost is bounded by budget and the one grep, not by how often a term occurs.
func scanTerms(cwd string, terms []string, skip map[string]bool,
	files indexedFiles, budget int) termScan {
	scan := termScan{hits: map[string]*termSet{}, sizes: map[string]int{}}
	candidates, err := grepCandidates(cwd, terms, skip)
	if err != nil {
		scan.err = err
		return scan
	}
	scan.candidates = len(candidates)
	sizes := files.sizes(candidates)
	var sized []string
	for _, path := range candidates {
		if _, ok := sizes[path]; ok {
			sized = append(sized, path)
		} else {
			scan.unread++
		}
	}
	sort.SliceStable(sized, func(a, b int) bool { return sizes[sized[a]] < sizes[sized[b]] })
	matcher := newTermMatcher(terms)
	spent := 0
	deadline := time.Now().Add(contextScanTime)
	for _, path := range sized {
		if spent+sizes[path] > budget || time.Now().After(deadline) {
			break
		}
		text, err := files.read(path)
		if err != nil || spent+len(text) > budget {
			scan.unread++
			continue
		}
		spent += len(text)
		set := matcher.match(text)
		if set == (termSet{}) { // on disk in a form git grep does not search
			scan.unread++
			continue
		}
		scan.scanned++
		scan.hits[path] = &set
		scan.sizes[path] = len(text)
	}
	return scan
}

// indexedFileCount is the number of index entries outside skip.
func indexedFileCount(cwd string, skip map[string]bool) int {
	out, ok := Git(cwd, gitMedium, "ls-files", "--cached", "-z")
	if !ok {
		return 0
	}
	n := 0
	for _, path := range nulFields(out) {
		if !skip[path] {
			n++
		}
	}
	return n
}

// contextFile is one unchanged file selected for the context block, with the matched
// terms it was selected on, rarest first.
type contextFile struct {
	path  string
	terms []string
	score float64
}

// ReferencingFiles lists tracked files that share a changed term and are NOT
// themselves in the diff, best first.
func ReferencingFiles(cfg Config, cwd string, terms map[string]bool, changed []string) []string {
	top := Toplevel(cwd)
	var paths []string
	files, _ := referencingFiles(cfg, top, terms, changed, newIndexedFiles(top))
	for _, f := range files {
		paths = append(paths, f.path)
	}
	return paths
}

// referencingFiles ranks the unchanged files that contain a changed term. A term
// weighs its squared inverse document frequency over the unchanged indexed files, or
// nothing once it is in more of them than both MaxContextFiles and 1/commonTermDivisor
// of the total. A file scores the sum of its terms' weights; ties go to the smaller
// file, then the path. cwd is the work-tree root.
func referencingFiles(cfg Config, cwd string, terms map[string]bool, changed []string,
	files indexedFiles) ([]contextFile, termScan) {
	search := searchTerms(terms)
	if len(search) == 0 || cfg.MaxContextFiles <= 0 {
		return nil, termScan{}
	}
	changedSet := map[string]bool{}
	for _, p := range changed {
		changedSet[p] = true
	}
	scan := scanTerms(cwd, search, changedSet, files, contextScanBytes)
	hits := scan.hits
	if len(hits) == 0 {
		return nil, scan
	}

	total := indexedFileCount(cwd, changedSet)
	if total < len(hits) {
		total = len(hits)
	}
	df := make([]int, len(search))
	for _, set := range hits {
		for i := range search {
			if set.has(i) {
				df[i]++
			}
		}
	}
	maxDF := total / commonTermDivisor
	if maxDF < cfg.MaxContextFiles {
		maxDF = cfg.MaxContextFiles
	}
	weight := make([]float64, len(search))
	for i, n := range df {
		if n > 0 && n <= maxDF {
			idf := math.Log(float64(total+1) / float64(n))
			weight[i] = idf * idf
		}
	}
	byWeight := make([]int, len(search))
	for i := range byWeight {
		byWeight[i] = i
	}
	sort.SliceStable(byWeight, func(a, b int) bool {
		wa, wb := weight[byWeight[a]], weight[byWeight[b]]
		if wa != wb {
			return wa > wb
		}
		return search[byWeight[a]] < search[byWeight[b]]
	})

	ranked := make([]contextFile, 0, len(hits))
	for path, set := range hits {
		f := contextFile{path: path}
		for _, i := range byWeight {
			if weight[i] > 0 && set.has(i) {
				f.score += weight[i]
				f.terms = append(f.terms, search[i])
			}
		}
		if f.score > 0 {
			ranked = append(ranked, f)
		}
	}
	sort.Slice(ranked, func(a, b int) bool {
		if ranked[a].score != ranked[b].score {
			return ranked[a].score > ranked[b].score
		}
		if sa, sb := scan.sizes[ranked[a].path], scan.sizes[ranked[b].path]; sa != sb {
			return sa < sb
		}
		return ranked[a].path < ranked[b].path
	})
	if len(ranked) > cfg.MaxContextFiles {
		ranked = ranked[:cfg.MaxContextFiles]
	}
	return ranked, scan
}

// searchNote says how much of the candidate search a context block reflects, or ""
// when the search covered every candidate.
func searchNote(scan termScan) string {
	switch {
	case scan.err != nil:
		return fmt.Sprintf("The search for files sharing these names did not finish (%v), "+
			"so no unchanged file was ranked; their absence here says nothing about callers.",
			scan.err)
	}
	var notes []string
	if scan.unread > 0 {
		notes = append(notes, fmt.Sprintf(
			"%d candidate files could not be read or matched and were not ranked.", scan.unread))
	}
	if scan.scanned+scan.unread < scan.candidates {
		notes = append(notes, fmt.Sprintf("The search read %d of %d candidate files, smallest "+
			"first, before its %d-byte or %s budget ran out; the rest were not ranked.",
			scan.scanned, scan.candidates, contextScanBytes, contextScanTime))
	}
	return strings.Join(notes, " ")
}

// BuildContext renders the unchanged files that share a changed term as one block,
// whole-file where the budget allows. Truncation is a budget backstop and says so in
// the text, so the reviewer never mistakes an elision for the end of a file.
func BuildContext(cfg Config, cwd string, terms map[string]bool, changed []string) string {
	cwd = Toplevel(cwd)
	indexed := newIndexedFiles(cwd)
	files, scan := referencingFiles(cfg, cwd, terms, changed, indexed)
	note := searchNote(scan)
	var chunks []string
	spent := 0
	for _, f := range files {
		raw, err := indexed.read(f.path)
		if err != nil {
			continue
		}
		// Whole unchanged files are the largest untrusted block in the prompt, and the
		// one closing tag that matters is this section's own.
		text := NeutralizeTags(string(raw))
		if len(text) > cfg.MaxContextFileChars {
			text = text[:cfg.MaxContextFileChars] +
				fmt.Sprintf("\n... [truncated at %d chars]\n", cfg.MaxContextFileChars)
		}
		// Ranking is by shared terms, not size: a later, smaller file may still fit.
		if spent+len(text) > cfg.MaxContextChars {
			continue
		}
		spent += len(text)
		listed := f.terms
		more := ""
		if len(listed) > maxListedTerms {
			more = fmt.Sprintf(", +%d more", len(listed)-maxListedTerms)
			listed = listed[:maxListedTerms]
		}
		chunks = append(chunks, fmt.Sprintf("--- %s (unchanged; shares: %s%s) ---\n%s",
			f.path, strings.Join(listed, ", "), more, text))
	}
	if note != "" {
		note += "\n"
	}
	if len(chunks) == 0 {
		if note == "" {
			return ""
		}
		return "\n<unchanged-callers>\n" + note + "</unchanged-callers>\n"
	}
	return "\n<unchanged-callers>\nThese files are NOT part of the commit. Each shares " +
		"names with the diff's changed lines, rarest names first; a shared name shows " +
		"possible relevance, not a confirmed caller.\n" + note + "\n" +
		strings.Join(chunks, "\n\n") + "\n</unchanged-callers>\n"
}
