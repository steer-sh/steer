package gate

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/steer-sh/steer-cli/internal/steerpath"
)

var hostSlugRe = regexp.MustCompile(`[^a-z0-9]+`)

// hostSlug is a name as the ledger stores it: lowercase, alphanumerics and dashes,
// up to the first dot. It returns "" for a name with nothing left.
func hostSlug(raw string) string {
	first, _, _ := strings.Cut(strings.ToLower(raw), ".")
	return strings.Trim(hostSlugRe.ReplaceAllString(first, "-"), "-")
}

// hostNames reads the host file: this machine's name, then its former names.
func hostNames() []string {
	path := steerpath.HostFile()
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return strings.Fields(string(data))
}

// HostID names this machine for the ledger. Stamped on every row and used as the
// shard suffix, so a merged log can still be split back into per-machine streams.
// COMMIT_REVIEW_HOST wins, then the host file; otherwise the hostname's slug is
// pinned there on first use. It returns "unknown" when no name can be derived.
func HostID() string {
	if raw := os.Getenv("COMMIT_REVIEW_HOST"); raw != "" {
		if slug := hostSlug(raw); slug != "" {
			return slug
		}
		return "unknown"
	}
	if names := hostNames(); len(names) > 0 {
		return names[0]
	}
	raw, _ := os.Hostname()
	slug := hostSlug(raw)
	if slug == "" {
		return "unknown"
	}
	return pinHost(slug)
}

// pinHost writes slug as this machine's name unless another process got there
// first, and returns the name the file holds. A failed write leaves slug unpinned.
// The link publishes a whole file, so no reader or failed write sees an empty one.
func pinHost(slug string) string {
	path := steerpath.HostFile()
	if path == "" || os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return slug
	}
	tmp, err := writeHostTemp(path, []string{slug})
	if err != nil {
		return slug
	}
	defer os.Remove(tmp)
	if os.Link(tmp, path) != nil {
		if names := hostNames(); len(names) > 0 {
			return names[0]
		}
	}
	return slug
}

// writeHostTemp writes names to a new file beside path and returns its path.
func writeHostTemp(path string, names []string) (string, error) {
	file, err := os.CreateTemp(filepath.Dir(path), "host-*.tmp")
	if err != nil {
		return "", err
	}
	defer file.Close()
	_, err = file.WriteString(strings.Join(names, "\n") + "\n")
	if err == nil {
		err = file.Chmod(0o644)
	}
	if err != nil {
		os.Remove(file.Name())
		return "", err
	}
	return file.Name(), nil
}

// FormerHosts lists the names this machine used before its current one. It lists
// none under COMMIT_REVIEW_HOST, which selects a name the file does not describe.
func FormerHosts() []string {
	if os.Getenv("COMMIT_REVIEW_HOST") != "" {
		return nil
	}
	names := hostNames()
	if len(names) < 2 {
		return nil
	}
	return names[1:]
}

// IsFormerHost reports that name is one this machine declared as its own earlier.
func IsFormerHost(name string) bool {
	for _, former := range FormerHosts() {
		if former == name {
			return true
		}
	}
	return false
}

// AddFormerHost declares name as one this machine used before, and returns the slug
// it stored. It refuses the current name, which is not a former one, and refuses to
// run under COMMIT_REVIEW_HOST, which would pin the override as this machine's name.
func AddFormerHost(name string) (string, error) {
	if os.Getenv("COMMIT_REVIEW_HOST") != "" {
		return "", fmt.Errorf("COMMIT_REVIEW_HOST is set; unset it to declare a former name")
	}
	slug := hostSlug(name)
	if slug == "" {
		return "", fmt.Errorf("%q holds no usable name", name)
	}
	current := HostID()
	if slug == current {
		return "", fmt.Errorf("%s is this machine's current name", slug)
	}
	if IsFormerHost(slug) {
		return slug, nil
	}
	path := steerpath.HostFile()
	if path == "" {
		return "", fmt.Errorf("the home directory cannot be resolved")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	names := hostNames()
	if len(names) == 0 {
		names = []string{current}
	}
	names = append(names, slug)
	tmp, err := writeHostTemp(path, names)
	if err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return slug, nil
}
