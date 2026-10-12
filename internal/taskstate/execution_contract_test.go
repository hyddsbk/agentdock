package taskstate

import (
	"strings"
	"testing"
	"time"
)

func executionRun(checkID, command, ref string, exitCode int, started time.Time) *ExecutionRun {
	return &ExecutionRun{
		CheckID:       checkID,
		Command:       command,
		CommandSHA256: executionCommandSHA256(command),
		ExitCode:      exitCode,
		Status:        "exited",
		EvidenceRef:   ref,
		Executor:      "agentdock.exec_command/v1",
		SessionID:     strings.TrimPrefix(ref, "exec://"),
		StartedAt:     started,
		FinishedAt:    started.Add(100 * time.Millisecond),
		Runtime:       "windows",
		Workdir:       "C:\\repo",
		StdoutSHA256:  strings.Repeat("a", 64),
		StderrSHA256:  strings.Repeat("b", 64),
		ReceiptSHA256: strings.Repeat("c", 64),
	}
}

func TestExecutionContractRequiresAttestedReceiptsAndPreservesOrder(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.Create("release", "prove commands", []string{"verified"}, []TaskStepInput{{
		ID: "verify", Title: "Verify",
		ExecutionContract: &ExecutionContractSpec{Ordered: true, Checks: []ExecutionCheckSpec{
			{ID: "build", Command: "go build ./...", AllowedExitCodes: []int{0}},
			{ID: "test", Command: "go test ./...", AllowedExitCodes: []int{0}},
		}},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	step := task.Steps[0]
	if step.ExecutionContract == nil || step.ExecutionContract.DeclaredAt.IsZero() || len(step.ExecutionContract.Checks) != 2 {
		t.Fatalf("execution contract not frozen: %#v", step.ExecutionContract)
	}
	if _, err := store.Checkpoint(task.ID, "verify", StepCompleted, "claimed"); err == nil {
		t.Fatal("completion without attested execution evidence was accepted")
	}
	start := step.ExecutionContract.DeclaredAt.Add(time.Second)
	evidence := &ExecutionEvidence{
		EvidenceRefs: []string{"exec://session-111111111111111111111111", "exec://session-222222222222222222222222"},
		Attestations: []*ExecutionRun{
			executionRun("build", "go build ./...", "exec://session-111111111111111111111111", 0, start),
			executionRun("test", "go test ./...", "exec://session-222222222222222222222222", 0, start.Add(time.Second)),
		},
	}
	updated, err := store.CheckpointWithEvidence(task.ID, "verify", StepCompleted, "attested", nil, evidence)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Steps[0].ExecutionEvidence == nil || len(updated.Steps[0].ExecutionEvidence.Attestations) != 2 {
		t.Fatalf("attested evidence missing: %#v", updated.Steps[0].ExecutionEvidence)
	}
}

func TestExecutionContractRejectsStaleWrongExitAndTDDOverlap(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create("overlap", "reject dual authority", []string{"safe"}, []TaskStepInput{{
		ID: "verify", Title: "Verify", TDDTestCommand: "go test ./...",
		ExecutionContract: &ExecutionContractSpec{Checks: []ExecutionCheckSpec{{ID: "test", Command: "go test ./..."}}},
	}}, nil); err == nil {
		t.Fatal("step accepted both TDD seam and generic execution contract")
	}
	task, err := store.Create("release", "reject bad evidence", []string{"safe"}, []TaskStepInput{{
		ID: "verify", Title: "Verify",
		ExecutionContract: &ExecutionContractSpec{Checks: []ExecutionCheckSpec{{ID: "test", Command: "go test ./...", AllowedExitCodes: []int{0}}}},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	declared := task.Steps[0].ExecutionContract.DeclaredAt
	for name, run := range map[string]*ExecutionRun{
		"stale":      executionRun("test", "go test ./...", "exec://session-333333333333333333333333", 0, declared.Add(-time.Second)),
		"wrong exit": executionRun("test", "go test ./...", "exec://session-444444444444444444444444", 1, declared.Add(time.Second)),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := store.CheckpointWithEvidence(task.ID, "verify", StepCompleted, name, nil, &ExecutionEvidence{
				EvidenceRefs: []string{run.EvidenceRef}, Attestations: []*ExecutionRun{run},
			})
			if err == nil {
				t.Fatalf("%s execution evidence was accepted", name)
			}
		})
	}
}

func TestExecutionContractRejectsKilledTimedOutAndOverlappingOrder(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.Create("release", "reject invalid terminal evidence", []string{"safe"}, []TaskStepInput{{
		ID: "verify", Title: "Verify",
		ExecutionContract: &ExecutionContractSpec{Ordered: true, Checks: []ExecutionCheckSpec{
			{ID: "build", Command: "go build ./..."},
			{ID: "test", Command: "go test ./..."},
		}},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	start := task.Steps[0].ExecutionContract.DeclaredAt.Add(time.Second)
	makeEvidence := func(first, second *ExecutionRun) *ExecutionEvidence {
		return &ExecutionEvidence{
			EvidenceRefs: []string{first.EvidenceRef, second.EvidenceRef},
			Attestations: []*ExecutionRun{first, second},
		}
	}

	t.Run("killed", func(t *testing.T) {
		build := executionRun("build", "go build ./...", "exec://session-666666666666666666666666", 0, start)
		build.Status = "killed"
		testRun := executionRun("test", "go test ./...", "exec://session-777777777777777777777777", 0, start.Add(time.Second))
		if _, err := store.CheckpointWithEvidence(task.ID, "verify", StepCompleted, "killed", nil, makeEvidence(build, testRun)); err == nil {
			t.Fatal("killed receipt was accepted")
		}
	})

	t.Run("timeout", func(t *testing.T) {
		build := executionRun("build", "go build ./...", "exec://session-888888888888888888888888", 0, start)
		build.TimedOut = true
		testRun := executionRun("test", "go test ./...", "exec://session-999999999999999999999999", 0, start.Add(time.Second))
		if _, err := store.CheckpointWithEvidence(task.ID, "verify", StepCompleted, "timeout", nil, makeEvidence(build, testRun)); err == nil {
			t.Fatal("timed-out receipt was accepted")
		}
	})

	t.Run("overlap", func(t *testing.T) {
		build := executionRun("build", "go build ./...", "exec://session-aaaaaaaaaaaaaaaaaaaaaaaa", 0, start)
		testRun := executionRun("test", "go test ./...", "exec://session-bbbbbbbbbbbbbbbbbbbbbbbb", 0, start.Add(50*time.Millisecond))
		if _, err := store.CheckpointWithEvidence(task.ID, "verify", StepCompleted, "overlap", nil, makeEvidence(build, testRun)); err == nil {
			t.Fatal("overlapping ordered receipts were accepted")
		}
	})
}

func TestExecutionContractAllowsDeclaredNonZeroExit(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.Create("verify", "accept declared exit", []string{"checked"}, []TaskStepInput{{
		ID: "verify", Title: "Verify",
		ExecutionContract: &ExecutionContractSpec{Checks: []ExecutionCheckSpec{{
			ID: "probe", Command: "probe --expect-missing", AllowedExitCodes: []int{2},
		}}},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	start := task.Steps[0].ExecutionContract.DeclaredAt.Add(time.Second)
	run := executionRun("probe", "probe --expect-missing", "exec://session-555555555555555555555555", 2, start)
	if _, err := store.CheckpointWithEvidence(task.ID, "verify", StepCompleted, "expected nonzero", nil, &ExecutionEvidence{
		EvidenceRefs: []string{run.EvidenceRef}, Attestations: []*ExecutionRun{run},
	}); err != nil {
		t.Fatalf("declared nonzero exit rejected: %v", err)
	}
}

func TestBatchCheckpointCannotBypassExecutionContract(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.Create("release", "batch guard", []string{"safe"}, []TaskStepInput{{
		ID: "verify", Title: "Verify",
		ExecutionContract: &ExecutionContractSpec{Checks: []ExecutionCheckSpec{{ID: "test", Command: "go test ./..."}}},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.BatchCheckpoint(task.ID, []string{"verify"}, "", "batch"); err == nil {
		t.Fatal("batch checkpoint bypassed generic execution contract")
	}
}
