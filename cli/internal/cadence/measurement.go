package cadence

// Scope is a record's frozen eligibility rule, carried verbatim from the stored
// record. A nil pointer is a legacy record with no scope at all.
type Scope struct {
	Version int      `json:"version"`
	All     bool     `json:"all,omitempty"`
	Harness []string `json:"harness,omitempty"`
	Repo    string   `json:"repo,omitempty"`
	// RepoRoot is the one exact repository root a v3 scope binds.
	RepoRoot string `json:"repo_root,omitempty"`
	Path     string `json:"path,omitempty"`
}

// Coverage accounts for one window's units against the scope. Eligible units are the
// denominator; unknown units withhold rather than shrink it. UnknownScope, Pending and
// UnknownEvidence break the chain corpus's unknowns down by cause; PartialExposure is
// the session corpus's own category, for a session that straddled the install.
type Coverage struct {
	Total           int `json:"total"`
	Eligible        int `json:"eligible"`
	Ineligible      int `json:"ineligible"`
	Unknown         int `json:"unknown"`
	UnknownScope    int `json:"unknown_scope"`
	UnknownEvidence int `json:"unknown_evidence"`
	Pending         int `json:"pending"`
	LedgerMissed    int `json:"ledger_missed"`
	LedgerOverdue   int `json:"ledger_overdue"`
	PartialExposure int `json:"partial_exposure"`
}

// ScopeOf reads a stored scope map, nil when the record carries none.
func ScopeOf(v any) *Scope {
	m, _ := v.(map[string]any)
	if m == nil {
		return nil
	}
	sc := &Scope{
		Version:  numInt(m["version"]),
		All:      truthy(m["all"]),
		Repo:     str(m["repo"]),
		RepoRoot: str(m["repo_root"]),
		Path:     str(m["path"]),
	}
	if list, ok := m["harness"].([]any); ok {
		for _, item := range list {
			if id := str(item); id != "" {
				sc.Harness = append(sc.Harness, id)
			}
		}
	}
	return sc
}

// CoverageOf reads one window's scope accounting.
func CoverageOf(v any) Coverage {
	m, _ := v.(map[string]any)
	if m == nil {
		return Coverage{}
	}
	return Coverage{
		Total:           numInt(m["total"]),
		Eligible:        numInt(m["eligible"]),
		Ineligible:      numInt(m["ineligible"]),
		Unknown:         numInt(m["unknown"]),
		UnknownScope:    numInt(m["unknown_scope"]),
		UnknownEvidence: numInt(m["unknown_evidence"]),
		Pending:         numInt(m["pending"]),
		LedgerMissed:    numInt(m["ledger_missed"]),
		LedgerOverdue:   numInt(m["ledger_overdue"]),
		PartialExposure: numInt(m["partial_exposure"]),
	}
}

func truthy(v any) bool {
	b, _ := v.(bool)
	return b
}
