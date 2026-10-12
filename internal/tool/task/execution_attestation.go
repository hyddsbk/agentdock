package task

import (
	"errors"
	"fmt"
	"strings"

	"github.com/uvwt/agentdock/internal/execevidence"
	"github.com/uvwt/agentdock/internal/taskstate"
)

func (s *Service) checkpointWithReceiptEvidence(
	taskID, stepID, status, summary string,
	tddRequest *TDDSeamRequest,
	executionReceipts []ExecutionReceiptRequest,
	diagnostic *taskstate.DiagnosticEvidence,
	legacyEvidence *taskstate.ExecutionEvidence,
) (taskstate.Task, error) {
	if tddRequest != nil && len(executionReceipts) > 0 {
		return taskstate.Task{}, errors.New("tdd_seam and execution_receipts cannot be combined")
	}
	if tddRequest != nil {
		return s.checkpointWithTDDReceipts(taskID, stepID, status, summary, tddRequest, diagnostic, legacyEvidence)
	}
	if len(executionReceipts) == 0 {
		return s.tasks.CheckpointWithEvidence(taskID, stepID, status, summary, diagnostic, legacyEvidence)
	}
	return s.checkpointWithExecutionReceipts(taskID, stepID, status, summary, executionReceipts, diagnostic, legacyEvidence)
}

func (s *Service) checkpointWithExecutionReceipts(
	taskID, stepID, status, summary string,
	requests []ExecutionReceiptRequest,
	diagnostic *taskstate.DiagnosticEvidence,
	legacyEvidence *taskstate.ExecutionEvidence,
) (taskstate.Task, error) {
	if status != taskstate.StepCompleted {
		return taskstate.Task{}, errors.New("execution_receipts are only valid for completed checkpoints")
	}
	if s.receipts == nil {
		return taskstate.Task{}, errors.New("executor-backed execution evidence is not configured")
	}

	current, err := s.tasks.Get(taskID)
	if err != nil {
		return taskstate.Task{}, err
	}
	var contract *taskstate.ExecutionContract
	for i := range current.Steps {
		if current.Steps[i].ID == stepID {
			contract = current.Steps[i].ExecutionContract
			break
		}
	}
	if contract == nil {
		return taskstate.Task{}, errors.New("execution receipts supplied for step without declared execution contract")
	}
	if len(requests) != len(contract.Checks) {
		return taskstate.Task{}, fmt.Errorf("execution contract requires %d receipt refs, got %d", len(contract.Checks), len(requests))
	}

	byCheck := make(map[string]string, len(requests))
	for _, request := range requests {
		checkID := strings.TrimSpace(request.CheckID)
		ref := strings.TrimSpace(request.Ref)
		if checkID == "" || ref == "" {
			return taskstate.Task{}, errors.New("execution receipt check_id and ref are required")
		}
		if _, exists := byCheck[checkID]; exists {
			return taskstate.Task{}, fmt.Errorf("duplicate execution receipt for check %s", checkID)
		}
		byCheck[checkID] = ref
	}

	refs := make([]string, 0, len(contract.Checks))
	for _, check := range contract.Checks {
		ref, ok := byCheck[check.ID]
		if !ok {
			return taskstate.Task{}, fmt.Errorf("execution receipt missing for check %s", check.ID)
		}
		refs = append(refs, ref)
		delete(byCheck, check.ID)
	}
	if len(byCheck) > 0 {
		return taskstate.Task{}, errors.New("execution receipts contain undeclared check ids")
	}

	claimKey := "task:" + taskID + ":step:" + stepID
	var updated taskstate.Task
	err = s.receipts.WithClaims(refs, claimKey, func(receipts []execevidence.Receipt) error {
		evidence, err := attestExecutionContract(contract, refs, receipts, legacyEvidence)
		if err != nil {
			return err
		}
		updated, err = s.tasks.CheckpointWithEvidenceAndTDD(taskID, stepID, status, summary, diagnostic, evidence, nil)
		return err
	})
	if err != nil {
		return taskstate.Task{}, err
	}
	return updated, nil
}

func attestExecutionContract(
	contract *taskstate.ExecutionContract,
	refs []string,
	receipts []execevidence.Receipt,
	legacyEvidence *taskstate.ExecutionEvidence,
) (*taskstate.ExecutionEvidence, error) {
	if contract == nil {
		return nil, errors.New("execution contract is required")
	}
	if len(refs) != len(contract.Checks) || len(receipts) != len(contract.Checks) {
		return nil, errors.New("execution receipt count does not match declared checks")
	}
	evidenceRefs := make([]string, 0, len(refs)+12)
	seenRefs := map[string]struct{}{}
	if legacyEvidence != nil {
		for _, ref := range legacyEvidence.EvidenceRefs {
			if _, exists := seenRefs[ref]; exists {
				continue
			}
			seenRefs[ref] = struct{}{}
			evidenceRefs = append(evidenceRefs, ref)
		}
	}
	attestations := make([]*taskstate.ExecutionRun, 0, len(receipts))
	for i, receipt := range receipts {
		ref := refs[i]
		if _, exists := seenRefs[ref]; !exists {
			seenRefs[ref] = struct{}{}
			evidenceRefs = append(evidenceRefs, ref)
		}
		attestations = append(attestations, &taskstate.ExecutionRun{
			CheckID:       contract.Checks[i].ID,
			Command:       receipt.Command,
			CommandSHA256: receipt.CommandSHA256,
			ExitCode:      receipt.ExitCode,
			Status:        receipt.Status,
			EvidenceRef:   ref,
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
		})
	}
	evidence := &taskstate.ExecutionEvidence{EvidenceRefs: evidenceRefs, Attestations: attestations}
	if err := contract.ValidateCompletion(evidence); err != nil {
		return nil, err
	}
	return evidence, nil
}
