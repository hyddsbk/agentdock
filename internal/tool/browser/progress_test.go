package browser

import "testing"

func testFingerprint(url, title, text string, scrollY int) PageFingerprint {
	return fingerprintFromDOM(domSnapshot{
		URL:     url,
		Title:   title,
		Text:    text,
		ScrollY: scrollY,
		InteractiveElements: []InteractiveElement{
			{Tag: "button", Text: "Save", Selector: "#save"},
		},
	})
}

func TestPageFingerprintAndStateDelta(t *testing.T) {
	before := testFingerprint("https://example.test/a", "A", "alpha", 0)
	same := testFingerprint("https://example.test/a", "A", "alpha", 0)
	if before.Digest == "" || before.Digest != same.Digest {
		t.Fatalf("stable fingerprint mismatch: before=%#v same=%#v", before, same)
	}
	unchanged := buildStateDelta(before, same, true)
	if unchanged.Changed || unchanged.TextChanged || unchanged.ScrollChanged {
		t.Fatalf("unchanged delta = %#v", unchanged)
	}

	after := testFingerprint("https://example.test/a", "A", "beta", 240)
	changed := buildStateDelta(before, after, true)
	if !changed.Changed || !changed.TextChanged || !changed.ScrollChanged || changed.URLChanged {
		t.Fatalf("changed delta = %#v", changed)
	}
}

func TestObserveFingerprintUsesBaselineThenDelta(t *testing.T) {
	sess := &session{}
	first := testFingerprint("https://example.test", "Example", "one", 0)
	baseline := sess.observeFingerprint(first)
	if baseline.HasPrevious || baseline.Changed || baseline.CurrentDigest != first.Digest {
		t.Fatalf("baseline delta = %#v", baseline)
	}
	second := testFingerprint("https://example.test", "Example", "two", 0)
	delta := sess.observeFingerprint(second)
	if !delta.HasPrevious || !delta.Changed || !delta.TextChanged {
		t.Fatalf("second delta = %#v", delta)
	}
}

func TestActionLoopDetectorTriggersReplanOnThirdRepeatedNoProgress(t *testing.T) {
	sess := &session{}
	fingerprint := testFingerprint("https://example.test", "Example", "steady", 0)
	actions := []Action{{Kind: "click", Click: &ClickAction{Selector: "#save"}}}

	for attempt := 1; attempt <= stallReplanThreshold; attempt++ {
		delta, result := sess.finalizeActionProgress(actions, fingerprint, true, fingerprint)
		if delta.Changed {
			t.Fatalf("attempt %d unexpectedly changed state: %#v", attempt, delta)
		}
		if result.StallCount != attempt {
			t.Fatalf("attempt %d stall_count=%d result=%#v", attempt, result.StallCount, result)
		}
		if attempt < stallReplanThreshold {
			if result.LoopDetected || result.ReplanRequired || result.Status != "no_progress" {
				t.Fatalf("attempt %d escalated too early: %#v", attempt, result)
			}
			continue
		}
		if !result.LoopDetected || !result.ReplanRequired || result.Status != "stalled" || result.Replan == nil {
			t.Fatalf("third no-progress action did not replan: %#v", result)
		}
		if result.Replan.SuggestedStrategy != "refresh_snapshot_and_choose_different_action" {
			t.Fatalf("unexpected replan strategy: %#v", result.Replan)
		}
	}
}

func TestActionLoopDetectorResetsOnProgressAndIgnoresWaits(t *testing.T) {
	sess := &session{}
	steady := testFingerprint("https://example.test", "Example", "steady", 0)
	changed := testFingerprint("https://example.test/next", "Next", "changed", 0)
	click := []Action{{Kind: "click", Click: &ClickAction{Selector: "#next"}}}

	_, first := sess.finalizeActionProgress(click, steady, true, steady)
	if first.StallCount != 1 {
		t.Fatalf("first no-progress result = %#v", first)
	}
	_, progressed := sess.finalizeActionProgress(click, steady, true, changed)
	if !progressed.StateChanged || progressed.StallCount != 0 || progressed.ReplanRequired {
		t.Fatalf("progress did not reset stall state: %#v", progressed)
	}
	_, afterReset := sess.finalizeActionProgress(click, changed, true, changed)
	if afterReset.StallCount != 1 {
		t.Fatalf("stall count did not restart after progress: %#v", afterReset)
	}

	wait := []Action{{Kind: "wait", Wait: &WaitAction{}}}
	for attempt := 0; attempt < 5; attempt++ {
		_, result := sess.finalizeActionProgress(wait, changed, true, changed)
		if result.StallCount != 0 || result.LoopDetected || result.ReplanRequired {
			t.Fatalf("passive wait counted as a loop on attempt %d: %#v", attempt+1, result)
		}
	}
	_, resumed := sess.finalizeActionProgress(click, changed, true, changed)
	if resumed.StallCount != 2 {
		t.Fatalf("passive waits should preserve the active-action stall streak: %#v", resumed)
	}
}
