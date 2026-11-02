package taskstate

import "testing"

func TestReadyFrontierDiamondBlockAndResumeInvariant(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.Create(
		"Diamond DAG",
		"prove ready frontier invariants",
		[]string{"graph completes"},
		[]TaskStepInput{
			{ID: "root", Title: "Root", CompletionConditions: []string{"Root completes."}},
			{ID: "left", Title: "Left", DependsOn: []string{"root"}, CompletionConditions: []string{"Left completes."}},
			{ID: "right", Title: "Right", DependsOn: []string{"root"}, CompletionConditions: []string{"Right completes."}},
			{ID: "join", Title: "Join", DependsOn: []string{"left", "right"}, CompletionConditions: []string{"Join completes."}},
			{ID: "verify", Title: "Verify", DependsOn: []string{"join"}, CompletionConditions: []string{"Verify completes."}},
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	assertReady := func(want ...string) {
		t.Helper()
		got := ReadyStepIDs(task)
		if len(got) != len(want) {
			t.Fatalf("ready=%v want=%v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("ready=%v want=%v", got, want)
			}
		}
	}

	assertReady("root")
	task, err = store.Checkpoint(task.ID, "root", StepCompleted, "root done")
	if err != nil {
		t.Fatal(err)
	}
	assertReady("left", "right")

	task, err = store.Block(task.ID, "external dependency unavailable")
	if err != nil {
		t.Fatal(err)
	}
	assertReady()

	task, err = store.Resume(task.ID, "external dependency restored")
	if err != nil {
		t.Fatal(err)
	}
	assertReady("left", "right")

	task, err = store.BatchCheckpoint(task.ID, []string{"left", "right"}, "", "branches done")
	if err != nil {
		t.Fatal(err)
	}
	assertReady("join")
	task, err = store.Checkpoint(task.ID, "join", StepCompleted, "join done")
	if err != nil {
		t.Fatal(err)
	}
	assertReady("verify")
	task, err = store.Checkpoint(task.ID, "verify", StepCompleted, "verify done")
	if err != nil {
		t.Fatal(err)
	}
	assertReady()
}
