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
	var current *Hunk
	var oldLeft, newLeft int
	var counted, filled bool

	flush := func() {
		if current == nil {
			return
		}
		hunks = append(hunks, finish(*current))
		current = nil
	}

	lines := strings.Split(text, "\n")
	for i, line := range lines {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			// A `diff --git ` at column 0 always starts a file: every line of a
			// hunk body carries a leading ' ', '+', '-' or '\'.
			flush()
			path = ""
			oldPath = ""
			newFile = false
			deletedFile = false
		case current == nil && strings.HasPrefix(line, "--- "):
			// The ---/+++ pair names the paths unambiguously; the diff --git
			// header's two-path form is ambiguous for a path holding a space.
			// Inside a hunk the same prefix is a removed line starting "-- ".
			old := strings.TrimSpace(strings.TrimPrefix(line, "--- "))
			newFile = old == "/dev/null"
			oldPath = stripPrefix(old, "a/")
		case current == nil && strings.HasPrefix(line, "+++ "):
			target := strings.TrimSpace(strings.TrimPrefix(line, "+++ "))
			deletedFile = target == "/dev/null"
			if deletedFile {
				path = oldPath
			} else {
				path = stripPrefix(target, "b/")
			}
		case strings.HasPrefix(line, "@@"):
			flush()
			if path == "" {
				continue
			}
			current = &Hunk{Path: path, Index: countFor(hunks, path) + 1, Header: line,
				NewFile: newFile, DeletedFile: deletedFile}
			oldLeft, newLeft, counted = hunkCounts(line)
			filled = counted && hunkFills(lines[i+1:], oldLeft, newLeft)
		case current != nil && strings.HasPrefix(line, "+"):
			// The marker is stripped, as the runtime does: a guard is handed
			// the line's content, never the diff's rendering of it.
			current.Added = append(current.Added, line[1:])
			newLeft--
		case current != nil && strings.HasPrefix(line, "-"):
			current.Removed = append(current.Removed, line[1:])
			oldLeft--
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
		case current != nil:
			flush()
		}
		// A full hunk ends here, so a following "--- " is the next file's header.
		if current != nil && counted && oldLeft <= 0 && newLeft <= 0 {
			flush()
		}
	}
	flush()

	if len(hunks) == 0 && !trunc.Lossy() {
		return nil, trunc, fmt.Errorf("the stored diff holds no hunks")
	}
	return hunks, trunc, nil
}

var hunkHeader = regexp.MustCompile(`^@@ -\d+(?:,(\d+))? \+\d+(?:,(\d+))? @@`)

// hunkCounts reads the old and new line counts from a hunk header. ok is false
// when the header is malformed or promises no lines.
func hunkCounts(header string) (oldLines, newLines int, ok bool) {
	m := hunkHeader.FindStringSubmatch(header)
	if m == nil {
		return 0, 0, false
	}
	oldLines, oldErr := countField(m[1])
	newLines, newErr := countField(m[2])
	if oldErr != nil || newErr != nil || (oldLines == 0 && newLines == 0) {
		return 0, 0, false
	}
	return oldLines, newLines, true
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
