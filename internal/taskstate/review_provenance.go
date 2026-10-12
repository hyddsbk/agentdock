package taskstate

import (
	"fmt"
	"strings"
)

const (
	maxReviewEvidenceBindings = 64
	maxReviewEvidenceRefs     = 16
	maxReviewEvidenceRefRunes = 2048
)

func buildOutcomeProvenance(task Task, verifiedFacts []string, verifiedEvidence []ReviewEvidenceBinding, intentSpec, engineeringPolicy *OutcomeAxis) (*OutcomeProvenance, error) {
	provenance := &OutcomeProvenance{Bindings: make([]OutcomeProvenanceBinding, 0, len(verifiedEvidence)+8)}
	stepSeen := map[string]struct{}{}
	requirementSeen := map[string]struct{}{}
	evidenceSeen := map[string]struct{}{}

	addClaims := func(scope string, claims []string, bindings []ReviewEvidenceBinding) error {
		canonical, err := validateReviewClaimBindings(task, scope, claims, bindings)
		if err != nil {
			return err
		}
		for _, binding := range canonical {
			provenance.Bindings = append(provenance.Bindings, OutcomeProvenanceBinding{
				Scope: scope, Claim: binding.Claim, StepID: binding.StepID,
				RequirementRefs: append([]string(nil), binding.RequirementRefs...),
				EvidenceRefs:    append([]string(nil), binding.EvidenceRefs...),
			})
			if _, ok := stepSeen[binding.StepID]; !ok {
				stepSeen[binding.StepID] = struct{}{}
				provenance.StepIDs = append(provenance.StepIDs, binding.StepID)
			}
			for _, ref := range binding.RequirementRefs {
				if _, ok := requirementSeen[ref]; ok {
					continue
				}
				requirementSeen[ref] = struct{}{}
				provenance.RequirementRefs = append(provenance.RequirementRefs, ref)
			}
			for _, ref := range binding.EvidenceRefs {
				if _, ok := evidenceSeen[ref]; ok {
					continue
				}
				evidenceSeen[ref] = struct{}{}
				provenance.EvidenceRefs = append(provenance.EvidenceRefs, ref)
			}
		}
		return nil
	}

	if err := addClaims("verified", verifiedFacts, verifiedEvidence); err != nil {
		return nil, err
	}
	if intentSpec == nil || engineeringPolicy == nil {
		return nil, fmt.Errorf("passing final review provenance requires both outcome axes")
	}
	if err := addClaims("intent_spec", intentSpec.Evidence, intentSpec.EvidenceBindings); err != nil {
		return nil, err
	}
	if err := addClaims("engineering_policy", engineeringPolicy.Evidence, engineeringPolicy.EvidenceBindings); err != nil {
		return nil, err
	}
	for _, requirementRef := range task.RequirementRefs {
		if _, ok := requirementSeen[requirementRef]; !ok {
			return nil, fmt.Errorf("passing final review provenance is missing requirement ref %q", requirementRef)
		}
	}
	return provenance, nil
}

func validateReviewClaimBindings(task Task, scope string, claims []string, bindings []ReviewEvidenceBinding) ([]ReviewEvidenceBinding, error) {
	if len(claims) == 0 {
		if len(bindings) > 0 {
			return nil, fmt.Errorf("%s has evidence bindings without review claims", scope)
		}
		return nil, nil
	}
	if len(bindings) == 0 {
		return nil, fmt.Errorf("passing %s requires at least one evidence binding", scope)
	}
	if len(bindings) > maxReviewEvidenceBindings {
		return nil, fmt.Errorf("%s evidence bindings cannot exceed %d", scope, maxReviewEvidenceBindings)
	}

	claimSet := make(map[string]struct{}, len(claims))
	boundClaims := make(map[string]bool, len(claims))
	for _, claim := range claims {
		claimSet[claim] = struct{}{}
	}
	stepByID := make(map[string]TaskStep, len(task.Steps))
	for _, step := range task.Steps {
		stepByID[step.ID] = step
	}
	taskRequirements := make(map[string]struct{}, len(task.RequirementRefs))
	for _, ref := range task.RequirementRefs {
		taskRequirements[ref] = struct{}{}
	}

	out := make([]ReviewEvidenceBinding, 0, len(bindings))
	for i, raw := range bindings {
		binding, err := normalizeReviewEvidenceBinding(scope, i, raw)
		if err != nil {
			return nil, err
		}
		if _, ok := claimSet[binding.Claim]; !ok {
			return nil, fmt.Errorf("%s evidence binding claim %q is not declared by the review", scope, binding.Claim)
		}
		step, ok := stepByID[binding.StepID]
		if !ok {
			return nil, fmt.Errorf("%s evidence binding references unknown step %s", scope, binding.StepID)
		}
		if step.Status != StepCompleted {
			return nil, fmt.Errorf("%s evidence binding step %s is not completed", scope, binding.StepID)
		}

		for _, ref := range binding.RequirementRefs {
			if _, ok := taskRequirements[ref]; !ok {
				return nil, fmt.Errorf("%s evidence binding references unknown requirement ref %q", scope, ref)
			}
			if !containsString(step.RequirementRefs, ref) {
				return nil, fmt.Errorf("%s evidence binding requirement ref %q is not owned by step %s", scope, ref, binding.StepID)
			}
		}

		ownedEvidence := stepOutcomeEvidenceRefs(step)
		owned := make(map[string]struct{}, len(ownedEvidence))
		for _, ref := range ownedEvidence {
			owned[ref] = struct{}{}
		}
		if len(ownedEvidence) > 0 && len(binding.EvidenceRefs) == 0 {
			return nil, fmt.Errorf("%s evidence binding for step %s must cite at least one owned evidence ref", scope, binding.StepID)
		}
		for _, ref := range binding.EvidenceRefs {
			if _, ok := owned[ref]; !ok {
				return nil, fmt.Errorf("%s evidence ref %q is not owned by step %s", scope, ref, binding.StepID)
			}
		}
		boundClaims[binding.Claim] = true
		out = append(out, binding)
	}
	for _, claim := range claims {
		if !boundClaims[claim] {
			return nil, fmt.Errorf("%s review claim %q has no evidence binding", scope, claim)
		}
	}
	return out, nil
}

func normalizeReviewEvidenceBinding(scope string, index int, input ReviewEvidenceBinding) (ReviewEvidenceBinding, error) {
	claim := strings.TrimSpace(input.Claim)
	if claim == "" || claim != input.Claim || strings.ContainsAny(claim, "\r\n") || len([]byte(claim)) > maxTaskReviewItemBytes {
		return ReviewEvidenceBinding{}, fmt.Errorf("%s evidence binding %d has invalid claim", scope, index)
	}
	stepID := strings.TrimSpace(input.StepID)
	if stepID == "" || stepID != input.StepID || !validStepID(stepID) {
		return ReviewEvidenceBinding{}, fmt.Errorf("%s evidence binding %d has invalid step_id", scope, index)
	}
	requirementRefs, err := normalizeRequirementRefs(scope+" evidence binding requirement_refs", input.RequirementRefs, maxStepRequirementRefs)
	if err != nil {
		return ReviewEvidenceBinding{}, err
	}
	evidenceRefs, err := normalizeReviewEvidenceRefs(scope, input.EvidenceRefs)
	if err != nil {
		return ReviewEvidenceBinding{}, err
	}
	return ReviewEvidenceBinding{
		Claim: claim, StepID: stepID,
		RequirementRefs: requirementRefs, EvidenceRefs: evidenceRefs,
	}, nil
}

func normalizeReviewEvidenceRefs(scope string, refs []string) ([]string, error) {
	if len(refs) > maxReviewEvidenceRefs {
		return nil, fmt.Errorf("%s evidence refs cannot exceed %d", scope, maxReviewEvidenceRefs)
	}
	seen := make(map[string]struct{}, len(refs))
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if strings.TrimSpace(ref) != ref || ref == "" || strings.ContainsAny(ref, "\r\n") || len([]rune(ref)) > maxReviewEvidenceRefRunes {
			return nil, fmt.Errorf("%s has invalid evidence ref %q", scope, ref)
		}
		if _, ok := seen[ref]; ok {
			return nil, fmt.Errorf("%s has duplicate evidence ref %q", scope, ref)
		}
		seen[ref] = struct{}{}
		out = append(out, ref)
	}
	return out, nil
}

// stepOutcomeEvidenceRefs is the server-owned evidence catalog for final review.
// Context refs are intentionally excluded: they orient work but do not prove outcome.
func stepOutcomeEvidenceRefs(step TaskStep) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, 12)
	add := func(ref string) {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			return
		}
		if _, ok := seen[ref]; ok {
			return
		}
		seen[ref] = struct{}{}
		out = append(out, ref)
	}
	if step.ExecutionEvidence != nil {
		for _, ref := range step.ExecutionEvidence.EvidenceRefs {
			add(ref)
		}
		for _, run := range step.ExecutionEvidence.Attestations {
			if run != nil {
				add(run.EvidenceRef)
			}
		}
	}
	if step.TDD != nil {
		for _, run := range []*TDDRun{step.TDD.Red, step.TDD.Green, step.TDD.Regression} {
			if run != nil {
				add(run.EvidenceRef)
			}
		}
	}
	if step.Diagnostic != nil {
		add(step.Diagnostic.ReproRef)
		for _, ref := range step.Diagnostic.EvidenceRefs {
			add(ref)
		}
		// Structured diagnostic state itself is persisted and validated by the Task
		// runtime, so expose a stable local reference even when no external artifact
		// was needed for a hypothesis or single-variable validation stage.
		add("diagnostic://" + step.ID)
	}
	return out
}

// OutcomeEvidenceRefs returns the stable evidence references a final-review
// binding may cite for this step.
func OutcomeEvidenceRefs(step TaskStep) []string {
	return stepOutcomeEvidenceRefs(step)
}
