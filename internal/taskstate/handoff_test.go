package taskstate

import (
	"strings"
	"testing"
)

func handoffTestSteps() []TaskStepInput {
	return []TaskStepInput{
		{
			ID: "inspect", Title: "Inspect",
			CompletionConditions: []string{"Inspection evidence exists."},
			ContextRefs:          []string{"artifact://inspect/latest"},
		},
		{
			ID: "build", Title: "Build", DependsOn: []string{"inspect"},
			CompletionConditions: []string{"Build output is verified."},
			ContextRefs:          []string{"spec://feature/current"},
		},
	}
}

func TestHandoffPersistsCompactResumeState(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.Create("handoff", "continue safely", []string{"done"}, handoffTestSteps(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Checkpoint(task.ID, "inspect", StepCompleted, "inspection complete"); err != nil {
		t.Fatal(err)
	}
	task, err = store.Handoff(
		task.ID,
		"build",
		"Inspection is complete; continue the build from the pinned spec.",
		[]string{"artifact://inspect/latest", "commit://abc123"},
		[]string{"Production activation still requires rollback verification."},
	)
	if err != nil {
		t.Fatal(err)
	}
	if task.Handoff == nil {
		t.Fatal("handoff was not persisted")
	}
	if task.Handoff.NextStepID != "build" || task.Handoff.Revision == "" || task.Handoff.CreatedAt.IsZero() {
		t.Fatalf("handoff = %#v", task.Handoff)
	}
	if len(task.Handoff.ContextRefs) != 2 || len(task.Handoff.Risks) != 1 {
		t.Fatalf("handoff payload = %#v", task.Handoff)
	}
	if task.Summary != "Inspection is complete; continue the build from the pinned spec." {
		t.Fatalf("task summary = %q", task.Summary)
	}

	loaded, err := store.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Handoff == nil || loaded.Handoff.Revision != task.Handoff.Revision {
		t.Fatalf("loaded handoff = %#v", loaded.Handoff)
	}
}

func TestHandoffRequiresCurrentOrDependencyReadyNextStep(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.Create("handoff", "validate frontier", []string{"done"}, handoffTestSteps(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Handoff(task.ID, "build", "too early", nil, nil); err == nil || !strings.Contains(err.Error(), "blocked by incomplete dependencies") {
		t.Fatalf("blocked next step accepted: %v", err)
	}
	if _, err := store.Checkpoint(task.ID, "inspect", StepCompleted, "inspection complete"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Handoff(task.ID, "inspect", "already complete", nil, nil); err == nil || !strings.Contains(err.Error(), "already completed") {
		t.Fatalf("completed next step accepted: %v", err)
	}
	if _, err := store.Handoff(task.ID, "missing", "unknown step", nil, nil); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unknown next step accepted: %v", err)
	}
	if _, err := store.Handoff(task.ID, "", "missing next step", nil, nil); err == nil || !strings.Contains(err.Error(), "next_step_id is required") {
		t.Fatalf("empty next step accepted: %v", err)
	}
}

func TestHandoffValidatesPointersAndRisks(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.Create("handoff", "validate packet", []string{"done"}, []TaskStepInput{{ID: "work", Title: "Work"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Handoff(task.ID, "work", "duplicate refs", []string{"artifact://a", "artifact://a"}, nil); err == nil || !strings.Contains(err.Error(), "duplicate context ref") {
		t.Fatalf("duplicate context refs accepted: %v", err)
	}
	if _, err := store.Handoff(task.ID, "work", "blank ref", []string{""}, nil); err == nil || !strings.Contains(err.Error(), "invalid context ref") {
		t.Fatalf("blank context ref accepted: %v", err)
	}
	if _, err := store.Handoff(task.ID, "work", "duplicate risks", nil, []string{"risk", "RISK"}); err == nil || !strings.Contains(err.Error(), "risks must be non-empty and distinct") {
		t.Fatalf("duplicate risks accepted: %v", err)
	}
}

func TestTaskMutationInvalidatesStaleHandoff(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.Create("handoff", "invalidate stale packet", []string{"done"}, []TaskStepInput{{ID: "work", Title: "Work"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err = store.Handoff(task.ID, "work", "continue work", []string{"artifact://state"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if task.Handoff == nil {
		t.Fatal("handoff missing before mutation")
	}
	task, err = store.Checkpoint(task.ID, "work", StepInProgress, "work started")
	if err != nil {
		t.Fatal(err)
	}
	if task.Handoff != nil {
		t.Fatalf("checkpoint preserved stale handoff: %#v", task.Handoff)
	}

	task, err = store.Handoff(task.ID, "work", "continue work after interruption", nil, []string{"external dependency may fail"})
	if err != nil {
		t.Fatal(err)
	}
	task, err = store.Block(task.ID, "external dependency unavailable")
	if err != nil {
		t.Fatal(err)
	}
	if task.Handoff != nil {
		t.Fatalf("block preserved stale handoff: %#v", task.Handoff)
	}

	task, err = store.Handoff(task.ID, "work", "resume after dependency returns", nil, []string{"verify dependency before write"})
	if err != nil {
		t.Fatal(err)
	}
	task, err = store.Resume(task.ID, "dependency restored")
	if err != nil {
		t.Fatal(err)
	}
	if task.Handoff != nil {
		t.Fatalf("resume preserved stale handoff: %#v", task.Handoff)
	}
}

func TestNoStepTaskHandoffAllowsNoNextStep(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.Create("handoff", "resume a task without explicit steps", []string{"done"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err = store.Handoff(task.ID, "", "Continue from the authoritative issue.", []string{"issue://123"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if task.Handoff == nil || task.Handoff.NextStepID != "" {
		t.Fatalf("no-step handoff = %#v", task.Handoff)
	}
}
