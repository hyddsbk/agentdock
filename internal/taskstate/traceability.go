package taskstate

import (
	"errors"
	"fmt"
	"strings"
)

const (
	maxTaskRequirementRefs       = 32
	maxStepRequirementRefs       = 12
	maxRequirementRefRunes       = 512
	maxExecutionEvidenceRefs     = 12
	maxExecutionEvidenceRefRunes = 512
)

type ExecutionEvidence struct {
	EvidenceRefs []string        `json:"evidence_refs,omitempty"`
	Attestations []*ExecutionRun `json:"attestations,omitempty"`
}

type RequirementCoverage struct {
	RequirementRef string   `json:"requirement_ref"`
	StepIDs        []string `json:"step_ids"`
	EvidenceRefs   []string `json:"evidence_refs"`
}

type TraceabilityCoverage struct {
	RequiredCount int                   `json:"required_count"`
	CoveredCount  int                   `json:"covered_count"`
	MissingRefs   []string              `json:"missing_refs,omitempty"`
	Items         []RequirementCoverage `json:"items"`
}

func normalizeRequirementRefs(label string, values []string, maxItems int) ([]string, error) {
	if len(values) > maxItems {
		return nil, fmt.Errorf("%s cannot exceed %d", label, maxItems)
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, ref := range values {
		trimmed := strings.TrimSpace(ref)
		if trimmed == "" || trimmed != ref || strings.ContainsAny(ref, "\r\n") || len([]rune(ref)) > maxRequirementRefRunes {
			return nil, fmt.Errorf("%s has invalid requirement ref %q", label, ref)
		}
		if _, exists := seen[ref]; exists {
			return nil, fmt.Errorf("%s has duplicate requirement ref %q", label, ref)
		}
		seen[ref] = struct{}{}
		out = append(out, ref)
	}
	return out, nil
}

func validateTaskTraceability(requirementRefs []string, steps []TaskStep) error {
	if len(requirementRefs) == 0 {
		for _, step := range steps {
			if len(step.RequirementRefs) > 0 {
				return fmt.Errorf("step %s has requirement_refs but task requirement_refs is empty", step.ID)
			}
		}
		return nil
	}

	known := make(map[string]struct{}, len(requirementRefs))
	mapped := make(map[string]bool, len(requirementRefs))
	for _, ref := range requirementRefs {
		known[ref] = struct{}{}
	}
	for _, step := range steps {
		for _, ref := range step.RequirementRefs {
			if _, ok := known[ref]; !ok {
				return fmt.Errorf("step %s references unknown requirement ref %q", step.ID, ref)
			}
			mapped[ref] = true
		}
	}
	for _, ref := range requirementRefs {
		if !mapped[ref] {
			return fmt.Errorf("requirement ref %q is not mapped to any step", ref)
		}
	}
	return nil
}

func normalizeExecutionEvidence(stepID string, input *ExecutionEvidence) (*ExecutionEvidence, error) {
	if input == nil {
		return nil, nil
	}
	if len(input.EvidenceRefs) == 0 && len(input.Attestations) == 0 {
		return nil, fmt.Errorf("step %s execution evidence requires at least one evidence ref or attestation", stepID)
	}
	if len(input.EvidenceRefs) > maxExecutionEvidenceRefs {
		return nil, fmt.Errorf("step %s execution evidence refs cannot exceed %d", stepID, maxExecutionEvidenceRefs)
	}
	seen := make(map[string]struct{}, len(input.EvidenceRefs))
	refs := make([]string, 0, len(input.EvidenceRefs))
	for _, ref := range input.EvidenceRefs {
		trimmed := strings.TrimSpace(ref)
		if trimmed == "" || trimmed != ref || strings.ContainsAny(ref, "\r\n") || len([]rune(ref)) > maxExecutionEvidenceRefRunes {
			return nil, fmt.Errorf("step %s has invalid execution evidence ref %q", stepID, ref)
		}
		if _, exists := seen[ref]; exists {
			return nil, fmt.Errorf("step %s has duplicate execution evidence ref %q", stepID, ref)
		}
		seen[ref] = struct{}{}
		refs = append(refs, ref)
	}
	attestations := make([]*ExecutionRun, 0, len(input.Attestations))
	attestedRefs := make(map[string]struct{}, len(input.Attestations))
	for _, run := range input.Attestations {
		if err := validateExecutionRunIntegrity(run); err != nil {
			return nil, fmt.Errorf("step %s has invalid execution attestation: %w", stepID, err)
		}
		if _, exists := seen[run.EvidenceRef]; !exists {
			return nil, fmt.Errorf("step %s execution attestation ref %q is not declared in evidence_refs", stepID, run.EvidenceRef)
		}
		if _, duplicate := attestedRefs[run.EvidenceRef]; duplicate {
			return nil, fmt.Errorf("step %s has duplicate execution attestation ref %q", stepID, run.EvidenceRef)
		}
		attestedRefs[run.EvidenceRef] = struct{}{}
		copyRun := *run
		attestations = append(attestations, &copyRun)
	}
	for _, ref := range refs {
		if !strings.HasPrefix(ref, "exec://") {
			continue
		}
		if _, ok := attestedRefs[ref]; !ok {
			return nil, fmt.Errorf("step %s generic execution evidence cannot use %q without a trusted executor attestation; use execution_receipts for exec:// evidence", stepID, ref)
		}
	}
	return &ExecutionEvidence{EvidenceRefs: refs, Attestations: attestations}, nil
}

func validateTraceableStepCompletion(step *TaskStep) error {
	if len(step.RequirementRefs) == 0 {
		return nil
	}
	if step.ExecutionEvidence == nil || len(step.ExecutionEvidence.EvidenceRefs) == 0 {
		return fmt.Errorf("traceable step %s requires execution evidence before completion", step.ID)
	}
	return nil
}

func buildTraceabilityCoverage(task Task) *TraceabilityCoverage {
	if len(task.RequirementRefs) == 0 {
		return nil
	}
	coverage := &TraceabilityCoverage{
		RequiredCount: len(task.RequirementRefs),
		Items:         make([]RequirementCoverage, 0, len(task.RequirementRefs)),
	}
	for _, ref := range task.RequirementRefs {
		item := RequirementCoverage{RequirementRef: ref}
		evidenceSeen := map[string]struct{}{}
		for _, step := range task.Steps {
			if !containsString(step.RequirementRefs, ref) {
				continue
			}
			item.StepIDs = append(item.StepIDs, step.ID)
			if step.Status != StepCompleted || step.ExecutionEvidence == nil {
				continue
			}
			for _, evidenceRef := range step.ExecutionEvidence.EvidenceRefs {
				if _, exists := evidenceSeen[evidenceRef]; exists {
					continue
				}
				evidenceSeen[evidenceRef] = struct{}{}
				item.EvidenceRefs = append(item.EvidenceRefs, evidenceRef)
			}
		}
		if len(item.StepIDs) > 0 && len(item.EvidenceRefs) > 0 {
			coverage.CoveredCount++
		} else {
			coverage.MissingRefs = append(coverage.MissingRefs, ref)
		}
		coverage.Items = append(coverage.Items, item)
	}
	return coverage
}

func requireCompleteTraceability(coverage *TraceabilityCoverage) error {
	if coverage == nil {
		return nil
	}
	if coverage.CoveredCount != coverage.RequiredCount || len(coverage.MissingRefs) > 0 {
		return errors.New("passing final review requires complete requirement traceability")
	}
	return nil
}
