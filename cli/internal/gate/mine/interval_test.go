package mine

import (
	"math"
	"testing"
)

func near(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s: got %.6f, want %.6f", name, got, want)
	}
}

// evenSeq spreads h hits over n trials, so a fixture never fakes a mid-window rate change.
func evenSeq(h, n int) []bool {
	s := make([]bool, n)
	for i := 0; i < h; i++ {
		s[(i*n)/h] = true
	}
	return s
}

// TestClopperPearsonFixtures pins the baseline interval against the binomial tail sums
// that define it, solved independently.
func TestClopperPearsonFixtures(t *testing.T) {
	cases := []struct {
		hits, n int
		lo, hi  float64
		name    string
	}{
		{2, 20, 0.01234853, 0.31698271, "2/20"},
		{10, 20, 0.27195785, 0.72804215, "10/20"},
		{50, 100, 0.39832113, 0.60167887, "50/100"},
		{1, 10, 0.00252858, 0.44501612, "1/10"},
	}
	for _, c := range cases {
		iv := ClopperPearson(c.hits, c.n, 0.05)
		near(t, c.name+" lower", iv.Lo, c.lo, 1e-6)
		near(t, c.name+" upper", iv.Hi, c.hi, 1e-6)
	}
}

// TestClopperPearsonZeroCells pins the closed forms at the two ends, where the interval
// is one-sided.
func TestClopperPearsonZeroCells(t *testing.T) {
	zero := ClopperPearson(0, 20, 0.05)
	near(t, "0/20 lower is exactly zero", zero.Lo, 0, 0)
	near(t, "0/20 upper", zero.Hi, 1-math.Pow(0.025, 1.0/20), 1e-9)

	full := ClopperPearson(20, 20, 0.05)
	near(t, "20/20 lower", full.Lo, math.Pow(0.025, 1.0/20), 1e-9)
	near(t, "20/20 upper is exactly one", full.Hi, 1, 0)

	empty := ClopperPearson(0, 0, 0.05)
	if empty.Lo != 0 || empty.Hi != 1 {
		t.Errorf("an empty window is the whole unit interval, got %v", empty)
	}
}

// TestMixtureCSContainsTheObservedRate checks the basic shape: the interval brackets the
// observed rate and narrows as the window grows.
func TestMixtureCSContainsTheObservedRate(t *testing.T) {
	var width float64 = 2
	for _, n := range []int{4, 16, 64, 256, 1024} {
		iv := MixtureCS(n/4, n, PieceAlpha)
		rate := 0.25
		if iv.Lo > rate || iv.Hi < rate {
			t.Errorf("n=%d: %v excludes the observed rate", n, iv)
		}
		if w := iv.Hi - iv.Lo; w >= width {
			t.Errorf("n=%d: width %.4f did not shrink below %.4f", n, w, width)
		} else {
			width = w
		}
	}
	if iv := MixtureCS(0, 0, PieceAlpha); iv.Lo != 0 || iv.Hi != 1 {
		t.Errorf("no trials is the whole unit interval, got %v", iv)
	}
}

// TestMixtureCSZeroCells pins that a clean or a saturated window pins the far bound to
// the end of the scale rather than to an interior root.
func TestMixtureCSZeroCells(t *testing.T) {
	clean := MixtureCS(0, 50, PieceAlpha)
	if clean.Lo != 0 {
		t.Errorf("a clean window has a zero lower bound, got %.6f", clean.Lo)
	}
	if clean.Hi >= 1 {
		t.Errorf("a clean window bounds the rate below one, got %.6f", clean.Hi)
	}
	full := MixtureCS(50, 50, PieceAlpha)
	if full.Hi != 1 {
		t.Errorf("a saturated window has a unit upper bound, got %.6f", full.Hi)
	}
	if full.Lo <= 0 {
		t.Errorf("a saturated window bounds the rate above zero, got %.6f", full.Lo)
	}
}

// TestRunningIntersectionIsMonotone pins that neither bound ever loosens as chains
// arrive, which is what lets a cadence report a bound and keep it.
func TestRunningIntersectionIsMonotone(t *testing.T) {
	seq := []bool{true, false, false, true, false, false, false, true, false, false,
		false, false, true, false, false, false}
	prev := Interval{0, 1}
	for i := 1; i <= len(seq); i++ {
		iv := RunningMixtureCS(seq[:i], PieceAlpha)
		if iv.Lo < prev.Lo {
			t.Errorf("step %d: lower bound loosened %.6f -> %.6f", i, prev.Lo, iv.Lo)
		}
		if iv.Hi > prev.Hi {
			t.Errorf("step %d: upper bound loosened %.6f -> %.6f", i, prev.Hi, iv.Hi)
		}
		prev = iv
	}
}

// TestRunningIntersectionFallsBackWhenEmpty covers a window whose rate changed hard
// enough to empty the intersection: the answer stays an ordered interval.
func TestRunningIntersectionFallsBackWhenEmpty(t *testing.T) {
	seq := make([]bool, 80)
	for i := 0; i < 40; i++ {
		seq[i] = true
	}
	iv := RunningMixtureCS(seq, PieceAlpha)
	if iv.Lo > iv.Hi {
		t.Fatalf("bounds inverted: %v", iv)
	}
	if want := MixtureCS(40, 80, PieceAlpha); iv != want {
		t.Errorf("expected the whole-window interval %v, got %v", want, iv)
	}
}

// TestRunningIntersectionCoversUniformly is the coverage claim, checked exhaustively:
// over every Bernoulli sequence of length 10, the total probability that the running
// intersection ever excludes the true rate stays inside the error budget.
func TestRunningIntersectionCoversUniformly(t *testing.T) {
	const n = 10
	for _, p := range []float64{0.1, 0.3, 0.5, 0.7, 0.9} {
		miss := 0.0
		for mask := 0; mask < 1<<n; mask++ {
			seq := make([]bool, n)
			hits := 0
			for i := 0; i < n; i++ {
				if mask&(1<<i) != 0 {
					seq[i] = true
					hits++
				}
			}
			excluded := false
			for i := 1; i <= n && !excluded; i++ {
				iv := RunningMixtureCS(seq[:i], PieceAlpha)
				excluded = p < iv.Lo || p > iv.Hi
			}
			if excluded {
				miss += math.Pow(p, float64(hits)) * math.Pow(1-p, float64(n-hits))
			}
		}
		if miss > PieceAlpha {
			t.Errorf("p=%.1f: miss probability %.4f exceeds the budget %.4f", p, miss, PieceAlpha)
		}
	}
}

// TestRelativeRiskPairsOpposingBounds pins the division and its unbounded case.
func TestRelativeRiskPairsOpposingBounds(t *testing.T) {
	base := Interval{0.2, 0.8}
	after := Interval{0.1, 0.4}
	e := RelativeRisk(base, after, 0.5, 0.25)
	near(t, "lower is after-low over baseline-high", e.Lower, 0.125, 1e-12)
	near(t, "upper is after-high over baseline-low", e.Upper, 2.0, 1e-12)
	if e.Estimate == nil || *e.Estimate != 0.5 {
		t.Errorf("estimate should be the rate ratio, got %v", e.Estimate)
	}
	if e.Confidence != Confidence || e.Method != EffectMethod {
		t.Errorf("effect should carry its level and method, got %v", e)
	}

	unbounded := RelativeRisk(Interval{0, 0.3}, Interval{0.1, 0.5}, 0, 0.2)
	if !math.IsInf(unbounded.Upper, 1) {
		t.Errorf("a zero baseline lower bound leaves the upper bound unbounded, got %v", unbounded.Upper)
	}
	if unbounded.Estimate != nil {
		t.Errorf("a zero baseline rate has no ratio to estimate, got %v", *unbounded.Estimate)
	}

	clean := RelativeRisk(Interval{0, 0.3}, Interval{0, 0}, 0, 0)
	if clean.Upper != 0 {
		t.Errorf("a bounded-at-zero after-window has a zero upper bound, got %v", clean.Upper)
	}
}

// TestRequiredAfterIsTheExposureFloor pins that the answer is the first clean window
// reaching the target, that it is not one chain fewer, and that it falls as the baseline
// tightens.
func TestRequiredAfterIsTheExposureFloor(t *testing.T) {
	base := ClopperPearson(60, 100, PieceAlpha)
	n := RequiredAfter(base, EffectiveRatio)
	if n <= 0 {
		t.Fatalf("a measured baseline has a reachable floor, got %d", n)
	}
	want := EffectiveRatio * base.Lo
	if got := MixtureCS(0, n, PieceAlpha).Hi; got > want {
		t.Errorf("n=%d does not reach the target: %.6f > %.6f", n, got, want)
	}
	if got := MixtureCS(0, n-1, PieceAlpha).Hi; got <= want {
		t.Errorf("n=%d is not minimal: n-1 already reaches %.6f", n, got)
	}
	if loose := RequiredAfter(ClopperPearson(6, 100, PieceAlpha), EffectiveRatio); loose <= n {
		t.Errorf("a looser baseline needs more exposure, got %d against %d", loose, n)
	}
	if got := RequiredAfter(ClopperPearson(0, 20, PieceAlpha), EffectiveRatio); got != 0 {
		t.Errorf("a baseline with no lower bound is unreachable, got %d", got)
	}
}
