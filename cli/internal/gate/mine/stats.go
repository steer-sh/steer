package mine

import (
	"encoding/json"
	"math/big"
	"strconv"
	"strings"
)

const (
	// MinAfterChains is the post-install chain count below which score withholds a verdict.
	MinAfterChains = 12
	// EffectiveRatio: an intervention must at least halve incidence to be called effective.
	EffectiveRatio = 0.5
	// GrowthRatio: a rejected class must grow by half again before the refusal reopens.
	GrowthRatio = 1.5
	// Alpha is the one-sided Fisher significance threshold.
	Alpha = 0.10
	// OverlapBlock: a candidate sharing this fraction of its chains with a rejected class
	// is that class again under a new name, so register stops rather than warns.
	OverlapBlock = 0.5
)

// FisherOneSided is P(after hits <= c) with margins fixed — a/b before hit/miss, c/d
// after. It returns 1.0 when a margin is empty, so an untestable table never reads as
// significant.
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

// VerdictFor classifies an installed intervention from its before/after incidence.
func VerdictFor(bHits, bTotal int, bRate float64, aHits, aTotal int, aRate float64, minAfter int) (string, float64) {
	p := FisherOneSided(bHits, bTotal-bHits, aHits, aTotal-aHits)
	switch {
	case aTotal < minAfter:
		return "insufficient-data", p
	case bTotal == 0 || bHits == 0:
		return "no-baseline", p
	case aRate >= bRate:
		return "ineffective", p
	case aRate <= bRate*EffectiveRatio:
		if p <= Alpha {
			return "effective", p
		}
		return "promising", p
	default:
		return "partial", p
	}
}

// TrendFor classifies a rejected class by how its incidence moved after the refusal.
func TrendFor(bHits, bTotal int, bRate float64, aHits, aTotal int, aRate float64, minAfter int) (string, float64) {
	p := FisherOneSided(bHits, bTotal-bHits, aHits, aTotal-aHits)
	switch {
	case aTotal < minAfter:
		return "insufficient-data", p
	case bTotal == 0 || bHits == 0:
		return "no-baseline", p
	case aRate >= bRate*GrowthRatio:
		return "class-grew", p
	case aRate <= bRate*EffectiveRatio:
		return "class-faded", p
	default:
		return "class-steady", p
	}
}

// round4 reproduces Python round(x, 4): format to four decimals with ties-to-even, then
// parse back to the nearest double.
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
