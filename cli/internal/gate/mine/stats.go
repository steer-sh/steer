package mine

import (
	"encoding/json"
	"math"
	"math/big"
	"strconv"
	"strings"
)

// FisherOneSided is P(after hits <= c) with margins fixed — a/b before hit/miss, c/d
// after. It returns 1.0 when a margin is empty, so an untestable table never reads as
// significant. Reported beside the range; no verdict reads it.
func FisherOneSided(a, b, c, d int) float64 {
	r1, r2 := a+b, c+d
	c1, n := a+c, a+b+c+d
	if r1 == 0 || r2 == 0 || c1 == 0 || n == c1 {
		return 1.0
	}
	total := binomial(n, c1)
	p := 0.0
	for k := 0; k <= c; k++ {
		if c1-k < 0 || c1-k > r1 || k > r2 {
			continue
		}
		// comb(r1,c1-k)*comb(r2,k)/total as a correctly-rounded double, matching
		// Python's per-term int/int true division, accumulated in ascending k.
		num := new(big.Int).Mul(binomial(r1, c1-k), binomial(r2, k))
		term, _ := new(big.Rat).SetFrac(num, total).Float64()
		p += term
	}
	if p > 1.0 {
		return 1.0
	}
	return p
}

func binomial(n, k int) *big.Int {
	return new(big.Int).Binomial(int64(n), int64(k))
}

// Measurement is one record's two windows expressed as a relative-risk range.
type Measurement struct {
	Baseline    Interval // exact interval on the before-window rate
	After       Interval // running mixture sequence on the after-window rate
	Effect      Effect   // the ratio of the two
	Required    int      // clean after-chains that could reach EffectiveRatio; 0 = unreachable
	HasBaseline bool     // the before-window carries the class at all
}

// Measure builds the range. afterHits is the after-window's per-chain hit sequence in
// time order, which the running intersection needs.
func Measure(bHits, bTotal int, bRate float64, afterHits []bool, aRate float64) Measurement {
	m := Measurement{HasBaseline: bTotal > 0 && bHits > 0}
	m.Baseline = ClopperPearson(bHits, bTotal, PieceAlpha)
	m.After = RunningMixtureCS(afterHits, PieceAlpha)
	m.Effect = RelativeRisk(m.Baseline, m.After, bRate, aRate)
	m.Required = RequiredAfter(m.Baseline, EffectiveRatio)
	return m
}

// VerdictFor classifies an installed intervention from its range. It abstains — with
// collecting or inconclusive — whenever the range clears no boundary.
func VerdictFor(m Measurement, aTotal, minAfter int) string {
	if !m.HasBaseline {
		return "no-baseline"
	}
	if aTotal < minAfter {
		return "collecting"
	}
	lower, upper := m.Effect.Lower, m.Effect.Upper
	switch {
	case upper <= EffectiveRatio:
		return "effective"
	case lower > HarmfulRatio:
		return "harmful"
	case lower > EffectiveRatio:
		return "ineffective"
	case m.Required == 0 || aTotal < m.Required:
		return "collecting"
	default:
		return "inconclusive"
	}
}

// TrendFor classifies a rejected class by how its incidence moved after the refusal.
// class-inconclusive is the abstention: the range supports no conclusion yet.
func TrendFor(m Measurement, aTotal, minAfter int) string {
	if !m.HasBaseline {
		return "no-baseline"
	}
	if aTotal < minAfter {
		return "class-inconclusive"
	}
	lower, upper := m.Effect.Lower, m.Effect.Upper
	switch {
	case lower >= GrowthRatio:
		return "class-grew"
	case upper <= EffectiveRatio:
		return "class-faded"
	case lower >= EffectiveRatio && upper <= GrowthRatio:
		return "class-steady"
	default:
		return "class-inconclusive"
	}
}

// effectMap renders an Effect for the scored record. An unbounded upper bound and an
// undefined estimate are both null.
func effectMap(e Effect) map[string]any {
	m := map[string]any{
		"estimate":   nil,
		"lower":      pyFloat(round4(e.Lower)),
		"upper":      nil,
		"confidence": pyFloat(Confidence),
		"method":     e.Method,
	}
	if e.Estimate != nil {
		m["estimate"] = pyFloat(round4(*e.Estimate))
	}
	if !math.IsInf(e.Upper, 1) {
		m["upper"] = pyFloat(round4(e.Upper))
	}
	return m
}

// round4 formats to four decimals with ties-to-even, then parses back to the nearest
// double.
func round4(x float64) float64 {
	s := strconv.FormatFloat(x, 'f', 4, 64)
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// pyFloat renders a float as Python's json module does: the shortest round-tripping
// form, with a trailing ".0" on integral values (0.0, not 0). Returned as json.Number
// so a standard encoder emits it verbatim.
func pyFloat(f float64) json.Number {
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return json.Number(s)
}
