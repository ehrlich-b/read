package embedding

import "testing"

func TestAssignSingleStrongMatch(t *testing.T) {
	// post and anchor are identical, cosine = 1.0, far above
	// ThresholdAssign (0.40). Exactly one assignment, not swallowed.
	post := []float32{1, 0, 0}
	anchors := [][]float32{{1, 0, 0}}
	got, swallowed := Assign(post, anchors, false)
	if swallowed {
		t.Error("want swallowed=false, got true")
	}
	if len(got) != 1 {
		t.Fatalf("want exactly 1 assignment, got %v", got)
	}
	if got[0].AnchorIndex != 0 {
		t.Errorf("AnchorIndex = %d, want 0", got[0].AnchorIndex)
	}
	if got[0].Similarity != 1 {
		t.Errorf("Similarity = %v, want exactly 1", got[0].Similarity)
	}
}

func TestAssignMaxAssignments(t *testing.T) {
	// Four anchors, all with cosine above ThresholdAssign (0.40). MaxAssignments
	// is 2, so exactly the two highest are returned.
	//
	//   post = [1, 0];  sim(anchor) = 1 / sqrt(1 + y^2):
	//     [1, 0  ] -> 1           (index 0)
	//     [1, 1  ] -> 1/sqrt(2)  ~ 0.7071  (index 1)
	//     [1, 1.5] -> 1/sqrt(3.25) ~ 0.5547  (index 2)
	//     [1, 2  ] -> 1/sqrt(5)  ~ 0.4472  (index 3)
	// All distinct, so the sort order is deterministic.
	post := []float32{1, 0}
	anchors := [][]float32{
		{1, 0},
		{1, 1},
		{1, 1.5},
		{1, 2},
	}
	got, swallowed := Assign(post, anchors, false)
	if swallowed {
		t.Error("want swallowed=false, got true")
	}
	if len(got) != 2 {
		t.Fatalf("want exactly %d assignments (MaxAssignments), got %d: %v", MaxAssignments, len(got), got)
	}
	if got[0].AnchorIndex != 0 {
		t.Errorf("assignments[0].AnchorIndex = %d, want 0 (highest)", got[0].AnchorIndex)
	}
	if got[1].AnchorIndex != 1 {
		t.Errorf("assignments[1].AnchorIndex = %d, want 1 (second highest)", got[1].AnchorIndex)
	}
}

func TestAssignBelowSwallow(t *testing.T) {
	// cosine = 0 for both anchors, below ThresholdSwallow (0.25): everything
	// is swallowed and assignments is nil.
	post := []float32{1, 0}
	anchors := [][]float32{{0, 1}, {0, 1}}
	got, swallowed := Assign(post, anchors, false)
	if !swallowed {
		t.Error("want swallowed=true, got false")
	}
	if got != nil || len(got) != 0 {
		t.Errorf("want nil assignments, got %v", got)
	}
}

func TestAssignFrontierBand(t *testing.T) {
	// Frontier band: the top similarity must sit between ThresholdSwallow
	// (0.25) and ThresholdAssign (0.40). Chosen post = [1, 0] and anchor =
	// [1, 2.5], whose cosine is 1/sqrt(1 + 2.5^2) = 1/sqrt(7.25) ~ 0.3714:
	// comfortably inside (0.25, 0.40). Result: nil assignments AND
	// swallowed=false.
	post := []float32{1, 0}
	anchors := [][]float32{{1, 2.5}}
	got, swallowed := Assign(post, anchors, false)
	if swallowed {
		t.Error("want swallowed=false (frontier), got true")
	}
	if got != nil || len(got) != 0 {
		t.Errorf("want nil assignments (frontier), got %v", got)
	}
}

func TestAssignProBoostAppliedBeforeThreshold(t *testing.T) {
	// The pro boost is applied BEFORE thresholding. Raw cosine of post=[1,0]
	// vs anchor=[1,2.5] is 1/sqrt(7.25) ~ 0.3714, which is below
	// ThresholdAssign (0.40) but within ProximityBoost (0.05) of it. So:
	//   isPro=false -> not assigned (frontier);
	//   isPro=true  -> 0.3714 + 0.05 = 0.4214 >= 0.40 -> assigned.
	// This is the single most behaviour-defining branch in assign.go.
	post := []float32{1, 0}
	anchors := [][]float32{{1, 2.5}}

	got, swallowed := Assign(post, anchors, false)
	if len(got) != 0 {
		t.Errorf("isPro=false: want no assignments (raw 0.3714 < 0.40), got %v", got)
	}
	if swallowed {
		t.Error("isPro=false: want swallowed=false (frontier), got true")
	}

	got, swallowed = Assign(post, anchors, true)
	if swallowed {
		t.Error("isPro=true: want swallowed=false, got true")
	}
	if len(got) != 1 {
		t.Fatalf("isPro=true: want 1 assignment (boosted 0.4214 >= 0.40), got %v", got)
	}
	if got[0].AnchorIndex != 0 {
		t.Errorf("isPro=true: AnchorIndex = %d, want 0", got[0].AnchorIndex)
	}
}

func TestAssignEmptyAnchors(t *testing.T) {
	// PIN — EMPTY-ANCHOR CASE. With anchorVecs empty, scores is empty and the
	// swallow check (guarded by len(scores) > 0) is SKIPPED, so Assign returns
	// (nil, false) — reported as "frontier", not "swallowed". A post with
	// nowhere to go is therefore treated as a near-miss, not as spam.
	got, swallowed := Assign([]float32{1, 0}, nil, false)
	if swallowed {
		t.Error("want swallowed=false, got true (swallow check skipped on empty anchors)")
	}
	if got != nil || len(got) != 0 {
		t.Errorf("want nil assignments, got %v", got)
	}
}
