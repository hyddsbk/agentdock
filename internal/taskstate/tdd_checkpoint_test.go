package taskstate

import "testing"

func TestTDDSeamCheckpointRequiresEvidence(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.Create("TDD step", "verify behavior", []string{"regression passes"}, []TaskStepInput{{ID: "fix", Title: "Fix bug", TDDTestCommand: "go test ./internal/taskstate"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Checkpoint(task.ID, "fix", StepCompleted, "unverified"); err == nil {
		t.Fatal("TDD completion bypassed without evidence")
	}
	if _, err = store.BatchCheckpoint(task.ID, []string{"fix"}, "", "unverified"); err == nil {
		t.Fatal("batch checkpoint bypassed TDD")
	}
	invalid := validTDDSeamForDeclaration(task.Steps[0].TDD.DeclaredAt)
	invalid.Red.ExitCode = 0
	if _, err = store.CheckpointWithTDD(task.ID, "fix", StepCompleted, "invalid red", invalid); err == nil {
		t.Fatal("accepted passing red")
	}
	current, err := store.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Steps[0].Status != StepPending {
		t.Fatalf("invalid checkpoint mutated step: %s", current.Steps[0].Status)
	}
	task, err = store.CheckpointWithTDD(task.ID, "fix", StepCompleted, "validated red green regression", validTDDSeamForDeclaration(task.Steps[0].TDD.DeclaredAt))
	if err != nil {
		t.Fatal(err)
	}
	if task.Steps[0].TDD == nil || task.Steps[0].TDD.Regression == nil {
		t.Fatal("receipt not persisted")
	}
	task, err = store.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Steps[0].TDD.Red.ExitCode != 1 {
		t.Fatal("red receipt did not survive reload")
	}
}

func TestLegacyCheckpointDoesNotRequireTDD(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.Create("Legacy step", "keep compatibility", []string{"finished"}, []TaskStepInput{{ID: "fix", Title: "Fix bug"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Checkpoint(task.ID, "fix", StepCompleted, "legacy remains supported"); err != nil {
		t.Fatal(err)
	}
}
