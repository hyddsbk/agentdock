package taskstate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTaskSchemaCompatibilityAllLegacyTaskSchemasRemainReadableAsSchema11(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	for version := legacySchemaVersion; version < SchemaVersion; version++ {
		version := version
		t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
			id := fmt.Sprintf("tsk_%016x", version)
			raw, err := json.Marshal(Task{SchemaVersion: version, ID: id, Title: "legacy", Status: StatusActive})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, id+".json"), raw, 0o600); err != nil {
				t.Fatal(err)
			}
			loaded, err := store.Get(id)
			if err != nil {
				t.Fatalf("legacy schema %d must remain readable: %v", version, err)
			}
			if loaded.SchemaVersion != SchemaVersion {
				t.Fatalf("legacy schema %d loaded as %d, want %d", version, loaded.SchemaVersion, SchemaVersion)
			}
		})
	}
}

func TestTaskSchemaCompatibilityNewTaskPersistsSchema11(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.Create("P14 current write", "write only current schema", []string{"current schema persisted"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, task.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var persisted Task
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.SchemaVersion != SchemaVersion || SchemaVersion != 11 {
		t.Fatalf("persisted schema=%d current=%d, want 11", persisted.SchemaVersion, SchemaVersion)
	}
}

func TestTaskSchemaCompatibilityLegacyTaskSchemaCannotBeWrittenDirectly(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []int{outcomeSchemaVersion, SchemaVersion + 1} {
		task := Task{SchemaVersion: version, ID: fmt.Sprintf("tsk_%016x", version), Title: "invalid write", Status: StatusActive}
		err := store.saveLocked(task)
		if err == nil {
			t.Fatalf("schema %d write unexpectedly succeeded", version)
		}
		if !strings.Contains(err.Error(), "current writable schema is 11") {
			t.Fatalf("schema %d write error = %v", version, err)
		}
	}
}

func TestTaskSchemaCompatibilityRejectsUnsupportedReadVersions(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []int{0, SchemaVersion + 1} {
		id := fmt.Sprintf("tsk_%016x", version+100)
		raw, err := json.Marshal(Task{SchemaVersion: version, ID: id, Title: "unsupported", Status: StatusActive})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, id+".json"), raw, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Get(id); err == nil || !strings.Contains(err.Error(), "unsupported task schema version") {
			t.Fatalf("schema %d read error = %v, want unsupported schema error", version, err)
		}
	}
}
