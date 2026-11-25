package guard

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
)

// StampPrefix opens the master hook's provenance line. The stamp lives in the
// master and not in the manifest: the manifest describes the guards, the master
// describes the runtime that runs them, and a master copied to another machine
// has to carry its own provenance.
const StampPrefix = "# steer-runtime: "

// stampScanLines caps how far into a file the parser looks. The stamp sits in
// the generated header, so a deeper match would be some guard's own text.
const stampScanLines = 20

// Stamp is one master's runtime provenance: the digest of the runtime spliced
// into it, and the CLI that spliced it.
type Stamp struct {
	Runtime string `json:"runtime"`
	Version string `json:"version"`
}

// RuntimeDigest is the first 8 hex digits of the sha256 of the embedded
// runtime. It is the comparison key: a master whose digest differs from the
// running binary's runs older Python than the binary would write.
func RuntimeDigest() string {
	sum := sha256.Sum256([]byte(RuntimeSource()))
	return hex.EncodeToString(sum[:])[:8]
}

// StampLine renders the provenance line for a master built by this binary.
func StampLine(version string) string {
	data, err := json.Marshal(Stamp{Runtime: RuntimeDigest(), Version: version})
	if err != nil {
		return StampPrefix + "{}\n"
	}
	return StampPrefix + string(data) + "\n"
}

// ParseStamp reads the provenance line out of a master's source. The second
// result is false when the file carries no stamp — every master generated
// before the stamp existed, which reads as unknown and never as current.
func ParseStamp(content string) (Stamp, bool) {
	scanner := bufio.NewScanner(strings.NewReader(content))
	for i := 0; i < stampScanLines && scanner.Scan(); i++ {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, strings.TrimSpace(StampPrefix)) {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, strings.TrimSpace(StampPrefix)))
		var stamp Stamp
		if err := json.Unmarshal([]byte(payload), &stamp); err != nil {
			return Stamp{}, false
		}
		if stamp.Runtime == "" {
			return Stamp{}, false
		}
		return stamp, true
	}
	return Stamp{}, false
}

// ReadStamp parses the stamp of the master at path. A missing or unreadable file
// reads as unstamped.
func ReadStamp(path string) (Stamp, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Stamp{}, false
	}
	return ParseStamp(string(data))
}
