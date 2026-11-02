package task

import (
	"context"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/taskstate"
)

func TestTaskManageEvidenceCheckpointReplayIdempotencyKey(t *testing.T) {
	service, _ := newTaskTestService(t)
	created, err := service.manageTest(context.Background(), map[string]any{
		"action": "create", "title": "checkpoint replay", "goal": "keep one logical observation canonical",
		"completion_conditions": []string{"one observation persists"},
		"steps":                 []map[string]any{{"id": "inspect", "title": "Inspect"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID := created["task_id"].(string)

	req := map[string]any{
		"action": "checkpoint", "task_id": taskID, "step_id": "inspect",
		"status": "in_progress", "summary": "observation recorded",
		"idempotency_key": "inspect-observation-1",
		"evidence_records": []map[string]any{{
			"statement": "Observed one stable state.", "fact_status": "observed",
			"source_refs": []string{"test://task-manage/idempotency"}, "coverage": "complete",
		}},
	}
	if _, err := service.manageTest(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	loaded, err := service.manageTest(context.Background(), map[string]any{"action": "get", "task_id": taskID})
	if err != nil {
		t.Fatal(err)
	}
	before := loaded["task"].(taskstate.Task)
	if len(before.Steps[0].EvidenceRecords) != 1 || len(before.Steps[0].CheckpointReplays) != 1 {
		t.Fatalf("initial checkpoint state = %#v", before.Steps[0])
	}
	beforeEvidenceID := before.Steps[0].EvidenceRecords[0].ID
	beforeEvents := len(before.Events)
	beforeUpdated := before.UpdatedAt

	if _, err := service.manageTest(context.Background(), req); err != nil {
		t.Fatalf("same logical replay failed: %v", err)
	}
	loaded, err = service.manageTest(context.Background(), map[string]any{"action": "get", "task_id": taskID})
	if err != nil {
		t.Fatal(err)
	}
	after := loaded["task"].(taskstate.Task)
	if len(after.Steps[0].EvidenceRecords) != 1 {
		t.Fatalf("replay evidence count = %d, want 1", len(after.Steps[0].EvidenceRecords))
	}
	if after.Steps[0].EvidenceRecords[0].ID != beforeEvidenceID {
		t.Fatalf("replay evidence id changed: before=%q after=%q", beforeEvidenceID, after.Steps[0].EvidenceRecords[0].ID)
	}
	if len(after.Events) != beforeEvents {
		t.Fatalf("replay event count changed: before=%d after=%d", beforeEvents, len(after.Events))
	}
	if !after.UpdatedAt.Equal(beforeUpdated) {
		t.Fatalf("replay UpdatedAt changed: before=%s after=%s", beforeUpdated, after.UpdatedAt)
	}

	conflicting := map[string]any{
		"action": "checkpoint", "task_id": taskID, "step_id": "inspect",
		"status": "in_progress", "summary": "observation recorded",
		"idempotency_key": "inspect-observation-1",
		"evidence_records": []map[string]any{{
			"statement": "Observed a materially different state.", "fact_status": "observed",
			"source_refs": []string{"test://task-manage/idempotency-conflict"}, "coverage": "complete",
		}},
	}
	if _, err := service.manageTest(context.Background(), conflicting); err == nil || !strings.Contains(strings.ToLower(err.Error()), "idempot") {
		t.Fatalf("conflicting replay error = %v, want idempotency conflict", err)
	}
}

func TestTaskManageCheckpointIdempotencyKeyRejectsUnrelatedCheckpointModes(t *testing.T) {
	service, _ := newTaskTestService(t)
	created, err := service.manageTest(context.Background(), map[string]any{
		"action": "create", "title": "checkpoint key validation", "goal": "keep replay key scoped",
		"completion_conditions": []string{"invalid combinations rejected"},
		"steps":                 []map[string]any{{"id": "inspect", "title": "Inspect"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID := created["task_id"].(string)

	_, err = service.manageTest(context.Background(), map[string]any{
		"action": "checkpoint", "task_id": taskID, "step_id": "inspect",
		"status": "in_progress", "summary": "plain progress", "idempotency_key": "plain-1",
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "requires diagnostic or evidence_records") {
		t.Fatalf("plain checkpoint idempotency error = %v", err)
	}
}
