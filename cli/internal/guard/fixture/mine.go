package fixture

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/steer-sh/steer-cli/internal/transcript"
)

// The two preambles renderDiff writes when a diff does not fit the review
// budget. Both announce a lossy stored diff, and each is lossy differently:
// the first kept some whole file sections, the second kept none.
const (
	droppedFilesPreamble = "[The review harness dropped "
	statOnlyPreamble     = "[diff is "
)

// Truncation reports what the review harness dropped from a stored diff.
// Budgeting keeps whole file sections only and never truncates inside a file,
// so a file whose hunks are present is complete.
type Truncation struct {
	DroppedFiles bool
	StatOnly     bool
}

// Lossy reports whether anything is missing from the stored diff.
func (t Truncation) Lossy() bool { return t.DroppedFiles || t.StatOnly }

// Hunk is one hunk of one file, rendered as the tool call a guard would have
// seen. Per-hunk is what the runtime emits for a MultiEdit, and it avoids
// welding an import at line 1 onto a change at line 300 into content no tool
// call ever carried. Added and Removed hold the hunk's + and - lines in order;
// context lines separate them, so neither is a contiguous run of the file.
type Hunk struct {
	Path        string
	Index       int
	Header      string
	Added       []string
	Removed     []string
	NewFile     bool
	DeletedFile bool
	// Payload is the whole tool call: a Write for a new file, a path-only
	// delete Edit for a deleted file, and an Edit carrying old_string and
	// new_string otherwise.
	Payload map[string]any
	// Skip names why this hunk yields no fixture, and is empty when it does.
	Skip string
}

// ParseDiff reads the git unified diff a ledger row stored and returns one
// candidate per hunk.
//
// This is not the runtime's apply_patch parser: that one reads Codex
// "*** Add File:" markers, which git diff text does not carry.
func ParseDiff(text string) ([]Hunk, Truncation, error) {
	trunc := Truncation{
		DroppedFiles: strings.Contains(text, droppedFilesPreamble),
		StatOnly:     strings.Contains(text, statOnlyPreamble),
	}

	var hunks []Hunk
	var path, oldPath string
	var newFile, deletedFile bool
	// gitFile is set once a `diff --git ` line delimits files: a file's header
	// pair then comes only before its first hunk.
	var gitFile bool
	var current *Hunk
	var oldLeft, newLeft int
	var counted, filled bool
	// The current hunk's old start and count, and its last two body lines.
	var start, span int
	var tail [2]string
	// overflow is the hunk that just filled its counts while the lines after it
	// may still be its body; -1 otherwise.
	overflow := -1
	// pair is the hunk that just filled its counts on a "--- " then "+++ " body
	// line, which may instead be the next file's header pair; -1 otherwise.
	pair := -1
	var pairEnd int
	var pairLines [2]string

	readOld := func(line string) {
		old := strings.TrimSpace(strings.TrimPrefix(line, "--- "))
		newFile = old == "/dev/null"
		oldPath = stripPrefix(old, "a/")
	}
	readNew := func(line string) {
		target := strings.TrimSpace(strings.TrimPrefix(line, "+++ "))
		deletedFile = target == "/dev/null"
		if deletedFile {
			path = oldPath
		} else {
			path = stripPrefix(target, "b/")
		}
	}
	body := func(line string) {
		tail = [2]string{tail[1], line}
	}

	flush := func() {
		if current == nil {
			return
		}
		hunks = append(hunks, finish(*current))
		current = nil
	}

	lines := strings.Split(text, "\n")
	for i, line := range lines {
		// After a full hunk, a body line belongs to no hunk: the hunk before it is
		// missing lines. In a header-less diff a "--- " line may be the next file's
		// header instead. An empty line may be blank context, so it neither marks
		// the hunk nor ends the check, and neither does a "\" marker.
		if overflow >= 0 && line != "" && !strings.HasPrefix(line, "\\") {
			header := !gitFile && strings.HasPrefix(line, "--- ")
			if strings.ContainsAny(line[:1], "+- ") && !header {
				hunks[overflow].Payload = nil
				hunks[overflow].Skip = "the hunk body runs past its header's line counts"
				pair = -1
				continue
			}
			overflow = -1
		}
		if pair >= 0 && !strings.HasPrefix(line, "@@") {
			pair = -1
		}
		switch {
		case strings.HasPrefix(line, "diff --git "):
			// A `diff --git ` at column 0 always starts a file: every line of a
			// hunk body carries a leading ' ', '+', '-' or '\'.
			flush()
			gitFile = true
			path = ""
			oldPath = ""
			newFile = false
			deletedFile = false
		case current == nil && strings.HasPrefix(line, "--- "):
			// The ---/+++ pair names the paths unambiguously; the diff --git
			// header's two-path form is ambiguous for a path holding a space.
			// Inside a hunk the same prefix is a removed line starting "-- ".
			readOld(line)
		case current == nil && strings.HasPrefix(line, "+++ "):
			readNew(line)
		case strings.HasPrefix(line, "@@"):
			flush()
			if next, ok := hunkStart(line); pair >= 0 && ok && next < pairEnd {
				// Hunks of one file run forward, so a hunk starting at a line the
				// previous one covers belongs to the next file: the previous hunk
				// was short, and its last two lines are this file's header.
				h := hunks[pair]
				h.Removed = h.Removed[:len(h.Removed)-1]
				h.Added = h.Added[:len(h.Added)-1]
				h.Payload, h.Skip = nil, ""
				hunks[pair] = finish(h)
				readOld(pairLines[0])
				readNew(pairLines[1])
			}
			pair = -1
			if path == "" {
				continue
			}
			current = &Hunk{Path: path, Index: countFor(hunks, path) + 1, Header: line,
				NewFile: newFile, DeletedFile: deletedFile}
			oldLeft, newLeft, counted = hunkCounts(line)
			filled = counted && hunkFills(lines[i+1:], oldLeft, newLeft)
			start, _ = hunkStart(line)
			span = oldLeft
			tail = [2]string{}
		case current != nil && strings.HasPrefix(line, "+"):
			// The marker is stripped, as the runtime does: a guard is handed
			// the line's content, never the diff's rendering of it.
			current.Added = append(current.Added, line[1:])
			newLeft--
			body(line)
		case current != nil && strings.HasPrefix(line, "-"):
			current.Removed = append(current.Removed, line[1:])
			oldLeft--
			body(line)
		case current != nil && strings.HasPrefix(line, "\\"):
			// "\ No newline at end of file" describes the line before it.
		case current != nil && (strings.HasPrefix(line, " ") ||
			(line == "" && filled)):
			// Context lines are in neither the added nor the removed text. An empty
			// line is one blank context line, as diff.suppressBlankEmpty writes it,
			// only when the body fills its header; otherwise it ends a hunk shorter
			// than its header.
			oldLeft--
			newLeft--
			body(line)
		case current != nil:
			flush()
		}
		// A full hunk ends here, so a following "--- " is the next file's header.
		if current != nil && counted && oldLeft <= 0 && newLeft <= 0 {
			flush()
			overflow = len(hunks) - 1
			if strings.HasPrefix(tail[0], "--- ") && strings.HasPrefix(tail[1], "+++ ") {
				pair, pairLines = overflow, tail
				pairEnd = start + max(span, 1)
			}
		}
	}
	flush()

	if len(hunks) == 0 && !trunc.Lossy() {
		return nil, trunc, fmt.Errorf("the stored diff holds no hunks")
	}
	return hunks, trunc, nil
}

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+\d+(?:,(\d+))? @@`)

// hunkCounts reads the old and new line counts from a hunk header. ok is false
// when the header is malformed or promises no lines.
func hunkCounts(header string) (oldLines, newLines int, ok bool) {
	m := hunkHeader.FindStringSubmatch(header)
	if m == nil {
		return 0, 0, false
	}
	oldLines, oldErr := countField(m[2])
	newLines, newErr := countField(m[3])
	if oldErr != nil || newErr != nil || (oldLines == 0 && newLines == 0) {
		return 0, 0, false
	}
	return oldLines, newLines, true
}

// hunkStart reads the old start line from a hunk header.
func hunkStart(header string) (int, bool) {
	m := hunkHeader.FindStringSubmatch(header)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	return n, err == nil
}

// hunkFills reports whether lines, read as hunk body, reach exactly the counts
// left, an empty line counting as context.
func hunkFills(lines []string, oldLeft, newLeft int) bool {
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "+"):
			newLeft--
		case strings.HasPrefix(line, "-"):
			oldLeft--
		case strings.HasPrefix(line, "\\"):
		case line == "" || strings.HasPrefix(line, " "):
			oldLeft--
			newLeft--
		default:
			return false
		}
		if oldLeft < 0 || newLeft < 0 {
			return false
		}
		if oldLeft == 0 && newLeft == 0 {
			return true
		}
	}
	return false
}

// countField reads one header count, which git omits when it is 1.
func countField(field string) (int, error) {
	if field == "" {
		return 1, nil
	}
	return strconv.Atoi(field)
}

// finish decides a hunk's payload, or why it has none.
func finish(h Hunk) Hunk {
	if len(h.Added) == 0 && len(h.Removed) == 0 {
		h.Skip = "the hunk adds and removes no lines"
		return h
	}
	if h.DeletedFile {
		// The live runtime never hands a guard a deleted file's content, so
		// neither does a candidate: the removed lines stay out of the payload.
		h.Payload = map[string]any{
			"tool_name": "Edit",
			"tool_input": map[string]any{
				"file_path": h.Path, "steer_operation": "delete", "new_string": "",
			},
		}
		return h
	}
	if h.NewFile {
		h.Payload = map[string]any{
			"tool_name":  "Write",
			"tool_input": map[string]any{"file_path": h.Path, "content": joinLines(h.Added)},
		}
		return h
	}
	h.Payload = map[string]any{
		"tool_name": "Edit",
		"tool_input": map[string]any{
			"file_path": h.Path, "old_string": joinLines(h.Removed), "new_string": joinLines(h.Added),
		},
	}
	return h
}

// joinLines renders diff lines as file text, each line newline-terminated.
func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// HunksFor narrows candidates to the file a finding names. The filename narrows
// candidates; it does not make every hunk in that file a violation, and it
// makes no hunk of another file legitimate.
func HunksFor(hunks []Hunk, path string) []Hunk {
	want := strings.TrimSpace(path)
	var out []Hunk
	for _, h := range hunks {
		if h.Path == want {
			out = append(out, h)
		}
	}
	return out
}

// Complete reports whether the stored diff carries the named file's own hunks.
// diff_chars measures the already-rendered text, so comparing it against the
// rendered length compares a value with itself; this asks the question directly.
func Complete(hunks []Hunk, trunc Truncation, path string) bool {
	if trunc.StatOnly {
		return false
	}
	return len(HunksFor(hunks, path)) > 0
}

// ShellCall is one shell command a session recorded, ready as a Bash payload.
type ShellCall struct {
	SessionID string
	Command   string
	Payload   map[string]any
}

// FromSession re-reads a session's transcript and returns its shell calls.
// Event.Shell is the flag, and Target is the tool's subject — the command
// itself for a shell call. A transcript the harness rotated away yields
// nothing, which is why every mined case is a reconstruction.
func FromSession(adapters []transcript.Adapter, sessionID string) ([]ShellCall, error) {
	want := strings.TrimSpace(sessionID)
	if want == "" {
		return nil, fmt.Errorf("session id cannot be empty")
	}

	var calls []ShellCall
	for _, adapter := range adapters {
		root, err := adapter.Root()
		if err != nil {
			continue
		}
		sessions, err := adapter.Discover(root, transcript.Filter{AllProjects: true})
		if err != nil {
			continue
		}
		for _, session := range sessions {
			_, err := adapter.Stream(session, transcript.Filter{AllProjects: true}, func(e transcript.Event) error {
				if e.Kind != transcript.KindToolCall || !e.Shell || e.SessionID != want {
					return nil
				}
				command := strings.TrimSpace(e.Target)
				if command == "" {
					return nil
				}
				calls = append(calls, ShellCall{
					SessionID: e.SessionID,
					Command:   command,
					Payload: map[string]any{
						"tool_name":  "Bash",
						"tool_input": map[string]any{"command": command},
					},
				})
				return nil
			})
			if err != nil {
				continue
			}
		}
	}
	return calls, nil
}

func countFor(hunks []Hunk, path string) int {
	n := 0
	for _, h := range hunks {
		if h.Path == path {
			n++
		}
	}
	return n
}

// stripPrefix removes git's a/ or b/ prefix and unquotes a C-quoted path.
func stripPrefix(path, prefix string) string {
	if strings.HasPrefix(path, `"`) {
		if unquoted, err := strconv.Unquote(path); err == nil {
			path = unquoted
		}
	}
	return strings.TrimPrefix(path, prefix)
}
