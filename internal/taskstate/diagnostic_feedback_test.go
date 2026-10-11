package taskstate

import (
	"strings"
	"testing"
)

func diagnosticFlowSteps() []TaskStepInput {
	return []TaskStepInput{
		{
			ID: "red-loop", Title: "Establish red feedback loop",
			CompletionConditions: []string{"The bug is deterministically red."},
			Quality:              quality(StepKindDiagnosticFeedbackLoop, "A fast agent-runnable check fails on the target bug.", "", nil),
		},
		{
			ID: "repro", Title: "Minimize reproduction", DependsOn: []string{"red-loop"},
			CompletionConditions: []string{"The smallest useful reproduction remains red."},
			Quality:              quality(StepKindDiagnosticReproduction, "A minimized reproducer isolates the symptom.", "", map[string]string{"red-loop": "The original failure must be observable before minimization."}),
		},
		{
			ID: "hypothesis", Title: "State falsifiable hypothesis", DependsOn: []string{"repro"},
			CompletionConditions: []string{"A causal claim and falsifier are explicit."},
			Quality:              quality(StepKindDiagnosticHypothesis, "The proposed cause can be disproved by a concrete observation.", "", map[string]string{"repro": "Hypotheses are formed only after the minimal reproduction is established."}),
		},
		{
			ID: "validate", Title: "Validate one variable", DependsOn: []string{"hypothesis"},
			CompletionConditions: []string{"Exactly one named variable is changed and measured."},
			Quality:              quality(StepKindDiagnosticValidation, "A single-variable experiment supports or refutes the hypothesis.", "", map[string]string{"hypothesis": "The experiment tests the stated falsifiable hypothesis."}),
		},
		{
			ID: "regression", Title: "Run regression", DependsOn: []string{"validate"},
			CompletionConditions: []string{"The minimal reproduction is green after the supported change."},
			Quality:              quality(StepKindDiagnosticRegression, "The exact minimized reproducer is rerun and passes.", "", map[string]string{"validate": "Regression follows the diagnostic experiment."}),
		},
	}
}

func TestDiagnosticFeedbackLoopRequiresCompleteOrderedFlow(t *testing.T) {
	store, _ := New(t.TempDir())
	if _, err := store.Create("diagnose", "find cause", []string{"fixed"}, diagnosticFlowSteps(), nil); err != nil {
		t.Fatalf("valid diagnostic flow rejected: %v", err)
	}

	partial := diagnosticFlowSteps()[:4]
	if _, err := store.Create("partial", "reject partial diagnostic flow", []string{"rejected"}, partial, nil); err == nil || !strings.Contains(err.Error(), "requires exactly one feedback_loop") {
		t.Fatalf("partial diagnostic flow err=%v", err)
	}

	ambiguous := diagnosticFlowSteps()
	secondHypothesis := TaskStepInput{
		ID: "hypothesis-b", Title: "Second hypothesis", DependsOn: []string{"repro"},
		CompletionConditions: []string{"A second falsifiable causal claim is explicit."},
		Quality:              quality(StepKindDiagnosticHypothesis, "A second cause can be falsified.", "", map[string]string{"repro": "It uses the same minimal reproduction."}),
	}
	ambiguous = append(ambiguous[:3], append([]TaskStepInput{secondHypothesis}, ambiguous[3:]...)...)
	ambiguous[4].DependsOn = []string{"hypothesis", "hypothesis-b"}
	ambiguous[4].Quality.DependencyReasons = map[string]string{
		"hypothesis":   "Test the first cause.",
		"hypothesis-b": "Also test the second cause.",
	}
	ambiguous[5].DependsOn = []string{"validate"}
	ambiguous[5].Quality.DependencyReasons = map[string]string{"validate": "Regression follows validation."}
	if _, err := store.Create("ambiguous", "reject multi-hypothesis experiment", []string{"rejected"}, ambiguous, nil); err == nil || !strings.Contains(err.Error(), "exactly one falsifiable hypothesis") {
		t.Fatalf("ambiguous diagnostic flow err=%v", err)
	}
}

func TestDiagnosticCheckpointGateEnforcesEvidenceSequence(t *testing.T) {
	store, _ := New(t.TempDir())
	task, err := store.Create("diagnose", "prove a fix", []string{"regression green"}, diagnosticFlowSteps(), nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.Checkpoint(task.ID, "red-loop", StepCompleted, "claimed red"); err == nil || !strings.Contains(err.Error(), "structured diagnostic evidence") {
		t.Fatalf("red loop completed without evidence: %v", err)
	}
	if _, err := store.Checkpoint(task.ID, "red-loop", StepCompleted, "red reproduced", &DiagnosticEvidence{
		Command: "go test ./pkg -run TestBug", Observed: DiagnosticObservedRed, Symptom: "TestBug fails with mismatch", EvidenceRefs: []string{"artifact://diag/red.txt"},
	}); err != nil {
		t.Fatalf("red loop checkpoint: %v", err)
	}

	if _, err := store.Checkpoint(task.ID, "repro", StepCompleted, "minimal repro red", &DiagnosticEvidence{
		Command: "go test ./pkg -run TestBug/minimal", Observed: DiagnosticObservedRed, ReproRef: "artifact://diag/minimal-repro",
	}); err != nil {
		t.Fatalf("repro checkpoint: %v", err)
	}
	if _, err := store.Checkpoint(task.ID, "hypothesis", StepCompleted, "hypothesis is falsifiable", &DiagnosticEvidence{
		Hypothesis: "The parser retains stale state.", Falsifier: "A fresh parser instance still fails identically.",
	}); err != nil {
		t.Fatalf("hypothesis checkpoint: %v", err)
	}
	if _, err := store.Checkpoint(task.ID, "validate", StepCompleted, "single variable supports cause", &DiagnosticEvidence{
		Variable: "parser state reset", Method: "Reset only parser state and rerun the minimal reproduction.", Outcome: DiagnosticOutcomeSupported,
	}); err != nil {
		t.Fatalf("validation checkpoint: %v", err)
	}

	if _, err := store.Checkpoint(task.ID, "regression", StepCompleted, "wrong check", &DiagnosticEvidence{
		Command: "go test ./...", Observed: DiagnosticObservedGreen,
	}); err == nil || !strings.Contains(err.Error(), "must rerun the minimal reproduction command") {
		t.Fatalf("regression accepted a different command: %v", err)
	}
	updated, err := store.Checkpoint(task.ID, "regression", StepCompleted, "minimal repro green", &DiagnosticEvidence{
		Command: "go test ./pkg -run TestBug/minimal", Observed: DiagnosticObservedGreen, EvidenceRefs: []string{"artifact://diag/regression.txt"},
	})
	if err != nil {
		t.Fatalf("regression checkpoint: %v", err)
	}
	if updated.Steps[4].Status != StepCompleted || updated.Steps[4].Diagnostic == nil || updated.Steps[4].Diagnostic.Observed != DiagnosticObservedGreen {
		t.Fatalf("diagnostic evidence not persisted: %#v", updated.Steps[4])
	}
}

func TestDiagnosticRegressionRejectsOnlyRefutedHypotheses(t *testing.T) {
	store, _ := New(t.TempDir())
	task, err := store.Create("diagnose", "do not regress after refutation", []string{"safe"}, diagnosticFlowSteps(), nil)
	if err != nil {
		t.Fatal(err)
	}
	checkpoints := []struct {
		id       string
		summary  string
		evidence *DiagnosticEvidence
	}{
		{"red-loop", "red", &DiagnosticEvidence{Command: "repro", Observed: DiagnosticObservedRed, Symptom: "fails"}},
		{"repro", "minimal", &DiagnosticEvidence{Command: "minimal", Observed: DiagnosticObservedRed, ReproRef: "artifact://minimal"}},
		{"hypothesis", "hypothesis", &DiagnosticEvidence{Hypothesis: "cause A", Falsifier: "observation B"}},
		{"validate", "refuted", &DiagnosticEvidence{Variable: "A", Method: "change only A", Outcome: DiagnosticOutcomeRefuted}},
	}
	for _, checkpoint := range checkpoints {
		if _, err := store.Checkpoint(task.ID, checkpoint.id, StepCompleted, checkpoint.summary, checkpoint.evidence); err != nil {
			t.Fatalf("%s: %v", checkpoint.id, err)
		}
	}
	if _, err := store.Checkpoint(task.ID, "regression", StepCompleted, "green", &DiagnosticEvidence{Command: "minimal", Observed: DiagnosticObservedGreen}); err == nil || !strings.Contains(err.Error(), "at least one supported single-variable validation") {
		t.Fatalf("regression accepted only refuted validation: %v", err)
	}
}
