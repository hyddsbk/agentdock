package task

import (
	"context"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/execevidence"
	"github.com/uvwt/agentdock/internal/taskstate"
)

func TestExecutionReceiptCheckpointReplayIsStateIdempotent(t *testing.T) {
	service, _ := newTaskTestService(t)
	receipts, err := execevidence.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service.WithExecutionEvidence(receipts)

	created, err := service.manageTest(context.Background(), map[string]any{
		"action": "create", "title": "Replay-safe checkpoint", "goal": "do not duplicate committed checkpoint state",
		"completion_conditions": []string{"verified once"},
		"steps": []map[string]any{{
			"id": "verify", "title": "Verify",
			"execution_contract": map[string]any{"checks": []map[string]any{{"id": "test", "command": "go test ./..."}}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID := created["task_id"].(string)
	loaded, err := service.manageTest(context.Background(), map[string]any{"action": "get", "task_id": taskID})
	if err != nil {
		t.Fatal(err)
	}
	declaredAt := loaded["task"].(taskstate.Task).Steps[0].ExecutionContract.DeclaredAt
	ref := issueTDDReceipt(t, receipts, "session-eeeeeeeeeeeeeeeeeeeeeeee", "go test ./...", 0, declaredAt.Add(time.Second))
	req := map[string]any{
		"action": "checkpoint", "task_id": taskID, "step_id": "verify", "status": "completed", "summary": "attested once",
		"execution_receipts": []map[string]any{{"check_id": "test", "ref": ref}},
	}
	if _, err := service.manageTest(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	first, err := service.manageTest(context.Background(), map[string]any{"action": "get", "task_id": taskID})
	if err != nil {
		t.Fatal(err)
	}
	before := first["task"].(taskstate.Task)
	beforeEvents := len(before.Events)
	beforeUpdated := before.UpdatedAt

	if _, err := service.manageTest(context.Background(), req); err != nil {
		t.Fatalf("same-step receipt replay should be accepted idempotently: %v", err)
	}
	second, err := service.manageTest(context.Background(), map[string]any{"action": "get", "task_id": taskID})
	if err != nil {
		t.Fatal(err)
	}
	after := second["task"].(taskstate.Task)
	if len(after.Events) != beforeEvents {
		t.Fatalf("checkpoint replay appended events: before=%d after=%d", beforeEvents, len(after.Events))
	}
	if !after.UpdatedAt.Equal(beforeUpdated) {
		t.Fatalf("checkpoint replay rewrote task timestamp: before=%s after=%s", beforeUpdated, after.UpdatedAt)
	}
	claim, err := receipts.ResolveClaim(ref)
	if err != nil {
		t.Fatal(err)
	}
	if claim.ClaimKey != "task:"+taskID+":step:verify" {
		t.Fatalf("claim key = %q", claim.ClaimKey)
	}
}
