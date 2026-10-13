package taskstate

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *Store) Checkpoint(id, stepID, status, summary string, diagnostics ...*DiagnosticEvidence) (Task, error) {
	if len(diagnostics) > 1 {
		return Task{}, errors.New("checkpoint accepts at most one diagnostic evidence object")
	}
	var diagnostic *DiagnosticEvidence
	if len(diagnostics) == 1 {
		diagnostic = diagnostics[0]
	}
	return s.CheckpointWithEvidence(id, stepID, status, summary, diagnostic, nil)
}

func (s *Store) CheckpointWithEvidence(id, stepID, status, summary string, diagnostic *DiagnosticEvidence, executionEvidence *ExecutionEvidence) (Task, error) {
	return s.CheckpointWithEvidenceAndTDD(id, stepID, status, summary, diagnostic, executionEvidence, nil)
}

func (s *Store) CheckpointWithTDD(id, stepID, status, summary string, evidence *TDDSeam) (Task, error) {
	return s.CheckpointWithEvidenceAndTDD(id, stepID, status, summary, nil, nil, evidence)
}

func (s *Store) CheckpointWithEvidenceAndTDD(id, stepID, status, summary string, diagnostic *DiagnosticEvidence, executionEvidence *ExecutionEvidence, tddEvidence *TDDSeam) (Task, error) {
	return s.CheckpointWithEvidenceTDDAndSemantics(id, stepID, status, summary, diagnostic, executionEvidence, tddEvidence, nil, nil)
}

func (s *Store) CheckpointWithEvidenceTDDAndSemantics(
	id, stepID, status, summary string,
	diagnostic *DiagnosticEvidence,
	executionEvidence *ExecutionEvidence,
	tddEvidence *TDDSeam,
	evidenceRecords []EvidenceRecordInput,
	unknownUpdates []UnknownUpdate,
) (Task, error) {
	return s.mutate(id, func(task *Task, now time.Time) error {
		if err := requireActive(task); err != nil {
			return err
		}
		if err := requireFinalReviewOpen(task); err != nil {
			return err
		}
		stepID = strings.TrimSpace(stepID)
		status = strings.ToLower(strings.TrimSpace(status))
		summary = strings.TrimSpace(summary)
		if stepID == "" || summary == "" {
			return errors.New("step_id and summary are required")
		}
		if err := validateTextLimit("checkpoint summary", summary, maxTaskSummaryBytes); err != nil {
			return err
		}
		if status != StepPending && status != StepInProgress && status != StepCompleted {
			return errors.New("step status must be pending, in_progress, or completed")
		}

		stepIndex := -1
		for i := range task.Steps {
			if task.Steps[i].ID == stepID {
				stepIndex = i
				break
			}
		}
		if stepIndex < 0 {
			return fmt.Errorf("task step %s not found", stepID)
		}
		step := &task.Steps[stepIndex]
		if (len(evidenceRecords) > 0 || len(unknownUpdates) > 0) && status == StepPending {
			return errors.New("evidence_records and unknown_updates require an in_progress or completed checkpoint")
		}
		if tddEvidence != nil && status != StepCompleted {
			return errors.New("tdd evidence requires completed checkpoint status")
		}
		if tddEvidence != nil && step.TDD == nil {
			return errors.New("tdd evidence supplied for step without declared seam")
		}
		if status == StepCompleted && step.TDD != nil {
			if tddEvidence == nil {
				return errors.New("declared tdd seam requires evidence to complete")
			}
			if tddEvidence.TestCommand != step.TDD.TestCommand {
				return errors.New("tdd test command differs from declared seam")
			}
			if !tddEvidence.DeclaredAt.Equal(step.TDD.DeclaredAt) {
				return errors.New("tdd seam declaration timestamp differs from declared seam")
			}
			if err := tddEvidence.ValidateCompletion(); err != nil {
				return err
			}
		}
		if status == StepCompleted && step.ExecutionContract != nil && executionEvidence == nil && step.ExecutionEvidence == nil {
			return errors.New("declared execution contract requires executor evidence to complete")
		}
		if diagnostic != nil {
			if status == StepPending {
				return errors.New("diagnostic evidence can only be recorded on an in_progress or completed checkpoint")
			}
			kind := ""
			if step.Quality != nil {
				kind = step.Quality.Kind
			}
			normalized, err := normalizeDiagnosticEvidence(step.ID, kind, diagnostic)
			if err != nil {
				return err
			}
			step.Diagnostic = normalized
		}
		if executionEvidence != nil {
			if status == StepPending {
				return errors.New("execution evidence can only be recorded on an in_progress or completed checkpoint")
			}
			normalized, err := normalizeExecutionEvidence(step.ID, executionEvidence)
			if err != nil {
				return err
			}
			step.ExecutionEvidence = normalized
		}
		if step.Status == status && task.Summary == summary && diagnostic == nil && executionEvidence == nil && tddEvidence == nil && len(evidenceRecords) == 0 && len(unknownUpdates) == 0 {
			return nil
		}
		if !validStepTransition(step.Status, status) {
			return fmt.Errorf("cannot move step %s from %s to %s", step.ID, step.Status, status)
		}
		if status == StepInProgress || status == StepCompleted {
			if blocked := unsatisfiedStepDependencies(task.Steps, *step, nil); len(blocked) > 0 {
				return fmt.Errorf("step %s is blocked by incomplete dependencies: %s", step.ID, strings.Join(blocked, ", "))
			}
		}
		if status == StepCompleted && tddEvidence != nil {
			step.TDD = tddEvidence
		}
		if err := applyEvidenceSemantics(task, step, evidenceRecords, unknownUpdates, now); err != nil {
			return err
		}
		if status == StepCompleted {
			if step.ExecutionContract != nil {
				if err := step.ExecutionContract.ValidateCompletion(step.ExecutionEvidence); err != nil {
					return err
				}
			}
			if err := validateDiagnosticStepCompletion(task, step); err != nil {
				return err
			}
			if err := validateTraceableStepCompletion(step); err != nil {
				return err
			}
		}
		clearHandoff(task)
		step.Status = status
		step.UpdatedAt = now
		if task.FinalReview != nil && task.FinalReview.Status == FinalReviewFailed {
			task.FinalReview = nil
		}
		task.Phase = step.Phase
		task.Summary = summary
		appendTaskEvent(task, Event{Type: "checkpoint", Summary: step.ID + "=" + status + ": " + summary, CreatedAt: now})
		return nil
	})
}

func (s *Store) BatchCheckpoint(id string, completedStepIDs []string, currentStepID, summary string) (Task, error) {
	return s.mutate(id, func(task *Task, now time.Time) error {
		if err := requireActive(task); err != nil {
			return err
		}
		if err := requireFinalReviewOpen(task); err != nil {
			return err
		}

		completedStepIDs = normalizeStepIDs(completedStepIDs)
		currentStepID = strings.TrimSpace(currentStepID)
		summary = strings.TrimSpace(summary)
		if len(completedStepIDs) == 0 && currentStepID == "" {
			return errors.New("completed_step_ids or current_step_id is required")
		}
		if summary == "" {
			return errors.New("checkpoint summary is required")
		}
		if err := validateTextLimit("checkpoint summary", summary, maxTaskSummaryBytes); err != nil {
			return err
		}

		// 批量 checkpoint 必须先验证所有目标步骤，再统一写入，避免无效请求留下半更新状态。
		stepIndexes := make(map[string]int, len(task.Steps))
		for i := range task.Steps {
			stepIndexes[task.Steps[i].ID] = i
		}

		completedSet := make(map[string]struct{}, len(completedStepIDs))
		for _, stepID := range completedStepIDs {
			stepIndex, ok := stepIndexes[stepID]
			if !ok {
				return fmt.Errorf("task step %s not found", stepID)
			}
			if task.Steps[stepIndex].TDD != nil && task.Steps[stepIndex].Status != StepCompleted {
				return fmt.Errorf("step %s requires single-step tdd evidence checkpoint", stepID)
			}
			if task.Steps[stepIndex].ExecutionContract != nil && task.Steps[stepIndex].Status != StepCompleted {
				return fmt.Errorf("step %s requires single-step execution receipt checkpoint", stepID)
			}
			if !validStepTransition(task.Steps[stepIndex].Status, StepCompleted) {
				return fmt.Errorf("cannot move step %s from %s to %s", stepID, task.Steps[stepIndex].Status, StepCompleted)
			}
			completedSet[stepID] = struct{}{}
		}
		for _, stepID := range completedStepIDs {
			step := &task.Steps[stepIndexes[stepID]]
			if len(step.RequirementRefs) > 0 {
				return fmt.Errorf("traceable step %s must be completed through a single-step checkpoint with execution evidence", step.ID)
			}
			dependencyCompleting := completedSet
			if step.Quality != nil && isDiagnosticStepKind(step.Quality.Kind) {
				// Diagnostic stages are deliberately sequential: a later stage cannot be
				// completed in the same atomic batch that claims its prerequisite.
				dependencyCompleting = nil
			}
			if blocked := unsatisfiedStepDependencies(task.Steps, *step, dependencyCompleting); len(blocked) > 0 {
				return fmt.Errorf("step %s is blocked by incomplete dependencies: %s", step.ID, strings.Join(blocked, ", "))
			}
			if err := validateDiagnosticStepCompletion(task, step); err != nil {
				return err
			}
		}

		currentStepIndex := -1
		if currentStepID != "" {
			if _, overlaps := completedSet[currentStepID]; overlaps {
				return fmt.Errorf("step %s cannot be both completed and current", currentStepID)
			}
			stepIndex, ok := stepIndexes[currentStepID]
			if !ok {
				return fmt.Errorf("task step %s not found", currentStepID)
			}
			if !validStepTransition(task.Steps[stepIndex].Status, StepInProgress) {
				return fmt.Errorf("cannot move step %s from %s to %s", currentStepID, task.Steps[stepIndex].Status, StepInProgress)
			}
			dependencyCompleting := completedSet
			if task.Steps[stepIndex].Quality != nil && isDiagnosticStepKind(task.Steps[stepIndex].Quality.Kind) {
				dependencyCompleting = nil
			}
			if blocked := unsatisfiedStepDependencies(task.Steps, task.Steps[stepIndex], dependencyCompleting); len(blocked) > 0 {
				return fmt.Errorf("step %s is blocked by incomplete dependencies: %s", currentStepID, strings.Join(blocked, ", "))
			}
			currentStepIndex = stepIndex
		}

		clearHandoff(task)
		for _, stepID := range completedStepIDs {
			step := &task.Steps[stepIndexes[stepID]]
			step.Status = StepCompleted
			step.UpdatedAt = now
		}
		if currentStepIndex >= 0 {
			step := &task.Steps[currentStepIndex]
			step.Status = StepInProgress
			step.UpdatedAt = now
			task.Phase = step.Phase
		} else if len(completedStepIDs) > 0 {
			task.Phase = task.Steps[stepIndexes[completedStepIDs[len(completedStepIDs)-1]]].Phase
		}
		if task.FinalReview != nil && task.FinalReview.Status == FinalReviewFailed {
			task.FinalReview = nil
		}
		task.Summary = summary
		eventSummary := "completed=[" + strings.Join(completedStepIDs, ",") + "]"
		if currentStepID != "" {
			eventSummary += ", current=" + currentStepID
		}
		appendTaskEvent(task, Event{Type: "checkpoint", Summary: eventSummary + ": " + summary, CreatedAt: now})
		return nil
	})
}

func (s *Store) Block(id, summary string) (Task, error) {
	return s.mutate(id, func(task *Task, now time.Time) error {
		if err := requireMutable(task); err != nil {
			return err
		}
		if err := requireFinalReviewOpen(task); err != nil {
			return err
		}
		summary = strings.TrimSpace(summary)
		if summary == "" {
			return errors.New("block summary is required")
		}
		if err := validateTextLimit("block summary", summary, maxTaskSummaryBytes); err != nil {
			return err
		}
		clearHandoff(task)
		task.Status = StatusBlocked
		task.Blocker = summary
		task.Summary = summary
		appendTaskEvent(task, Event{Type: "blocked", Summary: summary, CreatedAt: now})
		return nil
	})
}

func (s *Store) Resume(id, summary string) (Task, error) {
	return s.mutate(id, func(task *Task, now time.Time) error {
		if task.Status != StatusBlocked {
			return errors.New("only blocked tasks can be resumed")
		}
		summary = strings.TrimSpace(summary)
		if summary == "" {
			return errors.New("resume summary is required")
		}
		if err := validateTextLimit("resume summary", summary, maxTaskSummaryBytes); err != nil {
			return err
		}
		clearHandoff(task)
		task.Status = StatusActive
		task.Blocker = ""
		task.Summary = summary
		appendTaskEvent(task, Event{Type: "resumed", Summary: summary, CreatedAt: now})
		return nil
	})
}

func (s *Store) FinalReview(id string, input FinalReviewInput) (Task, error) {
	return s.mutate(id, func(task *Task, now time.Time) error {
		return applyFinalReview(task, input, now)
	})
}

func (s *Store) Complete(id string) (Task, error) {
	return s.mutate(id, func(task *Task, now time.Time) error {
		if task.FinalReview == nil || task.FinalReview.Status != FinalReviewPass {
			return errors.New("final_review must pass before complete")
		}
		return completeTask(task, task.FinalReview.Summary, now, true)
	})
}

func applyFinalReview(task *Task, input FinalReviewInput, now time.Time) error {
	if err := requireActive(task); err != nil {
		return err
	}
	if err := requireFinalReviewOpen(task); err != nil {
		return err
	}
	status := strings.ToLower(strings.TrimSpace(input.Status))
	if status != FinalReviewPass && status != FinalReviewFailed {
		return errors.New("final review status must be pass or failed")
	}
	summary := strings.TrimSpace(input.Summary)
	if summary == "" {
		return errors.New("final review summary is required")
	}
	if err := validateTextLimit("final review summary", summary, maxTaskSummaryBytes); err != nil {
		return err
	}
	verifiedFacts := normalizeReviewItems(input.VerifiedFacts)
	openRisks := normalizeReviewItems(input.OpenRisks)
	missingChecks := normalizeReviewItems(input.MissingChecks)
	for label, items := range map[string][]string{"verified fact": verifiedFacts, "open risk": openRisks, "missing check": missingChecks} {
		if len(items) > maxTaskReviewItems {
			return fmt.Errorf("final review %s items cannot exceed %d", label, maxTaskReviewItems)
		}
		for _, item := range items {
			if err := validateTextLimit("final review "+label, item, maxTaskReviewItemBytes); err != nil {
				return err
			}
		}
	}
	intentSpec, err := normalizeOutcomeAxis("intent_spec", input.IntentSpec)
	if err != nil {
		return err
	}
	engineeringPolicy, err := normalizeOutcomeAxis("engineering_policy", input.EngineeringPolicy)
	if err != nil {
		return err
	}
	acknowledgedUnknownIDs, err := normalizeAcknowledgedUnknownIDs(*task, status, input.AcknowledgedUnknownIDs)
	if err != nil {
		return err
	}
	if intentSpec == nil || engineeringPolicy == nil {
		return errors.New("final review requires both intent_spec and engineering_policy outcome axes")
	}
	traceability := buildTraceabilityCoverage(*task)
	var provenance *OutcomeProvenance
	if status == FinalReviewPass {
		if intentSpec.Status != FinalReviewPass || engineeringPolicy.Status != FinalReviewPass {
			return errors.New("passing final review requires both outcome axes to pass")
		}
		if len(verifiedFacts) == 0 {
			return errors.New("passing final review requires at least one verified fact")
		}
		if pending := incompleteStepIDs(task.Steps); len(pending) > 0 {
			return fmt.Errorf("passing final review requires all task steps completed: %s", strings.Join(pending, ", "))
		}
		if err := requireCompleteTraceability(traceability); err != nil {
			return err
		}
		provenance, err = buildOutcomeProvenance(*task, verifiedFacts, input.VerifiedEvidence, intentSpec, engineeringPolicy)
		if err != nil {
			return err
		}
	} else {
		if intentSpec.Status != FinalReviewFailed && engineeringPolicy.Status != FinalReviewFailed {
			return errors.New("failed final review requires at least one failed outcome axis")
		}
		if len(openRisks) == 0 && len(missingChecks) == 0 {
			return errors.New("failed final review requires at least one risk")
		}
	}

	reviewRevision, err := newOpaqueID("rev_")
	if err != nil {
		return err
	}
	clearHandoff(task)
	task.FinalReview = &FinalReview{Status: status, Summary: summary, VerifiedFacts: verifiedFacts, AcknowledgedUnknownIDs: acknowledgedUnknownIDs, OpenRisks: openRisks, MissingChecks: missingChecks, IntentSpec: intentSpec, EngineeringPolicy: engineeringPolicy, Traceability: traceability, Provenance: provenance, ReviewRevision: reviewRevision, ReviewedAt: now}
	task.EvolutionCandidates = nil
	if status == FinalReviewPass {
		task.Phase = PhaseCloseout
	}
	task.Summary = summary
	appendTaskEvent(task, Event{Type: "final_review", Summary: status + ": " + summary, CreatedAt: now})
	return nil
}

func normalizeOutcomeAxis(label string, input *OutcomeAxis) (*OutcomeAxis, error) {
	if input == nil {
		return nil, nil
	}
	status := strings.ToLower(strings.TrimSpace(input.Status))
	if status != FinalReviewPass && status != FinalReviewFailed {
		return nil, fmt.Errorf("%s status must be pass or failed", label)
	}
	evidence := normalizeReviewItems(input.Evidence)
	findings := normalizeReviewItems(input.Findings)
	bindings := make([]ReviewEvidenceBinding, 0, len(input.EvidenceBindings))
	for i, binding := range input.EvidenceBindings {
		normalized, err := normalizeReviewEvidenceBinding(label, i, binding)
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, normalized)
	}
	for _, item := range append(append([]string{}, evidence...), findings...) {
		if err := validateTextLimit("final review "+label+" item", item, maxTaskReviewItemBytes); err != nil {
			return nil, err
		}
	}
	if status == FinalReviewPass && len(evidence) == 0 {
		return nil, fmt.Errorf("passing %s requires at least one evidence item", label)
	}
	if status == FinalReviewFailed && len(findings) == 0 {
		return nil, fmt.Errorf("failed %s requires at least one finding", label)
	}
	return &OutcomeAxis{Status: status, Evidence: evidence, EvidenceBindings: bindings, Findings: findings}, nil
}

func completeTask(task *Task, summary string, now time.Time, emitEvent bool) error {
	if err := requireActive(task); err != nil {
		return err
	}
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return errors.New("final verification summary is required")
	}
	if err := validateTextLimit("final verification summary", summary, maxTaskSummaryBytes); err != nil {
		return err
	}
	if task.Phase != PhaseCloseout {
		return errors.New("task must reach closeout before completion")
	}
	task.Status = StatusCompleted
	task.Summary = summary
	task.CompletedAt = &now
	if emitEvent {
		appendTaskEvent(task, Event{Type: "completed", Summary: summary, CreatedAt: now})
	}
	return nil
}

func (s *Store) mutate(id string, fn func(*Task, time.Time) error) (Task, error) {
	release, err := s.acquireStoreLock()
	if err != nil {
		return Task{}, err
	}
	defer release()
	task, err := s.loadLocked(id)
	if err != nil {
		return Task{}, err
	}
	now := time.Now().UTC()
	if err := fn(&task, now); err != nil {
		return Task{}, err
	}
	task.UpdatedAt = now
	if err := s.saveLocked(task); err != nil {
		return Task{}, err
	}
	return task, nil
}
