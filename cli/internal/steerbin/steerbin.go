// Package steerbin owns the stable executable at ~/.steer/bin/steer and the hook
// commands that name it.
//
// A registration that names the bare word "steer" is resolved on PATH at hook
// time. Neither harness reports a command it cannot resolve: the tool call runs,
// the hook is skipped, and nothing says so. Naming one absolute path takes the
// PATH out of the contract, and the path is fixed so an upgrade rewrites the file
// rather than the registrations.
package steerbin

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/steer-sh/steer-cli/internal/shellword"
	"github.com/steer-sh/steer-cli/internal/steerpath"
)

// Resolution states for the command a registration names.
const (
	// Resolvable means the command resolves right now, from this process. It is a
	// proxy: a hook runs under the harness's environment, whose PATH may differ.
	Resolvable = "resolvable"
	// Missing means the command does not resolve. A hook naming it fails open.
	Missing = "missing"
	// Unknown means there is no command to test, or it cannot be read.
	Unknown = "unknown"
)

// Path returns the stable executable's absolute path.
func Path() (string, error) { return steerpath.BinPath() }

// ensured memoizes one process's copy, keyed on the target path: an install
// registers several channels and each asks for the command, and copying the
// binary once per ask is waste. The key is the path, not the process, so a test
// that moves HOME copies again.
var ensured = struct {
	mu   sync.Mutex
	done map[string]bool
}{done: map[string]bool{}}

// Ensure copies the running binary to the stable path and returns that path. It
// is a no-op when the running binary already is that file.
func Ensure() (string, error) {
	target, err := steerpath.BinPath()
	if err != nil {
		return "", err
	}
	ensured.mu.Lock()
	defer ensured.mu.Unlock()
	if ensured.done[target] {
		return target, nil
	}
	if _, err := install(); err != nil {
		return "", err
	}
	ensured.done[target] = true
	return target, nil
}

// HookCommand places the stable executable and renders a hook command naming it.
// On failure it returns the bare word with the error: a registration resolved on
// PATH beats no registration at all, and the caller reports what it settled for.
func HookCommand(args string) (string, error) {
	if _, err := Ensure(); err != nil {
		return bareCommand(args), err
	}
	return command(args)
}

func install() (string, error) {
	target, err := steerpath.BinPath()
	if err != nil {
		return "", err
	}
	running, err := runningBinary()
	if err != nil {
		return "", err
	}
	if running == target {
		return target, nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", fmt.Errorf("failed to create %s: %w", filepath.Dir(target), err)
	}
	if err := copyExecutable(running, target); err != nil {
		return "", err
	}
	return target, nil
}

// runningBinary names this process's executable with every symlink resolved, so a
// copy through a wrapper link copies the real file.
func runningBinary() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to resolve the running executable: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return exe, nil
	}
	return resolved, nil
}

// copyExecutable writes src to dst through a temporary file in the destination
// directory, then renames it. A running hook reads either the old file or the new
// one, never a half-written one, and a Unix rename over a busy executable is legal.
func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", src, err)
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".steer-bin-*")
	if err != nil {
		return fmt.Errorf("failed to stage the executable: %w", err)
	}
	staged := tmp.Name()
	defer os.Remove(staged)
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to copy the executable: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close the staged executable: %w", err)
	}
	if err := os.Chmod(staged, 0o755); err != nil {
		return fmt.Errorf("failed to mark the staged executable runnable: %w", err)
	}
	if err := os.Rename(staged, dst); err != nil {
		return fmt.Errorf("failed to install %s: %w", dst, err)
	}
	return nil
}

// Command renders a hook command naming the stable executable. It falls back to
// the bare word when the path cannot be resolved: a registration that resolves on
// PATH beats no registration at all. HookCommand reports why it settled for the
// fallback; a caller that only needs the string uses this.
func Command(args string) string {
	rendered, _ := command(args)
	return rendered
}

func command(args string) (string, error) {
	path, err := steerpath.BinPath()
	if err != nil {
		return bareCommand(args), err
	}
	// The stable path is the caller's preference, not its contract. A path this
	// machine cannot render as one word still resolves on PATH under its bare
	// name, so the fallback serves the unencodable path too.
	if err := shellword.Encodable(path); err != nil {
		return bareCommand(args), err
	}
	return strings.TrimSpace(Quote(path) + " " + args), nil
}

func bareCommand(args string) string {
	return strings.TrimSpace(steerpath.BinName + " " + args)
}

// Relink rewrites a registered command to name the stable executable, keeping
// every argument. A command whose first word is not steer's own is returned
// unchanged: a user's wrapper script is not ours to rewrite.
func Relink(registration, path string) string {
	head, rest := split(registration)
	if head == "" || !isSteer(head) {
		return registration
	}
	if err := shellword.Encodable(path); err != nil {
		return registration
	}
	return strings.TrimSpace(Quote(path) + " " + rest)
}

// Resolve reports whether a registered command resolves from this process.
func Resolve(command string) string {
	head, _ := split(command)
	if head == "" {
		return Unknown
	}
	if strings.ContainsRune(head, filepath.Separator) {
		info, err := os.Stat(head)
		if err != nil || info.IsDir() {
			return Missing
		}
		// Windows runs a file by its extension and Go reports no execute bit there,
		// so testing one would report every installed hook as missing.
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
			return Missing
		}
		return Resolvable
	}
	if _, err := exec.LookPath(head); err != nil {
		return Missing
	}
	return Resolvable
}

// split separates a command's first word from its arguments, honouring the
// quoting Quote writes. A command this package could not have written has no
// first word, and every caller then leaves it alone.
func split(command string) (string, string) {
	head, rest, ok := shellword.Split(command)
	if !ok {
		return "", ""
	}
	return head, rest
}

// IsSteerCommand reports whether a command's first word is a steer executable. A
// caller recognising one of steer's own registrations reads the head rather than
// searching the whole command: a foreign command carrying "steer" in an argument is
// not steer's, and treating it as steer's lets an install overwrite it unasked.
func IsSteerCommand(command string) bool {
	head, _ := split(command)
	return head != "" && isSteer(head)
}

// isSteer reports whether a command's first word is a steer executable. It
// compares the name without an extension, so a Windows registration naming
// steer.exe and a Unix one naming steer are both recognised on either platform.
func isSteer(head string) bool {
	base := head
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	return strings.TrimSuffix(base, filepath.Ext(base)) == steerpath.BinStem
}

// Quote renders a path as one shell word, so a path holding a space survives the
// shell the harness runs the command through.
func Quote(path string) string { return shellword.Quote(path) }

// StripFlags removes the named flags, and the value each carries, from a
// registered command. It reads both `--name value` and `--name=value`. Every name
// it is given must be a flag that TAKES a value: the separated form always
// consumes the following word, because a value may itself begin with a hyphen and
// nothing in the string says whether it is a value or the next flag. A command
// whose first word is not steer's own is returned unchanged, the same rule Relink
// follows: a user's wrapper script is not ours to rewrite.
//
// A flag on a registration outranks every configuration file, so one left on disk
// pins the setting it names forever.
func StripFlags(command string, names ...string) string {
	head, rest := split(command)
	if head == "" || !isSteer(head) || rest == "" {
		return command
	}
	// The head is re-encoded below, so a head this machine cannot render as one
	// word is left in the caller's hands rather than rewritten into another path.
	if err := shellword.Encodable(head); err != nil {
		return command
	}
	wanted := map[string]bool{}
	for _, name := range names {
		wanted[name] = true
	}
	fields := shellFields(rest)
	kept := make([]string, 0, len(fields))
	dropped := false
	for i := 0; i < len(fields); i++ {
		field := fields[i]
		if name, _, ok := strings.Cut(field, "="); ok && wanted[name] {
			dropped = true
			continue
		}
		if wanted[field] {
			dropped = true
			i++
			continue
		}
		kept = append(kept, field)
	}
	if !dropped {
		return command
	}
	return strings.TrimSpace(Quote(head) + " " + strings.Join(kept, " "))
}

// HasFlags reports whether a command carries any of the named flags.
func HasFlags(command string, names ...string) bool {
	return StripFlags(command, names...) != command
}

// shellFields splits a command's arguments into words, keeping a quoted run as one
// word. strings.Fields would split `--model 'a b'` into three, and dropping the
// flag would then leave half its value behind as an argument of its own.
func shellFields(rest string) []string {
	var fields []string
	var word strings.Builder
	var quote rune
	started := false
	for _, r := range rest {
		switch {
		case quote != 0:
			word.WriteRune(r)
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
			word.WriteRune(r)
			started = true
		case r == ' ' || r == '\t':
			if started {
				fields = append(fields, word.String())
				word.Reset()
				started = false
			}
		default:
			word.WriteRune(r)
			started = true
		}
	}
	if started {
		fields = append(fields, word.String())
	}
	return fields
}
