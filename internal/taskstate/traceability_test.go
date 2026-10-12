package taskstate

import (
	"strings"
	"testing"
)

func traceableSteps() []TaskStepInput {
	return []TaskStepInput{
		{
			ID: "build", Title: "Build behavior",
			CompletionConditions: []string{"Requirement R1 is implemented."},
			RequirementRefs:      []string{"spec://feature#R1"},
		},
		{
			ID: "verify", Title: "Verify behavior",
			CompletionConditions: []string{"Requirement R2 is verified."},
			RequirementRefs:      []string{"spec://feature#R2"},
		},
	}
}

func TestTraceabilityContractRequiresCompleteRequirementMapping(t *testing.T) {
	store, _ := New(t.TempDir())
	task, err := store.CreateWithTraceability(
		"Traceable change", "implement the settled spec", "", "",
		[]string{"all requirements are evidenced"},
		traceableSteps(), nil,
		[]string{"spec://feature#R1", "spec://feature#R2"},
	)
	if err != nil {
		t.Fatalf("valid traceability contract rejected: %v", err)
	}
	if task.SchemaVersion != SchemaVersion || len(task.RequirementRefs) != 2 {
		t.Fatalf("traceability contract not persisted: %#v", task)
	}

	unmapped := traceableSteps()
	unmapped[1].RequirementRefs = nil
	if _, err := store.CreateWithTraceability(
		"Unmapped", "reject incomplete mapping", "", "",
		[]string{"mapping rejected"}, unmapped, nil,
		[]string{"spec://feature#R1", "spec://feature#R2"},
	); err == nil || !strings.Contains(err.Error(), "not mapped to any step") {
		t.Fatalf("unmapped requirement err=%v", err)
	}

	unknown := traceableSteps()
	unknown[1].RequirementRefs = []string{"spec://feature#R3"}
	if _, err := store.CreateWithTraceability(
		"Unknown", "reject unknown requirement", "", "",
		[]string{"mapping rejected"}, unknown, nil,
		[]string{"spec://feature#R1", "spec://feature#R2"},
	); err == nil || !strings.Contains(err.Error(), "unknown requirement ref") {
		t.Fatalf("unknown requirement err=%v", err)
	}
}

func TestTraceableCheckpointRequiresExecutionEvidenceAndFinalReviewReportsCoverage(t *testing.T) {
	store, _ := New(t.TempDir())
	task, err := store.CreateWithTraceability(
		"Traceable change", "implement the settled spec", "", "",
		[]string{"all requirements are evidenced"},
		traceableSteps(), nil,
		[]string{"spec://feature#R1", "spec://feature#R2"},
	)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.Checkpoint(task.ID, "build", StepCompleted, "claimed complete"); err == nil || !strings.Contains(err.Error(), "execution evidence") {
		t.Fatalf("traceable step completed without evidence: %v", err)
	}
	if _, err := store.BatchCheckpoint(task.ID, []string{"build"}, "", "batch complete"); err == nil || !strings.Contains(err.Error(), "single-step checkpoint") {
		t.Fatalf("traceable step completed through batch checkpoint: %v", err)
	}
	if _, err := store.CheckpointWithEvidence(task.ID, "build", StepCompleted, "R1 implemented", nil, &ExecutionEvidence{
		EvidenceRefs: []string{"commit://agentdock/abc123", "test://taskstate/TestR1"},
	}); err != nil {
		t.Fatalf("R1 checkpoint: %v", err)
	}
	if _, err := store.CheckpointWithEvidence(task.ID, "verify", StepCompleted, "R2 verified", nil, &ExecutionEvidence{
		EvidenceRefs: []string{"test://integration/TestR2"},
	}); err != nil {
		t.Fatalf("R2 checkpoint: %v", err)
	}

	intent := &OutcomeAxis{
		Status: FinalReviewPass, Evidence: []string{"R1 intent covered"},
		EvidenceBindings: []ReviewEvidenceBinding{reviewBinding("R1 intent covered", "build", []string{"spec://feature#R1"}, []string{"commit://agentdock/abc123"})},
	}
	policy := &OutcomeAxis{
		Status: FinalReviewPass, Evidence: []string{"R2 verification passed"},
		EvidenceBindings: []ReviewEvidenceBinding{reviewBinding("R2 verification passed", "verify", []string{"spec://feature#R2"}, []string{"test://integration/TestR2"})},
	}
	reviewed, err := store.FinalReview(task.ID, FinalReviewInput{
		Status: FinalReviewPass, Summary: "spec coverage verified",
		VerifiedFacts: []string{"R1 and R2 have implementation evidence"},
		VerifiedEvidence: []ReviewEvidenceBinding{
			reviewBinding("R1 and R2 have implementation evidence", "build", []string{"spec://feature#R1"}, []string{"commit://agentdock/abc123"}),
			reviewBinding("R1 and R2 have implementation evidence", "verify", []string{"spec://feature#R2"}, []string{"test://integration/TestR2"}),
		},
		IntentSpec: intent, EngineeringPolicy: policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	if reviewed.FinalReview.Traceability == nil ||
		reviewed.FinalReview.Traceability.RequiredCount != 2 ||
		reviewed.FinalReview.Traceability.CoveredCount != 2 ||
		len(reviewed.FinalReview.Traceability.Items) != 2 {
		t.Fatalf("unexpected traceability coverage: %#v", reviewed.FinalReview.Traceability)
	}
	for _, item := range reviewed.FinalReview.Traceability.Items {
		if len(item.StepIDs) == 0 || len(item.EvidenceRefs) == 0 {
			t.Fatalf("coverage item lacks step/evidence linkage: %#v", item)
		}
	}
}

func TestOrdinaryTaskRemainsBackwardCompatibleWithoutTraceability(t *testing.T) {
	store, _ := New(t.TempDir())
	task, err := store.Create("Ordinary", "preserve compatibility", []string{"done"}, []TaskStepInput{{ID: "build", Title: "Build"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Checkpoint(task.ID, "build", StepCompleted, "done"); err != nil {
		t.Fatalf("ordinary checkpoint unexpectedly requires execution evidence: %v", err)
	}
}
