package report

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// reportFile holds the last stored report inside the steer-report state directory.
const reportFile = "report.json"

// ReportPath is the stored report inside dir.
func ReportPath(dir string) string { return filepath.Join(dir, reportFile) }

// Save stamps the report's id from its content and writes it to dir, replacing
// the report stored before it.
func Save(dir string, d *Digest) error {
	d.ReportID = ""
	body, err := json.Marshal(d)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(body)
	d.ReportID = hex.EncodeToString(sum[:])[:12]
	data, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, reportFile+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), ReportPath(dir))
}

// Load reads the stored report in dir. A non-empty want names the report the
// caller read before; a different stored report is refused, because its ids
// and pages name other evidence.
func Load(dir, want string) (*Digest, error) {
	data, err := os.ReadFile(ReportPath(dir))
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("no stored report in %s: run `steer report` first", dir)
	}
	if err != nil {
		return nil, err
	}
	var d Digest
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("stored report %s is unreadable: %w", ReportPath(dir), err)
	}
	if d.DigestVersion != DigestVersion {
		return nil, fmt.Errorf("stored report has digest_version %d, this binary reads %d: run `steer report` again", d.DigestVersion, DigestVersion)
	}
	if want != "" && d.ReportID != want {
		return nil, fmt.Errorf("stored report is %s, not %s: a later `steer report` replaced it, so run the scan again and read its index", d.ReportID, want)
	}
	return &d, nil
}
