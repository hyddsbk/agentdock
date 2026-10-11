package taskstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSchemaV6HandoffMigratesToV7WithoutDataLoss(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.Create(
		"P2 task", "preserve handoff across P3 migration",
		[]string{"handoff survives"},
		[]TaskStepInput{{ID: "build", Title: "Build", ContextRefs: []string{"artifact://p2/context"}}},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	task, err = store.Handoff(task.ID, "build", "continue build", []string{"artifact://p2/context"}, []string{"watch compatibility"})
	if err != nil {
		t.Fatal(err)
	}
	originalRevision := task.Handoff.Revision
	task.SchemaVersion = handoffSchemaVersion
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
		t.Fatalf("schema version = %d", loaded.SchemaVersion)
	}
	if loaded.Handoff == nil || loaded.Handoff.Revision != originalRevision ||
		loaded.Handoff.NextStepID != "build" || len(loaded.Handoff.ContextRefs) != 1 ||
		len(loaded.Handoff.Risks) != 1 {
		t.Fatalf("v6 handoff lost during migration: %#v", loaded.Handoff)
	}
	if len(loaded.RequirementRefs) != 0 || len(loaded.Steps[0].RequirementRefs) != 0 || loaded.Steps[0].ExecutionEvidence != nil {
		t.Fatalf("v6 task acquired invalid traceability state: %#v", loaded)
	}
}
