package cadence

import "github.com/steer-sh/steer-cli/internal/gate/mine"

// Scope is a record's frozen eligibility rule, carried verbatim from the stored
// record. A nil pointer is a legacy record with no scope at all.
type Scope struct {
	Version int      `json:"version"`
	All     bool     `json:"all,omitempty"`
	Harness []string `json:"harness,omitempty"`
	Repo    string   `json:"repo,omitempty"`
	Path    string   `json:"path,omitempty"`
}

// Coverage accounts for one window's chains against the scope. Eligible chains are
// the denominator; unknown chains withhold rather than shrink it.
type Coverage struct {
	Total           int `json:"total"`
	Eligible        int `json:"eligible"`
	Ineligible      int `json:"ineligible"`
	Unknown         int `json:"unknown"`
	UnknownScope    int `json:"unknown_scope"`
	UnknownEvidence int `json:"unknown_evidence"`
	Pending         int `json:"pending"`
}

// Effect is the joint relative-risk range the verdict reads. Estimate is null with no
// baseline rate; Upper is null when the range is unbounded above.
type Effect struct {
	Estimate   *float64 `json:"estimate"`
	Lower      float64  `json:"lower"`
	Upper      *float64 `json:"upper"`
	Confidence float64  `json:"confidence"`
	Method     string   `json:"method"`
}

// ScopeOf reads a stored scope map, nil when the record carries none.
func ScopeOf(v any) *Scope {
	m, _ := v.(map[string]any)
	if m == nil {
		return nil
	}
	sc := &Scope{
		Version: numInt(m["version"]),
		All:     truthy(m["all"]),
		Repo:    str(m["repo"]),
		Path:    str(m["path"]),
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
	}
}

// EffectOf reads the relative-risk range.
func EffectOf(v any) Effect {
	m, _ := v.(map[string]any)
	if m == nil {
		return Effect{}
	}
	e := Effect{
		Lower:      numFloat(m["lower"]),
		Confidence: numFloat(m["confidence"]),
		Method:     str(m["method"]),
	}
	if m["estimate"] != nil {
		x := numFloat(m["estimate"])
		e.Estimate = &x
	}
	if m["upper"] != nil {
		x := numFloat(m["upper"])
		e.Upper = &x
	}
	return e
}

// RequiredAfterOf reads the minimum clean after-window, nil when no window of any
// size could reach the effect target.
func RequiredAfterOf(v any) *int {
	if v == nil {
		return nil
	}
	n := numInt(v)
	return &n
}

// ConfoundsOf reads a scored confound list, never nil so a consumer sees [].
func ConfoundsOf(v any) []mine.Confound {
	c, _ := v.([]mine.Confound)
	if c == nil {
		return []mine.Confound{}
	}
	return c
}

func truthy(v any) bool {
	b, _ := v.(bool)
	return b
}
