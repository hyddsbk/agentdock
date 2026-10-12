package taskstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSchemaV7TraceabilityMigratesToV8WithoutDataLoss(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateWithTraceability(
		"P3 task", "preserve traceability across P4 migration", "global", "",
		[]string{"traceability survives"},
		[]TaskStepInput{{
			ID: "build", Title: "Build", RequirementRefs: []string{"spec://p4/contract"},
			CompletionConditions: []string{"build verified"},
			ContextRefs:          []string{"artifact://p3/context"},
			Quality:              &StepQuality{Kind: StepKindVerification, Outcome: "verify build", DependencyReasons: map[string]string{}},
		}},
		nil,
		[]string{"spec://p4/contract"},
	)
	if err != nil {
		t.Fatal(err)
	}
	task.SchemaVersion = traceabilitySchemaVersion
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
	if len(loaded.RequirementRefs) != 1 || loaded.RequirementRefs[0] != "spec://p4/contract" {
		t.Fatalf("top-level traceability lost: %#v", loaded.RequirementRefs)
	}
	if len(loaded.Steps) != 1 || len(loaded.Steps[0].RequirementRefs) != 1 || loaded.Steps[0].TDD != nil {
		t.Fatalf("v7 step migrated incorrectly: %#v", loaded.Steps)
	}
}
