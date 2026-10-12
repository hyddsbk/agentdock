package task

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/execevidence"
	"github.com/uvwt/agentdock/internal/taskstate"
)

func issueTDDReceipt(t *testing.T, store *execevidence.Store, sessionID, command string, exitCode int, started time.Time) string {
	t.Helper()
	return issueTDDReceiptState(t, store, sessionID, command, exitCode, "exited", false, started)
}

func issueTDDReceiptState(t *testing.T, store *execevidence.Store, sessionID, command string, exitCode int, status string, timedOut bool, started time.Time) string {
	t.Helper()
	ref, err := store.Issue(execevidence.Receipt{
		Issuer:       execevidence.ExecutorIssuer,
		SessionID:    sessionID,
		Command:      command,
		ExitCode:     exitCode,
		Status:       status,
		TimedOut:     timedOut,
		StartedAt:    started,
		FinishedAt:   started.Add(100 * time.Millisecond),
		Runtime:      "windows",
		Workdir:      `C:\\repo`,
		StdoutSHA256: strings.Repeat("a", 64),
		StderrSHA256: strings.Repeat("b", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func TestTDDCheckpointUsesExecutorReceiptsAndRejectsReuse(t *testing.T) {
	service, _ := newTaskTestService(t)
	receipts, err := execevidence.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service.WithExecutionEvidence(receipts)

	command := "go test ./internal/taskstate"
	created, err := service.manageTest(context.Background(), map[string]any{
		"action": "create", "title": "TDD", "goal": "prove executor evidence",
		"completion_conditions": []string{"tests pass"},
		"steps":                 []map[string]any{{"id": "fix", "title": "Fix", "tdd_test_command": command}},
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID := created["task_id"].(string)
	loaded, err := service.manageTest(context.Background(), map[string]any{"action": "get", "task_id": taskID})
	if err != nil {
		t.Fatal(err)
	}
	task := loaded["task"].(taskstate.Task)
	declaredAt := task.Steps[0].TDD.DeclaredAt
	start := declaredAt.Add(time.Second)
	red := issueTDDReceipt(t, receipts, "session-111111111111111111111111", command, 1, start)
	green := issueTDDReceipt(t, receipts, "session-222222222222222222222222", command, 0, start.Add(time.Second))
	regression := issueTDDReceipt(t, receipts, "session-333333333333333333333333", command, 0, start.Add(2*time.Second))

	if _, err := service.manageTest(context.Background(), map[string]any{
		"action": "checkpoint", "task_id": taskID, "step_id": "fix", "status": "completed", "summary": "attested",
		"tdd_seam": map[string]any{"red_ref": red, "green_ref": green, "regression_ref": regression},
	}); err != nil {
		t.Fatal(err)
	}
	loaded, err = service.manageTest(context.Background(), map[string]any{"action": "get", "task_id": taskID})
	if err != nil {
		t.Fatal(err)
	}
	task = loaded["task"].(taskstate.Task)
	if task.Steps[0].TDD.Red.Executor != execevidence.ExecutorIssuer || task.Steps[0].TDD.Green.CommandSHA256 == "" {
		t.Fatalf("executor attestation missing: %#v", task.Steps[0].TDD)
	}

	created2, err := service.manageTest(context.Background(), map[string]any{
		"action": "create", "title": "TDD reuse", "goal": "reject reuse",
		"completion_conditions": []string{"reuse rejected"},
		"steps":                 []map[string]any{{"id": "fix", "title": "Fix", "tdd_test_command": command}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.manageTest(context.Background(), map[string]any{
		"action": "checkpoint", "task_id": created2["task_id"], "step_id": "fix", "status": "completed", "summary": "reuse",
		"tdd_seam": map[string]any{"red_ref": red, "green_ref": green, "regression_ref": regression},
	})
	if err == nil || !strings.Contains(err.Error(), "already claimed") {
		t.Fatalf("reuse error = %v", err)
	}
}

func TestTDDCheckpointRejectsForgedStaleAndOutOfOrderReceiptsAtomically(t *testing.T) {
	service, _ := newTaskTestService(t)
	receipts, err := execevidence.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service.WithExecutionEvidence(receipts)
	command := "go test ./internal/taskstate"
	created, err := service.manageTest(context.Background(), map[string]any{
		"action": "create", "title": "TDD negatives", "goal": "fail closed",
		"completion_conditions": []string{"bad evidence rejected"},
		"steps":                 []map[string]any{{"id": "fix", "title": "Fix", "tdd_test_command": command}},
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID := created["task_id"].(string)
	loaded, _ := service.manageTest(context.Background(), map[string]any{"action": "get", "task_id": taskID})
	task := loaded["task"].(taskstate.Task)
	declaredAt := task.Steps[0].TDD.DeclaredAt

	stale := issueTDDReceipt(t, receipts, "session-444444444444444444444444", command, 1, declaredAt.Add(-time.Second))
	greenLate := issueTDDReceipt(t, receipts, "session-555555555555555555555555", command, 0, declaredAt.Add(3*time.Second))
	regressionEarly := issueTDDReceipt(t, receipts, "session-666666666666666666666666", command, 0, declaredAt.Add(2*time.Second))
	_, err = service.manageTest(context.Background(), map[string]any{
		"action": "checkpoint", "task_id": taskID, "step_id": "fix", "status": "completed", "summary": "bad",
		"tdd_seam": map[string]any{"red_ref": stale, "green_ref": greenLate, "regression_ref": regressionEarly},
	})
	if err == nil {
		t.Fatal("accepted stale/out-of-order receipts")
	}
	loaded, _ = service.manageTest(context.Background(), map[string]any{"action": "get", "task_id": taskID})
	if loaded["task"].(taskstate.Task).Steps[0].Status != taskstate.StepPending {
		t.Fatal("failed TDD attestation mutated task step")
	}

	_, err = service.manageTest(context.Background(), map[string]any{
		"action": "checkpoint", "task_id": taskID, "step_id": "fix", "status": "completed", "summary": "forged",
		"tdd_seam": map[string]any{
			"red_ref":        "exec://session-777777777777777777777777",
			"green_ref":      greenLate,
			"regression_ref": regressionEarly,
		},
	})
	if err == nil {
		t.Fatal("accepted forged receipt")
	}
}

func TestTDDCheckpointRejectsKilledOrTimedOutRed(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   string
		timedOut bool
	}{
		{name: "killed", status: "killed"},
		{name: "timed out", status: "exited", timedOut: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, _ := newTaskTestService(t)
			receipts, err := execevidence.New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			service.WithExecutionEvidence(receipts)
			command := "go test ./internal/taskstate"
			created, err := service.manageTest(context.Background(), map[string]any{
				"action": "create", "title": "TDD infra failure", "goal": "reject infrastructure failure",
				"completion_conditions": []string{"infra failure rejected"},
				"steps":                 []map[string]any{{"id": "fix", "title": "Fix", "tdd_test_command": command}},
			})
			if err != nil {
				t.Fatal(err)
			}
			taskID := created["task_id"].(string)
			loaded, err := service.manageTest(context.Background(), map[string]any{"action": "get", "task_id": taskID})
			if err != nil {
				t.Fatal(err)
			}
			declared := loaded["task"].(taskstate.Task).Steps[0].TDD.DeclaredAt
			start := declared.Add(time.Second)
			red := issueTDDReceiptState(t, receipts, "session-888888888888888888888888", command, 1, tc.status, tc.timedOut, start)
			green := issueTDDReceipt(t, receipts, "session-999999999999999999999999", command, 0, start.Add(time.Second))
			regression := issueTDDReceipt(t, receipts, "session-aaaaaaaaaaaaaaaaaaaaaaaa", command, 0, start.Add(2*time.Second))
			if _, err := service.manageTest(context.Background(), map[string]any{
				"action": "checkpoint", "task_id": taskID, "step_id": "fix", "status": "completed", "summary": "bad infra evidence",
				"tdd_seam": map[string]any{"red_ref": red, "green_ref": green, "regression_ref": regression},
			}); err == nil {
				t.Fatalf("accepted %s red receipt", tc.name)
			}
			loaded, err = service.manageTest(context.Background(), map[string]any{"action": "get", "task_id": taskID})
			if err != nil {
				t.Fatal(err)
			}
			if loaded["task"].(taskstate.Task).Steps[0].Status != taskstate.StepPending {
				t.Fatalf("%s receipt mutated task", tc.name)
			}
		})
	}
}
