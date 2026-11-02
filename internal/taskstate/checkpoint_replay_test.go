package taskstate

import (
	"strings"
	"testing"
)

func checkpointWithReplayKey(
	store *Store,
	taskID, stepID, status, summary, replayKey string,
	diagnostic *DiagnosticEvidence,
	executionEvidence *ExecutionEvidence,
	evidenceRecords []EvidenceRecordInput,
	unknownUpdates []UnknownUpdate,
) (Task, error) {
	return store.CheckpointWithEvidenceTDDAndSemanticsIdempotent(
		taskID, stepID, status, summary,
		diagnostic, executionEvidence, nil, evidenceRecords, unknownUpdates, replayKey,
	)
}

func TestEvidenceCheckpointReplayIsStateIdempotentWithLogicalKey(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.Create(
		"evidence replay",
		"record one logical evidence checkpoint once",
		[]string{"evidence remains canonical"},
		[]TaskStepInput{{
			ID: "inspect", Title: "Inspect",
			CompletionConditions: []string{"The observation is recorded once."},
		}},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	records := []EvidenceRecordInput{{
		Statement:  "The target behavior was observed once.",
		FactStatus: EvidenceFactObserved,
		SourceRefs: []string{"test://checkpoint-replay/source"},
		Coverage:   EvidenceCoverageComplete,
	}}
	const key = "inspect-observation-1"

	first, err := checkpointWithReplayKey(
		store, task.ID, "inspect", StepInProgress, "observation recorded", key,
		nil, nil, records, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Steps[0].EvidenceRecords) != 1 {
		t.Fatalf("first evidence count = %d, want 1", len(first.Steps[0].EvidenceRecords))
	}
	beforeID := first.Steps[0].EvidenceRecords[0].ID
	beforeUpdated := first.UpdatedAt
	beforeEvents := len(first.Events)

	reloaded, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := checkpointWithReplayKey(
		reloaded, task.ID, "inspect", StepInProgress, "observation recorded", key,
		nil, nil, records, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Steps[0].EvidenceRecords) != 1 {
		t.Fatalf("replay evidence count = %d, want 1", len(second.Steps[0].EvidenceRecords))
	}
	if got := second.Steps[0].EvidenceRecords[0].ID; got != beforeID {
		t.Fatalf("replay evidence id = %q, want %q", got, beforeID)
	}
	if len(second.Events) != beforeEvents {
		t.Fatalf("replay appended task events: before=%d after=%d", beforeEvents, len(second.Events))
	}
	if !second.UpdatedAt.Equal(beforeUpdated) {
		t.Fatalf("replay changed task UpdatedAt: before=%s after=%s", beforeUpdated, second.UpdatedAt)
	}
}

func TestEvidenceCheckpointReplayRejectsConflictingPayloadForLogicalKey(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.Create(
		"evidence conflict",
		"reject conflicting reuse of one logical checkpoint key",
		[]string{"conflict rejected"},
		[]TaskStepInput{{
			ID: "inspect", Title: "Inspect",
			CompletionConditions: []string{"The observation key cannot be rebound."},
		}},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	const key = "inspect-observation-1"
	first := []EvidenceRecordInput{{
		Statement:  "Observed state A.",
		FactStatus: EvidenceFactObserved,
		SourceRefs: []string{"test://checkpoint-replay/a"},
		Coverage:   EvidenceCoverageComplete,
	}}
	if _, err := checkpointWithReplayKey(
		store, task.ID, "inspect", StepInProgress, "observation recorded", key,
		nil, nil, first, nil,
	); err != nil {
		t.Fatal(err)
	}

	conflicting := []EvidenceRecordInput{{
		Statement:  "Observed state B.",
		FactStatus: EvidenceFactObserved,
		SourceRefs: []string{"test://checkpoint-replay/b"},
		Coverage:   EvidenceCoverageComplete,
	}}
	_, err = checkpointWithReplayKey(
		store, task.ID, "inspect", StepInProgress, "observation recorded", key,
		nil, nil, conflicting, nil,
	)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "idempot") {
		t.Fatalf("conflicting replay error = %v, want idempotency conflict", err)
	}
}

func TestDiagnosticCheckpointReplayIsStateIdempotentWithLogicalKey(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.Create(
		"diagnostic replay",
		"keep one logical diagnostic observation canonical",
		[]string{"diagnostic replay is a no-op"},
		diagnosticFlowSteps(),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	diagnostic := &DiagnosticEvidence{
		Command:      "go test ./pkg -run TestBug",
		Observed:     DiagnosticObservedRed,
		Symptom:      "TestBug fails with mismatch",
		EvidenceRefs: []string{"test://checkpoint-replay/diagnostic"},
	}
	const key = "diagnostic-red-1"
	first, err := checkpointWithReplayKey(
		store, task.ID, "red-loop", StepInProgress, "red observed", key,
		diagnostic, nil, nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	beforeUpdated := first.UpdatedAt
	beforeEvents := len(first.Events)

	second, err := checkpointWithReplayKey(
		store, task.ID, "red-loop", StepInProgress, "red observed", key,
		diagnostic, nil, nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Events) != beforeEvents {
		t.Fatalf("diagnostic replay appended task events: before=%d after=%d", beforeEvents, len(second.Events))
	}
	if !second.UpdatedAt.Equal(beforeUpdated) {
		t.Fatalf("diagnostic replay changed task UpdatedAt: before=%s after=%s", beforeUpdated, second.UpdatedAt)
	}
}
