package task

import (
	"context"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/execevidence"
	"github.com/uvwt/agentdock/internal/taskstate"
)

func TestGenericExecutionReceiptsAreResolvedAndClaimedServerSide(t *testing.T) {
	service, _ := newTaskTestService(t)
	receipts, err := execevidence.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service.WithExecutionEvidence(receipts)
	created, err := service.manageTest(context.Background(), map[string]any{
		"action": "create", "title": "Release", "goal": "attest generic execution",
		"completion_conditions": []string{"build and test pass"},
		"steps": []map[string]any{{
			"id": "verify", "title": "Verify",
			"execution_contract": map[string]any{
				"ordered": true,
				"checks": []map[string]any{
					{"id": "build", "command": "go build ./...", "allowed_exit_codes": []int{0}},
					{"id": "test", "command": "go test ./...", "allowed_exit_codes": []int{0}},
				},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID := created["task_id"].(string)
	got, err := service.manageTest(context.Background(), map[string]any{"action": "get", "task_id": taskID})
	if err != nil {
		t.Fatal(err)
	}
	task := got["task"].(taskstate.Task)
	declaredAt := task.Steps[0].ExecutionContract.DeclaredAt
	start := declaredAt.Add(time.Second)
	buildRef := issueTDDReceipt(t, receipts, "session-aaaaaaaaaaaaaaaaaaaaaaaa", "go build ./...", 0, start)
	testRef := issueTDDReceipt(t, receipts, "session-bbbbbbbbbbbbbbbbbbbbbbbb", "go test ./...", 0, start.Add(time.Second))
	progress, err := service.manageTest(context.Background(), map[string]any{
		"action": "checkpoint", "task_id": taskID, "step_id": "verify", "status": "completed", "summary": "attested",
		"execution_receipts": []map[string]any{{"check_id": "build", "ref": buildRef}, {"check_id": "test", "ref": testRef}},
	})
	if err != nil {
		t.Fatal(err)
	}
	step := progress["task_summary"].(map[string]any)["steps"].([]map[string]any)[0]
	if step["execution_evidence"] == nil {
		t.Fatalf("execution evidence missing from result: %#v", step)
	}
	got, err = service.manageTest(context.Background(), map[string]any{"action": "get", "task_id": taskID})
	if err != nil {
		t.Fatal(err)
	}
	task = got["task"].(taskstate.Task)
	if len(task.Steps[0].ExecutionEvidence.Attestations) != 2 || task.Steps[0].ExecutionEvidence.Attestations[0].Executor != execevidence.ExecutorIssuer {
		t.Fatalf("attestations = %#v", task.Steps[0].ExecutionEvidence)
	}

	created2, err := service.manageTest(context.Background(), map[string]any{
		"action": "create", "title": "Reuse", "goal": "reject reuse",
		"completion_conditions": []string{"reject reused receipt"},
		"steps": []map[string]any{{
			"id": "verify", "title": "Verify",
			"execution_contract": map[string]any{"checks": []map[string]any{{"id": "build", "command": "go build ./..."}}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.manageTest(context.Background(), map[string]any{
		"action": "checkpoint", "task_id": created2["task_id"].(string), "step_id": "verify", "status": "completed", "summary": "reuse",
		"execution_receipts": []map[string]any{{"check_id": "build", "ref": buildRef}},
	}); err == nil {
		t.Fatal("receipt reuse across task steps was accepted")
	}
}

func TestExecutionReceiptRejectsWrongCommandAndStaleEvidenceWithoutMutation(t *testing.T) {
	service, _ := newTaskTestService(t)
	receipts, err := execevidence.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service.WithExecutionEvidence(receipts)
	created, err := service.manageTest(context.Background(), map[string]any{
		"action": "create", "title": "Verify", "goal": "reject bad receipts",
		"completion_conditions": []string{"safe"},
		"steps": []map[string]any{{
			"id": "verify", "title": "Verify",
			"execution_contract": map[string]any{"checks": []map[string]any{{"id": "test", "command": "go test ./..."}}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID := created["task_id"].(string)
	got, _ := service.manageTest(context.Background(), map[string]any{"action": "get", "task_id": taskID})
	declaredAt := got["task"].(taskstate.Task).Steps[0].ExecutionContract.DeclaredAt
	for name, ref := range map[string]string{
		"wrong command": issueTDDReceipt(t, receipts, "session-cccccccccccccccccccccccc", "go test ./other", 0, declaredAt.Add(time.Second)),
		"stale":         issueTDDReceipt(t, receipts, "session-dddddddddddddddddddddddd", "go test ./...", 0, declaredAt.Add(-time.Second)),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.manageTest(context.Background(), map[string]any{
				"action": "checkpoint", "task_id": taskID, "step_id": "verify", "status": "completed", "summary": name,
				"execution_receipts": []map[string]any{{"check_id": "test", "ref": ref}},
			}); err == nil {
				t.Fatalf("%s receipt was accepted", name)
			}
			loaded, _ := service.manageTest(context.Background(), map[string]any{"action": "get", "task_id": taskID})
			if loaded["task"].(taskstate.Task).Steps[0].Status == taskstate.StepCompleted {
				t.Fatalf("%s mutated step", name)
			}
		})
	}
}

func TestTaskManageSchemaPublishesGenericExecutionContract(t *testing.T) {
	schema := ManageInputSchema(config.Config{})
	props := schema["properties"].(map[string]any)
	if _, ok := props["execution_receipts"]; !ok {
		t.Fatal("task_manage schema missing execution_receipts")
	}
	steps := props["steps"].(map[string]any)
	stepProps := steps["items"].(map[string]any)["properties"].(map[string]any)
	if _, ok := stepProps["execution_contract"]; !ok {
		t.Fatal("step schema missing execution_contract")
	}
}
