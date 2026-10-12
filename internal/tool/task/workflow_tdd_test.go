package task

import (
	"context"
	"testing"

	"github.com/uvwt/agentdock/internal/taskstate"
)

func TestWorkflowTemplatePropagatesTDDSeamIntoTask(t *testing.T) {
	rt, _ := newTaskTestService(t)
	command := "go test ./internal/taskstate"
	template := taskstate.Template{
		ID: "tdd.template", Version: "1.0.0", Title: "TDD template",
		CompletionConditions: []string{"tests pass"},
		Steps: []taskstate.TemplateStep{{
			ID: "fix", Title: "Fix with TDD", Phase: taskstate.PhaseExecute, TDDTestCommand: command,
		}},
	}
	createTestWorkflowTemplate(t, rt, template)

	created, err := rt.manageTest(context.Background(), map[string]any{
		"action": "create", "title": "From TDD template", "goal": "preserve seam",
		"completion_conditions": []string{"done"}, "template_id": template.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := rt.manageTest(context.Background(), map[string]any{"action": "get", "task_id": created["task_id"]})
	if err != nil {
		t.Fatal(err)
	}
	task := loaded["task"].(taskstate.Task)
	if len(task.Steps) != 1 || task.Steps[0].TDD == nil {
		t.Fatalf("workflow TDD seam was not propagated: %#v", task.Steps)
	}
	if task.Steps[0].TDD.TestCommand != command || task.Steps[0].TDD.DeclaredAt.IsZero() {
		t.Fatalf("workflow TDD seam declaration invalid: %#v", task.Steps[0].TDD)
	}
}
