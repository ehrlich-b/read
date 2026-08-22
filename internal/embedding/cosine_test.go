package embedding

import (
	"math"
	"testing"
)

// almostEqual reports whether a and b are within tol of each other.
//
// Tolerance rationale: every value compared here is float32, which carries
// about 7 significant decimal digits (absolute error ~1e-7 for values near 1).
// We use tol = 1e-4 throughout: a ~1000x margin over float32 noise, yet far
// tighter than any genuinely wrong answer (which differs from the hand-derived
// or differentially computed value by 1e-3 or more).
func almostEqual(a, b, tol float32) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff <= tol
}

// l2Norm returns the Euclidean norm of v in float32, mirroring how the
// package accumulates norms in Cosine and Normalize.
func l2Norm(v []float32) float32 {
	var sum float32
	for _, x := range v {
		sum += x * x
	}
	return float32(math.Sqrt(float64(sum)))
}

func TestCosineExactCases(t *testing.T) {
	// Identical vectors: cos = 1, exactly representable.
	if got := Cosine([]float32{1, 2}, []float32{1, 2}); got != 1 {
		t.Errorf("identical vectors: Cosine = %v, want 1", got)
	}
	// Opposite vectors: cos = -1, exactly representable.
	if got := Cosine([]float32{1, 0}, []float32{-1, 0}); got != -1 {
		t.Errorf("opposite vectors: Cosine = %v, want -1", got)
	}
	// Orthogonal unit vectors: cos = 0, exactly representable.
	if got := Cosine([]float32{1, 0}, []float32{0, 1}); got != 0 {
		t.Errorf("orthogonal vectors: Cosine = %v, want 0", got)
	}
}

func TestCosineHandComputed(t *testing.T) {
	// a = [1, 2, 3], b = [4, 5, 6], computed by hand:
	//   dot    = 1*4 + 2*5 + 3*6  = 4 + 10 + 18     = 32
	//   |a|^2  = 1 + 4 + 9        = 14               |a| = sqrt(14)  ≈ 3.741657
	//   |b|^2  = 16 + 25 + 36     = 77               |b| = sqrt(77)  ≈ 8.774964
	//   denom  = sqrt(14) * sqrt(77) = sqrt(1078)    ≈ 32.832910
	//   cos    = 32 / 32.832910    ≈ 0.9746318
	got := Cosine([]float32{1, 2, 3}, []float32{4, 5, 6})
	if !almostEqual(got, 0.9746318, 1e-4) {
		t.Errorf("Cosine([1,2,3],[4,5,6]) = %v, want 0.9746318", got)
	}
}

func TestCosineZeroVector(t *testing.T) {
	// denom == 0 guard: returns exactly 0, not NaN.
	got := Cosine([]float32{0, 0}, []float32{1, 1})
	if got != 0 {
		t.Errorf("Cosine(zero, one) = %v, want exactly 0 (denom==0 guard)", got)
	}
	if math.IsNaN(float64(got)) {
		t.Error("Cosine(zero, one) returned NaN")
	}
}

func TestCosineScaleInvariance(t *testing.T) {
	// Differential: scaling each argument by a different positive factor must
	// not change the cosine. 2a and 3b are exact in float32, so any deviation
	// is pure float32 noise, not a hard-coded expected number.
	a := []float32{1, 2, 3}
	b := []float32{4, 5, 6}
	base := Cosine(a, b)
	scaled := Cosine([]float32{2, 4, 6}, []float32{12, 15, 18})
	if !almostEqual(base, scaled, 1e-4) {
		t.Errorf("Cosine(a,b) = %v, Cosine(2a,3b) = %v, want equal within 1e-4", base, scaled)
	}
}

func TestCosineLengthAsymmetry(t *testing.T) {
	// PIN — LENGTH ASYMMETRY (do NOT fix cosine.go; this is a finding).
	// The loop is `for i := range a` but indexes `b[i]`, so Cosine depends on
	// argument order.

	// Case 1: len(b) < len(a) panics with an index-out-of-range error.
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("Cosine with len(b) < len(a): want index-out-of-range panic, got none")
			}
		}()
		Cosine([]float32{1, 2, 3}, []float32{1, 2})
	}()

	// Case 2: len(b) > len(a) does NOT panic: the tail of b is silently
	// ignored and normB is computed over only the first len(a) entries, so the
	// result is not the cosine of the two full vectors.
	a := []float32{1, 0}
	b := []float32{1, 0, 1} // extra trailing dimension
	got := Cosine(a, b)
	// The true cosine of the padded pair (a extended with a zero) is:
	//   dot   = 1*1 + 0*0 + 0*1 = 1,  |a|^2 = 1,  |b|^2 = 2
	//   cos   = 1 / (1 * sqrt(2)) = 0.70710678...
	// The function drops the tail of b and reports 1 instead.
	if got != 1 {
		t.Errorf("Cosine(a, b) with len(b)>len(a) = %v, want 1 (tail of b ignored)", got)
	}
	if almostEqual(got, 0.70710678, 1e-3) {
		t.Error("Cosine(a, b) equals the true cosine 0.7071 — the tail was NOT ignored")
	}
	// The asymmetry is directly visible in the reversed order: Cosine(b, a)
	// panics, because the loop now runs over the longer b and indexes the
	// shorter a. (A direct Cosine(a,b) != Cosine(b,a) numeric assertion is
	// impossible: the reversed call never returns a value.)
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("Cosine(b, a) (reversed, len(b)>len(a)): want panic, got none")
			}
		}()
		Cosine(b, a)
	}()
}

func TestNormalizeUnitNorm(t *testing.T) {
	vectors := [][]float32{
		{3, 4},       // norm 5
		{-1, 2, -2},  // norm 3, includes negative components
		{1, 1, 1, 1}, // norm 2
		{5, -12},     // norm 13, Pythagorean triple
	}
	for _, v := range vectors {
		out := Normalize(v)
		if n := l2Norm(out); !almostEqual(n, 1, 1e-4) {
			t.Errorf("Normalize(%v): L2 norm = %v, want 1", v, n)
		}
	}
}

func TestNormalizeZeroVector(t *testing.T) {
	// norm == 0 guard: returned unchanged, element-wise, with no NaN/Inf.
	v := []float32{0, 0, 0}
	out := Normalize(v)
	if len(out) != len(v) {
		t.Fatalf("Normalize(zero) changed length: %d -> %d", len(v), len(out))
	}
	for i, x := range out {
		if x != 0 {
			t.Errorf("Normalize(zero)[%d] = %v, want 0 (unchanged)", i, x)
		}
		if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
			t.Errorf("Normalize(zero)[%d] = %v, want no NaN/Inf", i, x)
		}
	}
}

func TestNormalizeAliasing(t *testing.T) {
	// PIN — Normalize mutates in place and returns the SAME backing array.
	// A caller that expects a copy gets a corrupted original.
	v := []float32{3, 4}
	out := Normalize(v)
	if &out[0] != &v[0] {
		t.Fatal("Normalize did not return the same backing array as the input")
	}
	if got := out[0]; !almostEqual(got, 0.6, 1e-4) {
		t.Errorf("Normalize([3,4])[0] = %v, want 0.6", got)
	}
	out[0] = 123
	if v[0] != 123 {
		t.Errorf("writing out[0] = 123 left v[0] = %v — returned slice does not alias input", v[0])
	}
}

func TestBlendUnitNorm(t *testing.T) {
	// Blend always normalizes: output has L2 norm 1, even for asymmetric
	// weights.
	a := []float32{1, 2, 3}
	b := []float32{4, 5, 6}
	for _, w := range [][2]float32{{0.3, 0.7}, {0.9, 0.1}, {1, 0}, {0, 1}} {
		out := Blend(a, b, w[0], w[1])
		if n := l2Norm(out); !almostEqual(n, 1, 1e-4) {
			t.Errorf("Blend(a,b,%v,%v): L2 norm = %v, want 1", w[0], w[1], n)
		}
	}
}

func TestBlendSelfAgainstNormalize(t *testing.T) {
	// Differential: Blend(a, a, 0.5, 0.5) is normalize(a), because
	// 0.5*a[i] + 0.5*a[i] == a[i] exactly in float32. Pass Normalize a COPY:
	// Normalize mutates its input in place.
	a := []float32{1, 2, 3}
	got := Blend(a, a, 0.5, 0.5)
	want := Normalize(append([]float32(nil), a...))
	if len(got) != len(want) {
		t.Fatalf("lengths differ: Blend %d vs Normalize %d", len(got), len(want))
	}
	for i := range want {
		if !almostEqual(got[i], want[i], 1e-4) {
			t.Errorf("dim %d: Blend = %v, Normalize = %v", i, got[i], want[i])
		}
	}
}

func TestEffectiveAnchorNilCentroid(t *testing.T) {
	// PIN — EffectiveAnchor(static, nil) returns the caller's OWN slice, not a
	// copy. A caller who later normalizes the result silently rewrites the
	// stored static anchor.
	static := []float32{1, 2, 3}
	out := EffectiveAnchor(static, nil)
	if len(out) != len(static) {
		t.Fatalf("length changed: %d vs %d", len(static), len(out))
	}
	for i := range static {
		if out[i] != static[i] {
			t.Errorf("dim %d: out = %v, static = %v", i, out[i], static[i])
		}
	}
	if &out[0] != &static[0] {
		t.Fatal("EffectiveAnchor(static, nil) did not return the caller's backing array")
	}
	out[0] = 99
	if static[0] != 99 {
		t.Errorf("mutating the returned slice left static[0] = %v — no aliasing", static[0])
	}
}

func TestTopNOrdering(t *testing.T) {
	// Query q = [1, 0]; similarities against the candidates, arranged by hand
	// to be clearly distinct:
	//   [1, 0] -> 1            (index 0)
	//   [1, 1] -> 1/sqrt(2) ~ 0.7071  (index 1)
	//   [1, 2] -> 1/sqrt(5) ~ 0.4472  (index 2)
	//   [0, 1] -> 0            (index 3)
	q := []float32{1, 0}
	candidates := [][]float32{{1, 0}, {1, 1}, {1, 2}, {0, 1}}

	got := TopN(q, candidates, 4)
	want := []int{0, 1, 2, 3}
	if len(got) != len(want) {
		t.Fatalf("TopN(n=4): len = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Index != w {
			t.Errorf("TopN(n=4) rank %d: Index = %d, want %d", i, got[i].Index, w)
		}
	}

	got = TopN(q, candidates, 2)
	if len(got) != 2 {
		t.Fatalf("TopN(n=2): len = %d, want 2", len(got))
	}
	for i, w := range []int{0, 1} {
		if got[i].Index != w {
			t.Errorf("TopN(n=2) rank %d: Index = %d, want %d", i, got[i].Index, w)
		}
	}
}

func TestTopNClampAndEmpty(t *testing.T) {
	q := []float32{1, 0}
	candidates := [][]float32{{1, 0}, {0, 1}}

	// n larger than the candidate count is clamped and returns all candidates.
	if got := TopN(q, candidates, 10); len(got) != 2 {
		t.Errorf("TopN(n=10): len = %d, want 2 (clamped)", len(got))
	}
	// n == 0 returns an empty slice.
	if got := TopN(q, candidates, 0); len(got) != 0 {
		t.Errorf("TopN(n=0): len = %d, want 0", len(got))
	}
	// len(candidates) == 0 returns empty for any positive n.
	if got := TopN(q, nil, 5); len(got) != 0 {
		t.Errorf("TopN(empty, n=5): len = %d, want 0", len(got))
	}
}

func TestTopNNegativePanics(t *testing.T) {
	// PIN — n negative panics: the final `matches[:n]` slice expression is
	// evaluated with a negative bound and no guard exists.
	defer func() {
		if r := recover(); r == nil {
			t.Error("TopN with n = -1: want panic, got none")
		}
	}()
	TopN([]float32{1, 0}, [][]float32{{1, 0}}, -1)
}

func TestTopNTiesOrderUnspecified(t *testing.T) {
	// PIN — the sort is sort.Slice, which is NOT stable. The relative order of
	// exactly-tied candidates is unspecified behaviour, so we deliberately do
	// NOT assert a tie order (that assertion could fail on a future Go
	// release). We only assert the guaranteed property: the returned set has
	// the right size, every returned similarity is >= every omitted one, and
	// the returned indices are exactly the top-n candidates' set.
	q := []float32{1, 0}
	candidates := [][]float32{{1, 0}, {1, 0}, {0, 1}, {0, 1}}
	got := TopN(q, candidates, 2)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	seen := map[int]bool{}
	for _, m := range got {
		seen[m.Index] = true
		if m.Similarity != 1 {
			t.Errorf("returned match (index %d) has similarity %v, want 1", m.Index, m.Similarity)
		}
	}
	// The two sim-1 candidates are candidates 0 and 1, so the returned set
	// must be exactly {0, 1} regardless of tie order.
	if !seen[0] || !seen[1] {
		t.Errorf("returned indices = %v, want set {0 1}", got)
	}
	// Every returned similarity must be >= every omitted one (omitted are 0).
	for _, m := range got {
		if m.Similarity < 0 {
			t.Errorf("returned similarity %v is below an omitted similarity", m.Similarity)
		}
	}
}

func TestEffectiveAnchorBlendDifferential(t *testing.T) {
	// Differential: with a non-nil centroid, EffectiveAnchor is exactly
	// Blend(static, centroid, 0.5, 0.5).
	static := []float32{1, 2, 3}
	centroid := []float32{4, -1, 2}
	ea := EffectiveAnchor(static, centroid)
	bl := Blend(static, centroid, 0.5, 0.5)
	if len(ea) != len(bl) {
		t.Fatalf("lengths differ: EffectiveAnchor %d vs Blend %d", len(ea), len(bl))
	}
	for i := range bl {
		if !almostEqual(ea[i], bl[i], 1e-4) {
			t.Errorf("dim %d: EffectiveAnchor = %v, Blend = %v", i, ea[i], bl[i])
		}
	}
}
