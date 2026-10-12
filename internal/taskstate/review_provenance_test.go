package taskstate

import (
	"strings"
	"testing"
	"time"
)

func reviewBinding(claim, stepID string, requirementRefs, evidenceRefs []string) ReviewEvidenceBinding {
	return ReviewEvidenceBinding{
		Claim: claim, StepID: stepID,
		RequirementRefs: requirementRefs, EvidenceRefs: evidenceRefs,
	}
}

func passingBoundAxes(stepID string, evidenceRefs []string) (*OutcomeAxis, *OutcomeAxis) {
	intentClaim := "intent requirement is satisfied"
	policyClaim := "engineering checks are satisfied"
	return &OutcomeAxis{
			Status:           FinalReviewPass,
			Evidence:         []string{intentClaim},
			EvidenceBindings: []ReviewEvidenceBinding{reviewBinding(intentClaim, stepID, nil, evidenceRefs)},
		}, &OutcomeAxis{
			Status:           FinalReviewPass,
			Evidence:         []string{policyClaim},
			EvidenceBindings: []ReviewEvidenceBinding{reviewBinding(policyClaim, stepID, nil, evidenceRefs)},
		}
}

func TestPassingFinalReviewRequiresEvidenceBindings(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.Create(
		"Ordinary review", "bind outcome claims",
		[]string{"work is really complete"},
		[]TaskStepInput{{ID: "work", Title: "Do work"}},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Checkpoint(task.ID, "work", StepCompleted, "work completed"); err != nil {
		t.Fatal(err)
	}
	intent, policy := passingOutcomeAxes()
	if _, err := store.FinalReview(task.ID, FinalReviewInput{
		Status: FinalReviewPass, Summary: "claimed pass",
		VerifiedFacts: []string{"work really happened"},
		IntentSpec:    intent, EngineeringPolicy: policy,
	}); err == nil || !strings.Contains(err.Error(), "evidence binding") {
		t.Fatalf("text-only pass should be rejected, got %v", err)
	}

	verifiedClaim := "work really happened"
	intent, policy = passingBoundAxes("work", nil)
	reviewed, err := store.FinalReview(task.ID, FinalReviewInput{
		Status: FinalReviewPass, Summary: "bound pass",
		VerifiedFacts:    []string{verifiedClaim},
		VerifiedEvidence: []ReviewEvidenceBinding{reviewBinding(verifiedClaim, "work", nil, nil)},
		IntentSpec:       intent, EngineeringPolicy: policy,
	})
	if err != nil {
		t.Fatalf("step-bound ordinary review rejected: %v", err)
	}
	if reviewed.FinalReview == nil || reviewed.FinalReview.Provenance == nil {
		t.Fatalf("final review provenance missing: %#v", reviewed.FinalReview)
	}
	if got := reviewed.FinalReview.Provenance.StepIDs; len(got) != 1 || got[0] != "work" {
		t.Fatalf("provenance step ids = %#v", got)
	}
}

func TestFinalReviewEvidenceBindingRejectsUnownedAndRequiresRequirementClosure(t *testing.T) {
	store, _ := New(t.TempDir())
	task, err := store.CreateWithTraceability(
		"Traceable review", "bind R1 to implementation evidence", "", "",
		[]string{"R1 is implemented and verified"},
		[]TaskStepInput{
			{ID: "build", Title: "Build", RequirementRefs: []string{"spec://p6#R1"}},
			{ID: "verify", Title: "Verify"},
		},
		nil,
		[]string{"spec://p6#R1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CheckpointWithEvidence(task.ID, "build", StepCompleted, "built", nil, &ExecutionEvidence{
		EvidenceRefs: []string{"commit://agentdock/abc123", "artifact://candidate/sha256-deadbeef"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CheckpointWithEvidence(task.ID, "verify", StepCompleted, "verified", nil, &ExecutionEvidence{
		EvidenceRefs: []string{"test://taskstate/P6"},
	}); err != nil {
		t.Fatal(err)
	}

	claim := "R1 is complete"
	intent, policy := passingBoundAxes("verify", []string{"test://taskstate/P6"})
	_, err = store.FinalReview(task.ID, FinalReviewInput{
		Status: FinalReviewPass, Summary: "wrong owner",
		VerifiedFacts:    []string{claim},
		VerifiedEvidence: []ReviewEvidenceBinding{reviewBinding(claim, "verify", nil, []string{"commit://agentdock/abc123"})},
		IntentSpec:       intent, EngineeringPolicy: policy,
	})
	if err == nil || !strings.Contains(err.Error(), "not owned by step verify") {
		t.Fatalf("cross-step evidence should fail closed, got %v", err)
	}
	loaded, err := store.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.FinalReview != nil {
		t.Fatalf("failed final review mutated task: %#v", loaded.FinalReview)
	}

	_, err = store.FinalReview(task.ID, FinalReviewInput{
		Status: FinalReviewPass, Summary: "missing requirement closure",
		VerifiedFacts:    []string{claim},
		VerifiedEvidence: []ReviewEvidenceBinding{reviewBinding(claim, "build", nil, []string{"commit://agentdock/abc123"})},
		IntentSpec: &OutcomeAxis{
			Status:           FinalReviewPass,
			Evidence:         []string{"intent covered"},
			EvidenceBindings: []ReviewEvidenceBinding{reviewBinding("intent covered", "build", nil, []string{"commit://agentdock/abc123"})},
		},
		EngineeringPolicy: &OutcomeAxis{
			Status:           FinalReviewPass,
			Evidence:         []string{"tests passed"},
			EvidenceBindings: []ReviewEvidenceBinding{reviewBinding("tests passed", "verify", nil, []string{"test://taskstate/P6"})},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "requirement") {
		t.Fatalf("review without requirement closure should fail, got %v", err)
	}

	reviewed, err := store.FinalReview(task.ID, FinalReviewInput{
		Status: FinalReviewPass, Summary: "fully bound",
		VerifiedFacts:    []string{claim},
		VerifiedEvidence: []ReviewEvidenceBinding{reviewBinding(claim, "build", []string{"spec://p6#R1"}, []string{"commit://agentdock/abc123", "artifact://candidate/sha256-deadbeef"})},
		IntentSpec: &OutcomeAxis{
			Status:           FinalReviewPass,
			Evidence:         []string{"intent covered"},
			EvidenceBindings: []ReviewEvidenceBinding{reviewBinding("intent covered", "build", []string{"spec://p6#R1"}, []string{"commit://agentdock/abc123"})},
		},
		EngineeringPolicy: &OutcomeAxis{
			Status:           FinalReviewPass,
			Evidence:         []string{"tests passed"},
			EvidenceBindings: []ReviewEvidenceBinding{reviewBinding("tests passed", "verify", nil, []string{"test://taskstate/P6"})},
		},
	})
	if err != nil {
		t.Fatalf("fully bound final review rejected: %v", err)
	}
	provenance := reviewed.FinalReview.Provenance
	if provenance == nil || len(provenance.Bindings) != 3 {
		t.Fatalf("unexpected provenance: %#v", provenance)
	}
	if len(provenance.RequirementRefs) != 1 || provenance.RequirementRefs[0] != "spec://p6#R1" {
		t.Fatalf("requirement closure missing: %#v", provenance.RequirementRefs)
	}
	if len(provenance.EvidenceRefs) != 3 {
		t.Fatalf("evidence closure = %#v", provenance.EvidenceRefs)
	}
}

func TestReviewEvidenceCatalogIncludesExecutionTDDAndDiagnosticEvidence(t *testing.T) {
	step := TaskStep{
		ID: "verify", Status: StepCompleted,
		ExecutionEvidence: &ExecutionEvidence{
			EvidenceRefs: []string{"commit://agentdock/abc123"},
			Attestations: []*ExecutionRun{{EvidenceRef: "exec://session-generic"}},
		},
		TDD: &TDDSeam{
			Red:        &TDDRun{EvidenceRef: "exec://session-red"},
			Green:      &TDDRun{EvidenceRef: "exec://session-green"},
			Regression: &TDDRun{EvidenceRef: "exec://session-regression"},
		},
		Diagnostic: &DiagnosticEvidence{
			ReproRef:     "artifact://minimal-repro",
			EvidenceRefs: []string{"artifact://diagnostic-log"},
		},
	}
	refs := stepOutcomeEvidenceRefs(step)
	for _, want := range []string{
		"commit://agentdock/abc123",
		"exec://session-generic",
		"exec://session-red",
		"exec://session-green",
		"exec://session-regression",
		"artifact://minimal-repro",
		"artifact://diagnostic-log",
		"diagnostic://verify",
	} {
		if !containsString(refs, want) {
			t.Fatalf("evidence catalog %#v missing %s", refs, want)
		}
	}
}

func TestFinalReviewBindsTDDAndGenericExecutorReceipts(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.Create(
		"Receipt-backed review", "prove P4 and P5 evidence reaches final review",
		[]string{"TDD and generic execution are both evidenced"},
		[]TaskStepInput{
			{ID: "tdd", Title: "TDD verification", TDDTestCommand: "go test ./internal/taskstate"},
			{ID: "exec", Title: "Generic verification", ExecutionContract: &ExecutionContractSpec{
				Checks: []ExecutionCheckSpec{{ID: "test", Command: "go test ./internal/taskstate"}},
			}},
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	tddEvidence := validTDDSeamForDeclaration(task.Steps[0].TDD.DeclaredAt)
	if _, err := store.CheckpointWithTDD(task.ID, "tdd", StepCompleted, "TDD complete", tddEvidence); err != nil {
		t.Fatal(err)
	}

	execDeclared := task.Steps[1].ExecutionContract.DeclaredAt
	execRef := "exec://session-444444444444444444444444"
	execRun := executionRun("test", "go test ./internal/taskstate", execRef, 0, execDeclared.Add(time.Second))
	if _, err := store.CheckpointWithEvidence(task.ID, "exec", StepCompleted, "generic execution complete", nil, &ExecutionEvidence{
		EvidenceRefs: []string{execRef},
		Attestations: []*ExecutionRun{execRun},
	}); err != nil {
		t.Fatal(err)
	}

	tddRef := tddEvidence.Regression.EvidenceRef
	verified := "both execution paths were attested"
	reviewed, err := store.FinalReview(task.ID, FinalReviewInput{
		Status: FinalReviewPass, Summary: "receipt-backed pass",
		VerifiedFacts: []string{verified},
		VerifiedEvidence: []ReviewEvidenceBinding{
			reviewBinding(verified, "tdd", nil, []string{tddRef}),
			reviewBinding(verified, "exec", nil, []string{execRef}),
		},
		IntentSpec: &OutcomeAxis{
			Status: FinalReviewPass, Evidence: []string{"TDD regression passed"},
			EvidenceBindings: []ReviewEvidenceBinding{reviewBinding("TDD regression passed", "tdd", nil, []string{tddRef})},
		},
		EngineeringPolicy: &OutcomeAxis{
			Status: FinalReviewPass, Evidence: []string{"generic check passed"},
			EvidenceBindings: []ReviewEvidenceBinding{reviewBinding("generic check passed", "exec", nil, []string{execRef})},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if reviewed.FinalReview == nil || reviewed.FinalReview.Provenance == nil {
		t.Fatal("receipt-backed final review lost provenance")
	}
	if !containsString(reviewed.FinalReview.Provenance.EvidenceRefs, tddRef) || !containsString(reviewed.FinalReview.Provenance.EvidenceRefs, execRef) {
		t.Fatalf("provenance does not bind both executor receipts: %#v", reviewed.FinalReview.Provenance.EvidenceRefs)
	}
}
