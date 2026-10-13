package task

import (
	"errors"
	"fmt"
	"strings"

	"github.com/uvwt/agentdock/internal/execevidence"
	"github.com/uvwt/agentdock/internal/taskstate"
)

func (s *Service) checkpointWithTDDReceipts(
	taskID, stepID, status, summary string,
	request *TDDSeamRequest,
	diagnostic *taskstate.DiagnosticEvidence,
	executionEvidence *taskstate.ExecutionEvidence,
	evidenceRecords []taskstate.EvidenceRecordInput,
	unknownUpdates []taskstate.UnknownUpdate,
) (taskstate.Task, error) {
	if request == nil {
		return s.tasks.CheckpointWithEvidenceTDDAndSemantics(taskID, stepID, status, summary, diagnostic, executionEvidence, nil, evidenceRecords, unknownUpdates)
	}
	if status != taskstate.StepCompleted {
		return taskstate.Task{}, errors.New("tdd_seam is only valid for completed checkpoints")
	}
	if s.receipts == nil {
		return taskstate.Task{}, errors.New("executor-backed tdd evidence is not configured")
	}

	current, err := s.tasks.Get(taskID)
	if err != nil {
		return taskstate.Task{}, err
	}
	var declared *taskstate.TDDSeam
	for i := range current.Steps {
		if current.Steps[i].ID == stepID {
			declared = current.Steps[i].TDD
			break
		}
	}
	if declared == nil {
		return taskstate.Task{}, errors.New("tdd evidence supplied for step without declared seam")
	}

	refs := []string{
		strings.TrimSpace(request.RedRef),
		strings.TrimSpace(request.GreenRef),
		strings.TrimSpace(request.RegressionRef),
	}
	for i, ref := range refs {
		if ref == "" {
			return taskstate.Task{}, fmt.Errorf("tdd receipt ref %d is required", i+1)
		}
	}
	claimKey := "task:" + taskID + ":step:" + stepID
	var updated taskstate.Task
	err = s.receipts.WithClaims(refs, claimKey, func(receipts []execevidence.Receipt) error {
		attested, err := attestTDDSeam(declared, refs, receipts)
		if err != nil {
			return err
		}
		updated, err = s.tasks.CheckpointWithEvidenceTDDAndSemantics(taskID, stepID, status, summary, diagnostic, executionEvidence, attested, evidenceRecords, unknownUpdates)
		return err
	})
	if err != nil {
		return taskstate.Task{}, err
	}
	return updated, nil
}

func attestTDDSeam(declared *taskstate.TDDSeam, refs []string, receipts []execevidence.Receipt) (*taskstate.TDDSeam, error) {
	if declared == nil {
		return nil, errors.New("tdd seam declaration is required")
	}
	if len(refs) != 3 || len(receipts) != 3 {
		return nil, errors.New("tdd seam requires red, green, and regression receipts")
	}
	command := strings.TrimSpace(declared.TestCommand)
	if command == "" || declared.DeclaredAt.IsZero() {
		return nil, errors.New("tdd seam declaration is incomplete")
	}

	names := []string{"red", "green", "regression"}
	expectedFailure := []bool{true, false, false}
	runs := make([]*taskstate.TDDRun, 3)
	for i, receipt := range receipts {
		if receipt.Issuer != execevidence.ExecutorIssuer {
			return nil, fmt.Errorf("tdd %s receipt has untrusted issuer", names[i])
		}
		if strings.TrimSpace(receipt.Command) != command {
			return nil, fmt.Errorf("tdd %s receipt command differs from declared test command", names[i])
		}
		if receipt.CommandSHA256 != execevidence.CommandSHA256(command) {
			return nil, fmt.Errorf("tdd %s receipt command digest mismatch", names[i])
		}
		if strings.ToLower(strings.TrimSpace(receipt.Status)) != "exited" {
			return nil, fmt.Errorf("tdd %s receipt did not exit normally", names[i])
		}
		if receipt.TimedOut {
			return nil, fmt.Errorf("tdd %s receipt timed out", names[i])
		}
		if receipt.StartedAt.Before(declared.DeclaredAt) {
			return nil, fmt.Errorf("tdd %s receipt predates seam declaration", names[i])
		}
		if expectedFailure[i] && receipt.ExitCode == 0 {
			return nil, errors.New("tdd red receipt must fail")
		}
		if !expectedFailure[i] && receipt.ExitCode != 0 {
			return nil, fmt.Errorf("tdd %s receipt must pass", names[i])
		}
		runs[i] = &taskstate.TDDRun{
			Command:       receipt.Command,
			CommandSHA256: receipt.CommandSHA256,
			ExitCode:      receipt.ExitCode,
			Status:        receipt.Status,
			EvidenceRef:   refs[i],
			Executor:      receipt.Issuer,
			SessionID:     receipt.SessionID,
			StartedAt:     receipt.StartedAt,
			FinishedAt:    receipt.FinishedAt,
			Runtime:       receipt.Runtime,
			Workdir:       receipt.Workdir,
			TimedOut:      receipt.TimedOut,
			StdoutSHA256:  receipt.StdoutSHA256,
			StderrSHA256:  receipt.StderrSHA256,
			ReceiptSHA256: receipt.ReceiptSHA256,
		}
	}
	if !runs[0].FinishedAt.Before(runs[1].StartedAt) {
		return nil, errors.New("tdd receipt order invalid: red must finish before green starts")
	}
	if !runs[1].FinishedAt.Before(runs[2].StartedAt) {
		return nil, errors.New("tdd receipt order invalid: green must finish before regression starts")
	}
	seam := &taskstate.TDDSeam{
		TestCommand: command,
		DeclaredAt:  declared.DeclaredAt,
		Red:         runs[0],
		Green:       runs[1],
		Regression:  runs[2],
	}
	if err := seam.ValidateCompletion(); err != nil {
		return nil, err
	}
	return seam, nil
}
