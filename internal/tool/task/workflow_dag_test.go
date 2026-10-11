package task

import (
	"context"
	"testing"

	"github.com/uvwt/agentdock/internal/taskstate"
)

func TestTaskManageSingleTemplatePreservesDependencyGraph(t *testing.T) {
	rt, _ := newTaskTestService(t)
	createTestWorkflowTemplate(t, rt, taskstate.Template{
		ID: "dag.template", Version: "1.0.0", Title: "DAG template",
		CompletionConditions: []string{"verified"},
		Steps: []taskstate.TemplateStep{
			{ID: "inspect", Title: "Inspect", Phase: taskstate.PhaseCheck, CompletionConditions: []string{"Inspection is complete."}, ContextRefs: []string{"recall://project/baseline"}},
			{ID: "implement", Title: "Implement", Phase: taskstate.PhaseExecute, DependsOn: []string{"inspect"}, CompletionConditions: []string{"Implementation is complete."}, ContextRefs: []string{"skill://domain-modeling/SKILL.md"}},
		},
	})
	created, err := rt.manageTest(context.Background(), map[string]any{
		"action": "create", "title": "DAG task", "goal": "preserve template graph", "template_id": "dag.template",
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := rt.manageTest(context.Background(), map[string]any{"action": "get", "task_id": created["task_id"]})
	if err != nil {
		t.Fatal(err)
	}
	task := loaded["task"].(taskstate.Task)
	if len(task.Steps) != 2 || len(task.Steps[1].DependsOn) != 1 || task.Steps[1].DependsOn[0] != "inspect" {
		t.Fatalf("template dependency graph was not preserved: %#v", task.Steps)
	}
	if len(task.Steps[1].CompletionConditions) != 1 || task.Steps[1].CompletionConditions[0] != "Implementation is complete." || len(task.Steps[1].ContextRefs) != 1 || task.Steps[1].ContextRefs[0] != "skill://domain-modeling/SKILL.md" {
		t.Fatalf("template step contract was not preserved: %#v", task.Steps[1])
	}
	if ready := taskstate.ReadyStepIDs(task); len(ready) != 1 || ready[0] != "inspect" {
		t.Fatalf("initial ready frontier = %v", ready)
	}
}
