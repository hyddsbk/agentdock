package task

import (
	"context"
	"testing"

	"github.com/uvwt/agentdock/internal/taskstate"
)

func TestWorkflowTemplatePropagatesExecutionContractIntoTask(t *testing.T) {
	rt, _ := newTaskTestService(t)
	template := taskstate.Template{
		ID: "execution.template", Version: "1.0.0", Title: "Execution template",
		CompletionConditions: []string{"verified"},
		Steps: []taskstate.TemplateStep{{
			ID: "verify", Title: "Verify release", Phase: taskstate.PhaseVerify,
			ExecutionContract: &taskstate.ExecutionContractSpec{
				Ordered: true,
				Checks: []taskstate.ExecutionCheckSpec{
					{ID: "build", Command: "go build ./..."},
					{ID: "test", Command: "go test ./...", AllowedExitCodes: []int{0}},
				},
			},
		}},
	}
	createTestWorkflowTemplate(t, rt, template)

	created, err := rt.manageTest(context.Background(), map[string]any{
		"action": "create", "title": "From execution template", "goal": "preserve execution contract",
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
	if len(task.Steps) != 1 || task.Steps[0].ExecutionContract == nil {
		t.Fatalf("workflow execution contract was not propagated: %#v", task.Steps)
	}
	contract := task.Steps[0].ExecutionContract
	if contract.DeclaredAt.IsZero() || !contract.Ordered || len(contract.Checks) != 2 ||
		contract.Checks[0].CommandSHA256 == "" || len(contract.Checks[0].AllowedExitCodes) != 1 ||
		contract.Checks[0].AllowedExitCodes[0] != 0 {
		t.Fatalf("workflow execution contract declaration invalid: %#v", contract)
	}
}
