package taskstate

import (
	"strings"
	"testing"
)

func TestFinalReviewRequiresBothOutcomeAxes(t *testing.T) {
	store, _ := New(t.TempDir())
	task, err := store.Create("review", "verify two axes", []string{"done"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.FinalReview(task.ID, FinalReviewInput{Status: FinalReviewPass, Summary: "legacy", VerifiedFacts: []string{"fact"}})
	if err == nil || !strings.Contains(err.Error(), "requires both intent_spec and engineering_policy") {
		t.Fatalf("legacy review err=%v", err)
	}
	_, err = store.FinalReview(task.ID, FinalReviewInput{Status: FinalReviewPass, Summary: "mixed", VerifiedFacts: []string{"fact"}, IntentSpec: &OutcomeAxis{Status: FinalReviewPass, Evidence: []string{"intent"}}, EngineeringPolicy: &OutcomeAxis{Status: FinalReviewFailed, Findings: []string{"policy gap"}}})
	if err == nil || !strings.Contains(err.Error(), "requires both outcome axes to pass") {
		t.Fatalf("mixed pass err=%v", err)
	}
}

func TestFinalReviewPersistsBothOutcomeAxes(t *testing.T) {
	store, _ := New(t.TempDir())
	task, err := store.Create("review", "verify two axes", []string{"done"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err = store.FinalReview(task.ID, FinalReviewInput{Status: FinalReviewPass, Summary: "verified", VerifiedFacts: []string{"fact"}, IntentSpec: &OutcomeAxis{Status: FinalReviewPass, Evidence: []string{"intent"}}, EngineeringPolicy: &OutcomeAxis{Status: FinalReviewPass, Evidence: []string{"policy"}}})
	if err != nil {
		t.Fatal(err)
	}
	if task.FinalReview.IntentSpec == nil || task.FinalReview.EngineeringPolicy == nil {
		t.Fatalf("axes not persisted: %#v", task.FinalReview)
	}
}
