package taskstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSchemaV8TDDMigratesForwardWithoutDataLoss(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.Create(
		"P4 task", "preserve TDD seam across P5 migration",
		[]string{"tdd survives"},
		[]TaskStepInput{{ID: "fix", Title: "Fix", TDDTestCommand: "go test ./internal/taskstate"}},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	task.SchemaVersion = tddSchemaVersion
	data, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, task.ID+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	reopened, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := reopened.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SchemaVersion != SchemaVersion {
		t.Fatalf("schema version = %d, want %d", loaded.SchemaVersion, SchemaVersion)
	}
	if len(loaded.Steps) != 1 || loaded.Steps[0].TDD == nil || loaded.Steps[0].TDD.TestCommand != "go test ./internal/taskstate" {
		t.Fatalf("v8 TDD seam lost during migration: %#v", loaded.Steps)
	}
	if loaded.Steps[0].ExecutionContract != nil {
		t.Fatalf("v8 task acquired execution contract during migration: %#v", loaded.Steps[0].ExecutionContract)
	}
}

func TestSchemaV9PassedReviewRemainsReadableWithoutP6Provenance(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.Create(
		"P5 task", "preserve an already-passed v9 final review",
		[]string{"review survives"},
		[]TaskStepInput{{ID: "verify", Title: "Verify"}},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Checkpoint(task.ID, "verify", StepCompleted, "verified"); err != nil {
		t.Fatal(err)
	}
	intent, policy := passingBoundAxes("verify", nil)
	task, err = store.FinalReview(task.ID, FinalReviewInput{
		Status: FinalReviewPass, Summary: "passed before P6",
		VerifiedFacts:    []string{"legacy pass remains readable"},
		VerifiedEvidence: []ReviewEvidenceBinding{reviewBinding("legacy pass remains readable", "verify", nil, nil)},
		IntentSpec:       intent, EngineeringPolicy: policy,
	})
	if err != nil {
		t.Fatal(err)
	}

	task.SchemaVersion = executionSchemaVersion
	task.FinalReview.Provenance = nil
	task.FinalReview.IntentSpec.EvidenceBindings = nil
	task.FinalReview.EngineeringPolicy.EvidenceBindings = nil
	data, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, task.ID+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	reopened, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := reopened.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SchemaVersion != SchemaVersion {
		t.Fatalf("schema version = %d, want %d", loaded.SchemaVersion, SchemaVersion)
	}
	if loaded.FinalReview == nil || loaded.FinalReview.Status != FinalReviewPass {
		t.Fatalf("v9 passed final review was not preserved: %#v", loaded.FinalReview)
	}
	if loaded.FinalReview.Provenance != nil {
		t.Fatalf("legacy v9 review should not acquire fabricated provenance: %#v", loaded.FinalReview.Provenance)
	}
}
