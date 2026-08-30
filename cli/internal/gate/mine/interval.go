package mine

import (
	"math"
)

// The decision constants. Alpha is the joint error budget of the relative-risk range;
// each half of the range spends PieceAlpha of it.
const (
	// Alpha is the joint error budget of the relative-risk range.
	Alpha = 0.10
	// PieceAlpha is each half's share of Alpha.
	PieceAlpha = Alpha / 2
	// Confidence is the joint coverage the reported range carries.
	Confidence = 1 - Alpha
	// EffectiveRatio: an intervention must at least halve incidence to be called effective.
	EffectiveRatio = 0.5
	// GrowthRatio: a rejected class must grow by half again before the refusal reopens.
	GrowthRatio = 1.5
	// HarmfulRatio: above it, incidence rose, so the intervention is worse than nothing.
	HarmfulRatio = 1.0
	// OverlapBlock: a candidate sharing this fraction of its chains with a rejected class
	// is that class again under a new name, so register stops rather than warns.
	OverlapBlock = 0.5
	// EffectMethod names the construction in the emitted effect record.
	EffectMethod = "cp-baseline/bb-mixture-cs-after"
	// requiredAfterCap bounds the search for required_after. Past it the answer is
	// reported as unreachable.
	requiredAfterCap = 100000
	// bisectIters drives every root search here. A fixed count keeps the result
	// bit-identical across runs.
	bisectIters = 80
)

// Interval is a closed range on a rate or a ratio. Hi may be positive infinity when a
// zero-valued denominator leaves the upper bound unbounded.
type Interval struct {
	Lo float64
	Hi float64
}

// ClopperPearson is the exact two-sided interval for hits/n at level 1-alpha. It is the
// fixed-sample construction: the baseline window is read once, not monitored.
func ClopperPearson(hits, n int, alpha float64) Interval {
	if n <= 0 {
		return Interval{0, 1}
	}
	iv := Interval{0, 1}
	if hits > 0 {
		iv.Lo = betaQuantile(alpha/2, float64(hits), float64(n-hits+1))
	}
	if hits < n {
		iv.Hi = betaQuantile(1-alpha/2, float64(hits+1), float64(n-hits))
	}
	return iv
}

// MixtureCS is the beta-binomial mixture confidence sequence for a Bernoulli rate after
// n trials with the given hit count, at level 1-alpha. The interval is the set of rates
// whose mixture martingale stays under 1/alpha, so it holds uniformly over n and a
// cadence may read it as often as it likes. Beta(1/2,1/2) is the mixing distribution of
// that martingale, not a prior on the verdict.
func MixtureCS(hits, n int, alpha float64) Interval {
	if n <= 0 || alpha <= 0 || alpha >= 1 {
		return Interval{0, 1}
	}
	// log M_n(p), with the mixing normaliser B(1/2,1/2) divided out and the binomial
	// coefficient omitted because it cancels.
	logBB := logBeta(float64(hits)+0.5, float64(n-hits)+0.5) - logBeta(0.5, 0.5)
	threshold := math.Log(1 / alpha)
	// exceeds reports M_n(p) >= 1/alpha, so p sits outside the sequence.
	exceeds := func(p float64) bool {
		return logBB-xLogY(hits, p)-xLogY(n-hits, 1-p) >= threshold
	}
	center := float64(hits) / float64(n)
	iv := Interval{0, 1}
	if exceeds(0) {
		iv.Lo = bisectFalseToTrue(exceeds, center, 0)
	}
	if exceeds(1) {
		iv.Hi = bisectFalseToTrue(exceeds, center, 1)
	}
	return iv
}

// RunningMixtureCS intersects the mixture sequence over every prefix of the hit
// sequence, so a bound never loosens after a cadence reported the tighter one.
// An empty intersection says the rate did not hold still across the window, which the
// sequence cannot describe; the whole-window interval is then the widest valid answer.
func RunningMixtureCS(hits []bool, alpha float64) Interval {
	iv := Interval{0, 1}
	step := iv
	seen := 0
	for i, hit := range hits {
		if hit {
			seen++
		}
		step = MixtureCS(seen, i+1, alpha)
		if step.Lo > iv.Lo {
			iv.Lo = step.Lo
		}
		if step.Hi < iv.Hi {
			iv.Hi = step.Hi
		}
	}
	if iv.Lo > iv.Hi {
		return step
	}
	return iv
}

// Effect is the reported relative-risk range: after-rate over baseline rate.
type Effect struct {
	Estimate   *float64
	Lower      float64
	Upper      float64 // +Inf when the baseline lower bound is zero
	Confidence float64
	Method     string
}

// RelativeRisk divides the two rate ranges. Each bound pairs with the opposing baseline
// bound, which is what makes the ratio conservative at the joint level.
func RelativeRisk(baseline, after Interval, bRate, aRate float64) Effect {
	e := Effect{Confidence: Confidence, Method: EffectMethod}
	if bRate > 0 {
		est := aRate / bRate
		e.Estimate = &est
	}
	if baseline.Hi > 0 {
		e.Lower = after.Lo / baseline.Hi
	}
	switch {
	case after.Hi == 0:
		e.Upper = 0
	case baseline.Lo <= 0:
		e.Upper = math.Inf(1)
	default:
		e.Upper = after.Hi / baseline.Lo
	}
	return e
}

// RequiredAfter is the smallest clean after-window — one carrying no hits at all — whose
// upper bound could reach the target ratio. It returns 0 when no window of any size can
// get there, which a zero baseline lower bound causes.
func RequiredAfter(baseline Interval, target float64) int {
	if baseline.Lo <= 0 || target <= 0 {
		return 0
	}
	want := target * baseline.Lo
	// MixtureCS(0, n) falls monotonically in n, so double until it clears, then bisect.
	reaches := func(n int) bool { return MixtureCS(0, n, PieceAlpha).Hi <= want }
	n := 1
	for !reaches(n) {
		if n >= requiredAfterCap {
			return 0
		}
		if n *= 2; n > requiredAfterCap {
			n = requiredAfterCap
		}
	}
	lo, hi := n/2, n // lo fails or is zero; hi reaches
	for hi-lo > 1 {
		mid := (lo + hi) / 2
		if reaches(mid) {
			hi = mid
		} else {
			lo = mid
		}
	}
	return hi
}

// xLogY is x*log(y) with the 0*log(0) = 0 convention the likelihood needs.
func xLogY(x int, y float64) float64 {
	if x == 0 {
		return 0
	}
	return float64(x) * math.Log(y)
}

// bisectFalseToTrue finds the boundary between a point where pred is false and one where
// it is true, for a pred that switches once between them.
func bisectFalseToTrue(pred func(float64) bool, falseAt, trueAt float64) float64 {
	for i := 0; i < bisectIters; i++ {
		mid := (falseAt + trueAt) / 2
		if pred(mid) {
			trueAt = mid
		} else {
			falseAt = mid
		}
	}
	return (falseAt + trueAt) / 2
}

func logBeta(a, b float64) float64 {
	la, _ := math.Lgamma(a)
	lb, _ := math.Lgamma(b)
	lab, _ := math.Lgamma(a + b)
	return la + lb - lab
}

// betaQuantile inverts the regularized incomplete beta function by bisection over its
// monotone CDF.
func betaQuantile(p, a, b float64) float64 {
	if p <= 0 {
		return 0
	}
	if p >= 1 {
		return 1
	}
	return bisectFalseToTrue(func(x float64) bool { return betaCDF(x, a, b) >= p }, 0, 1)
}

// betaCDF is the regularized incomplete beta function I_x(a,b), by the continued-fraction
// expansion with the symmetry reflection that keeps it converging.
func betaCDF(x, a, b float64) float64 {
	if x <= 0 {
		return 0
	}
	if x >= 1 {
		return 1
	}
	front := math.Exp(a*math.Log(x) + b*math.Log(1-x) - logBeta(a, b))
	if x < (a+1)/(a+b+2) {
		return front * betaContinuedFraction(x, a, b) / a
	}
	return 1 - front*betaContinuedFraction(1-x, b, a)/b
}

// betaContinuedFraction evaluates the Lentz recurrence for the incomplete beta.
func betaContinuedFraction(x, a, b float64) float64 {
	const tiny = 1e-300
	qab, qap, qam := a+b, a+1, a-1
	c, d := 1.0, 1-qab*x/qap
	if math.Abs(d) < tiny {
		d = tiny
	}
	d = 1 / d
	h := d
	for m := 1; m <= 300; m++ {
		fm := float64(m)
		m2 := 2 * fm
		// Each iteration applies the even then the odd numerator of the expansion.
		for _, num := range [2]float64{
			fm * (b - fm) * x / ((qam + m2) * (a + m2)),
			-(a + fm) * (qab + fm) * x / ((a + m2) * (qap + m2)),
		} {
			d = 1 + num*d
			if math.Abs(d) < tiny {
				d = tiny
			}
			d = 1 / d
			c = 1 + num/c
			if math.Abs(c) < tiny {
				c = tiny
			}
			h *= d * c
		}
		if math.Abs(1-d*c) < 1e-15 {
			break
		}
	}
	return h
}
