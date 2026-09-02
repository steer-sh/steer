package mine

import (
	"path"
	"sort"
	"strings"
)

// Confound is a measurement condition that makes the two windows incomparable. A hard
// confound withholds the verdict; a soft one is reported and withholds nothing.
type Confound struct {
	Category string   `json:"category"`
	Reason   string   `json:"reason"`
	Before   []string `json:"before"`
	After    []string `json:"after"`
}

// PathKind buckets one changed path by its lowercased extension. A path with no
// extension is "(none)". Repo basename is already its own category, so the bucket must
// not restate a directory layout.
func PathKind(p string) string {
	ext := strings.ToLower(path.Ext(p))
	if ext == "" {
		return "(none)"
	}
	return strings.TrimPrefix(ext, ".")
}

// pathKindsIn is the union of path kinds over the chains that stamped or recovered a
// path list. Chains with unknown paths contribute nothing rather than a false kind.
func pathKindsIn(chains []Chain) []string {
	var seen []string
	for _, ch := range chains {
		if !ch.PathsKnown {
			continue
		}
		for _, p := range ch.Paths {
			seen = appendUnique(seen, PathKind(p))
		}
	}
	sort.Strings(seen)
	return seen
}

// authorModelsIn is the union of author models over the chains that carry the stamp.
func authorModelsIn(chains []Chain) []string {
	var seen []string
	for _, ch := range chains {
		if !ch.AuthorModelKnown {
			continue
		}
		for _, m := range ch.AuthorModels {
			seen = appendUnique(seen, m)
		}
	}
	sort.Strings(seen)
	return seen
}

// harnessesIn is the union of harnesses over the chains that carry the stamp.
func harnessesIn(chains []Chain) []string {
	var seen []string
	for _, ch := range chains {
		if ch.HarnessKnown {
			seen = appendUnique(seen, ch.Harness)
		}
	}
	sort.Strings(seen)
	return seen
}

// reposIn is the union of repository basenames over the chains that carry one.
func reposIn(chains []Chain) []string {
	var seen []string
	for _, ch := range chains {
		if ch.RepoKnown {
			seen = appendUnique(seen, ch.Repo)
		}
	}
	sort.Strings(seen)
	return seen
}

func sortedHosts(chains []Chain) []string {
	hosts := HostsIn(chains)
	sort.Strings(hosts)
	return hosts
}

// instrumentDrift compares the after-window's reviewer identities against the record's
// registered one, by dimension rather than as whole strings. It returns the confound
// and its grade, or nil when the reviewer did not move.
//
// Grading by dimension is what lets a row predating `instrument_fields` be compared at
// all: the legacy string is parsed into the same dimensions, so a pair whose only
// difference is effort reads as that one change instead of as a full mismatch.
func instrumentDrift(before, after []Chain, authored string) (*Confound, string) {
	base := BaselineInstrument(authored, InstrumentUses(before))
	afterUses := InstrumentUses(after)

	grade := ""
	var drifted []string
	dims := map[string]bool{}
	undimensioned := false
	for _, u := range afterUses {
		g := gradeDrift(authored, base, u)
		if g == "" {
			continue
		}
		if g == DriftMajor {
			grade = DriftMajor
		} else if grade == "" {
			grade = DriftMinor
		}
		drifted = appendUnique(drifted, u.Label())
		named := DriftDims(base, u.Fields)
		if len(named) == 0 {
			undimensioned = true
		}
		for _, d := range named {
			dims[d] = true
		}
	}
	if grade == "" {
		return nil, ""
	}
	// A drift no dimension explains keeps the undimensioned sentence beside the named
	// ones. Reporting the dimensions the OTHER identities moved as the whole reason
	// names the smaller half of a drift graded on the larger.
	const unread = "an after-window chain was reviewed by a different instrument than the registered baseline"
	reason := unread
	if len(dims) > 0 {
		names := make([]string, 0, len(dims))
		for d := range dims {
			names = append(names, d)
		}
		sort.Strings(names)
		reason = "the reviewer's " + strings.Join(names, ", ") + " changed between the windows"
		if undimensioned {
			reason += ", and " + unread
		}
	}
	return &Confound{
		Category: "instrument",
		Reason:   reason,
		Before:   []string{InstrumentUse{ID: authored, Fields: base}.Label()},
		After:    drifted,
	}, grade
}

// Confounds compares the two windows' categorical supports. It returns the hard
// confounds that withhold the verdict and the soft composition warnings that do not.
// authored is the record's registered baseline instrument, empty when it predates the
// field.
//
// An instrument drift is placed by its grade. A major drift changes what the reviewer
// is, so the two windows measure different things and the verdict is withheld. A minor
// drift — effort or run count at one identity — is reported beside the range instead:
// the same reviewer worked harder or was sampled more, which shifts the finding rate it
// reports without making the windows incomparable, and withholding on it discards every
// measurement that spans a settings edit.
func Confounds(before, after []Chain, authored string) (hard, soft []Confound) {
	if authored != "" {
		if c, grade := instrumentDrift(before, after, authored); c != nil {
			if grade == DriftMajor {
				hard = append(hard, *c)
			} else {
				soft = append(soft, *c)
			}
		}
	}
	categories := []struct {
		name string
		of   func([]Chain) []string
	}{
		{"host", sortedHosts},
		{"harness", harnessesIn},
		{"repo", reposIn},
		{"author_model", authorModelsIn},
		{"path_kind", pathKindsIn},
	}
	for _, cat := range categories {
		b, a := cat.of(before), cat.of(after)
		// An empty support on either side is unknown, not a shift: a category no chain
		// stamped says nothing about whether the windows differ.
		if len(b) == 0 || len(a) == 0 {
			continue
		}
		switch {
		case disjoint(b, a):
			hard = append(hard, Confound{
				Category: cat.name,
				Reason:   "the before and after windows share no " + cat.name,
				Before:   b, After: a,
			})
		case !sameStringSet(b, a):
			soft = append(soft, Confound{
				Category: cat.name,
				Reason:   "the " + cat.name + " mix changed between the windows",
				Before:   b, After: a,
			})
		}
	}
	return hard, soft
}

func disjoint(a, b []string) bool {
	set := map[string]bool{}
	for _, x := range a {
		set[x] = true
	}
	for _, y := range b {
		if set[y] {
			return false
		}
	}
	return true
}

// differingFrom is the members of list that are not v, in order.
func differingFrom(list []string, v string) []string {
	var out []string
	for _, x := range list {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

// confoundList renders confounds for the scored record, [] rather than null when empty.
func confoundList(c []Confound) []Confound {
	if c == nil {
		return []Confound{}
	}
	return c
}
