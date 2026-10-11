package taskstate

import "testing"

func TestCreateAcceptsNexusCompatibleDottedStepIDs(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.Create(
		"Dotted step ids",
		"keep workflow and task id grammar aligned",
		[]string{"done"},
		[]TaskStepInput{
			{ID: "build.api", Title: "Build API"},
			{ID: "verify.api", Title: "Verify API", DependsOn: []string{"build.api"}},
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(task.Steps) != 2 || task.Steps[1].DependsOn[0] != "build.api" {
		t.Fatalf("dotted dependency graph = %#v", task.Steps)
	}
}
