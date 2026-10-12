package taskstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSchemaV8TDDMigratesToV9WithoutDataLoss(t *testing.T) {
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
