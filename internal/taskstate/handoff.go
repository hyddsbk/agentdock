package taskstate

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	maxHandoffContextRefs = 12
	maxHandoffRisks       = 16
	maxHandoffRiskBytes   = 2 << 10
)

func (s *Store) Handoff(id, nextStepID, summary string, contextRefs, risks []string) (Task, error) {
	return s.mutate(id, func(task *Task, now time.Time) error {
		if err := requireMutable(task); err != nil {
			return err
		}
		if err := requireFinalReviewOpen(task); err != nil {
			return err
		}

		summary = strings.TrimSpace(summary)
		if summary == "" {
			return errors.New("handoff summary is required")
		}
		if err := validateTextLimit("handoff summary", summary, maxTaskSummaryBytes); err != nil {
			return err
		}

		nextStepID = strings.TrimSpace(nextStepID)
		if len(task.Steps) == 0 {
			if nextStepID != "" {
				return errors.New("handoff next_step_id must be empty when the task has no steps")
			}
		} else {
			if nextStepID == "" {
				return errors.New("handoff next_step_id is required when the task has steps")
			}
			if err := validateHandoffNextStep(*task, nextStepID); err != nil {
				return err
			}
		}

		refs, err := normalizeHandoffContextRefs(contextRefs)
		if err != nil {
			return err
		}
		normalizedRisks, err := normalizeHandoffRisks(risks)
		if err != nil {
			return err
		}
		revision, err := newOpaqueID("hof_")
		if err != nil {
			return err
		}

		task.Summary = summary
		task.Handoff = &Handoff{
			NextStepID:  nextStepID,
			ContextRefs: refs,
			Risks:       normalizedRisks,
			Revision:    revision,
			CreatedAt:   now,
		}
		eventSummary := "revision=" + revision
		if nextStepID != "" {
			eventSummary += ", next=" + nextStepID
		}
		appendTaskEvent(task, Event{Type: "handoff", Summary: eventSummary + ": " + summary, CreatedAt: now})
		return nil
	})
}

func validateHandoffNextStep(task Task, nextStepID string) error {
	for _, step := range task.Steps {
		if step.ID != nextStepID {
			continue
		}
		switch step.Status {
		case StepInProgress:
			return nil
		case StepPending:
			if blocked := unsatisfiedStepDependencies(task.Steps, step, nil); len(blocked) > 0 {
				return fmt.Errorf("handoff next step %s is blocked by incomplete dependencies: %s", step.ID, strings.Join(blocked, ", "))
			}
			return nil
		case StepCompleted:
			return fmt.Errorf("handoff next step %s is already completed", step.ID)
		default:
			return fmt.Errorf("handoff next step %s has unsupported status %s", step.ID, step.Status)
		}
	}
	return fmt.Errorf("handoff next step %s not found", nextStepID)
}

func normalizeHandoffContextRefs(values []string) ([]string, error) {
	if len(values) > maxHandoffContextRefs {
		return nil, fmt.Errorf("handoff context_refs cannot exceed %d", maxHandoffContextRefs)
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || trimmed != value || strings.ContainsAny(value, "\r\n") || len([]rune(value)) > maxStepContextRefRunes {
			return nil, fmt.Errorf("handoff has invalid context ref %q", value)
		}
		if _, exists := seen[value]; exists {
			return nil, fmt.Errorf("handoff has duplicate context ref %q", value)
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out, nil
}

func normalizeHandoffRisks(values []string) ([]string, error) {
	if len(values) > maxHandoffRisks {
		return nil, fmt.Errorf("handoff risks cannot exceed %d", maxHandoffRisks)
	}
	normalized := normalizeReviewItems(values)
	if len(normalized) != len(values) {
		return nil, errors.New("handoff risks must be non-empty and distinct")
	}
	for _, value := range normalized {
		if err := validateTextLimit("handoff risk", value, maxHandoffRiskBytes); err != nil {
			return nil, err
		}
	}
	return normalized, nil
}

func clearHandoff(task *Task) {
	task.Handoff = nil
}
