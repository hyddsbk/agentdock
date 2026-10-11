package taskstate

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestTwoAxisOutcomeReviewGate(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "tasks"))
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.Create("Review", "deliver outcome", []string{"implemented"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	axis := func(status string, evidence, findings []string) *OutcomeAxis {
		return &OutcomeAxis{Status: status, Evidence: evidence, Findings: findings}
	}
	pass := func() FinalReviewInput {
		return FinalReviewInput{
			Status: FinalReviewPass, Summary: "verified",
			VerifiedFacts:     []string{"test passed"},
			IntentSpec:        axis(FinalReviewPass, []string{"acceptance check passed"}, nil),
			EngineeringPolicy: axis(FinalReviewPass, []string{"security check passed"}, nil),
		}
	}
	cases := []struct {
		name   string
		modify func(*FinalReviewInput)
		want   string
	}{
		{"missing intent", func(r *FinalReviewInput) { r.IntentSpec = nil }, "intent_spec"},
		{"missing engineering", func(r *FinalReviewInput) { r.EngineeringPolicy = nil }, "engineering_policy"},
		{"unsupported intent pass", func(r *FinalReviewInput) { r.IntentSpec.Evidence = nil }, "requires evidence"},
		{"failed engineering overrides pass", func(r *FinalReviewInput) {
			r.EngineeringPolicy = axis(FinalReviewFailed, nil, []string{"policy violation"})
		}, "overall pass requires"},
		{"failed intent overrides pass", func(r *FinalReviewInput) {
			r.IntentSpec = axis(FinalReviewFailed, nil, []string{"missing requirement"})
		}, "overall pass requires"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := pass()
			tc.modify(&r)
			_, err := store.FinalReview(task.ID, r)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
	saved, err := store.FinalReview(task.ID, pass())
	if err != nil {
		t.Fatal(err)
	}
	if saved.FinalReview.IntentSpec.Status != FinalReviewPass || saved.FinalReview.EngineeringPolicy.Status != FinalReviewPass {
		t.Fatal("axes not persisted")
	}
	if _, err := store.Complete(task.ID); err != nil {
		t.Fatal(err)
	}
}
