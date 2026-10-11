package taskstate

import (
	"errors"
	"fmt"
	"strings"
)

const (
	DiagnosticObservedRed   = "red"
	DiagnosticObservedGreen = "green"

	DiagnosticOutcomeSupported = "supported"
	DiagnosticOutcomeRefuted   = "refuted"

	maxDiagnosticFieldRunes = 2048
)

func isDiagnosticStepKind(kind string) bool {
	switch kind {
	case StepKindDiagnosticFeedbackLoop, StepKindDiagnosticReproduction, StepKindDiagnosticHypothesis, StepKindDiagnosticValidation, StepKindDiagnosticRegression:
		return true
	default:
		return false
	}
}

func validateDiagnosticStepFlow(steps []TaskStep, byID map[string]TaskStep) error {
	var feedbackLoops, reproductions, hypotheses, validations, regressions []TaskStep
	for _, step := range steps {
		if step.Quality == nil || !isDiagnosticStepKind(step.Quality.Kind) {
			continue
		}
		switch step.Quality.Kind {
		case StepKindDiagnosticFeedbackLoop:
			feedbackLoops = append(feedbackLoops, step)
		case StepKindDiagnosticReproduction:
			reproductions = append(reproductions, step)
		case StepKindDiagnosticHypothesis:
			hypotheses = append(hypotheses, step)
		case StepKindDiagnosticValidation:
			validations = append(validations, step)
		case StepKindDiagnosticRegression:
			regressions = append(regressions, step)
		}
	}
	total := len(feedbackLoops) + len(reproductions) + len(hypotheses) + len(validations) + len(regressions)
	if total == 0 {
		return nil
	}
	if len(feedbackLoops) != 1 || len(reproductions) != 1 || len(regressions) != 1 || len(hypotheses) == 0 || len(validations) == 0 {
		return errors.New("diagnostic feedback loop requires exactly one feedback_loop, one reproduction, one regression, and at least one hypothesis and validation step")
	}

	feedback := feedbackLoops[0]
	repro := reproductions[0]
	regression := regressions[0]
	if !stepDependsTransitively(byID, repro.ID, feedback.ID, map[string]bool{}) {
		return fmt.Errorf("diagnostic reproduction step %s must depend on feedback loop %s", repro.ID, feedback.ID)
	}

	validationCountByHypothesis := make(map[string]int, len(hypotheses))
	for _, hypothesis := range hypotheses {
		if !stepDependsTransitively(byID, hypothesis.ID, repro.ID, map[string]bool{}) {
			return fmt.Errorf("diagnostic hypothesis step %s must depend on reproduction %s", hypothesis.ID, repro.ID)
		}
	}
	for _, validation := range validations {
		ancestorHypotheses := make([]string, 0, 2)
		for _, hypothesis := range hypotheses {
			if stepDependsTransitively(byID, validation.ID, hypothesis.ID, map[string]bool{}) {
				ancestorHypotheses = append(ancestorHypotheses, hypothesis.ID)
			}
		}
		if len(ancestorHypotheses) != 1 {
			return fmt.Errorf("diagnostic validation step %s must depend on exactly one falsifiable hypothesis; found %d", validation.ID, len(ancestorHypotheses))
		}
		validationCountByHypothesis[ancestorHypotheses[0]]++
		if !stepDependsTransitively(byID, regression.ID, validation.ID, map[string]bool{}) {
			return fmt.Errorf("diagnostic regression step %s must depend on validation %s", regression.ID, validation.ID)
		}
	}
	for _, hypothesis := range hypotheses {
		if validationCountByHypothesis[hypothesis.ID] == 0 {
			return fmt.Errorf("diagnostic hypothesis step %s requires at least one downstream single-variable validation", hypothesis.ID)
		}
	}
	return nil
}

func normalizeDiagnosticEvidence(stepID, kind string, input *DiagnosticEvidence) (*DiagnosticEvidence, error) {
	if input == nil {
		return nil, nil
	}
	if !isDiagnosticStepKind(kind) {
		return nil, fmt.Errorf("task step %s is not diagnostic and cannot accept diagnostic evidence", stepID)
	}
	out := *input
	fields := []struct {
		name string
		raw  string
		dst  *string
	}{
		{"command", input.Command, &out.Command},
		{"observed", input.Observed, &out.Observed},
		{"symptom", input.Symptom, &out.Symptom},
		{"repro_ref", input.ReproRef, &out.ReproRef},
		{"hypothesis", input.Hypothesis, &out.Hypothesis},
		{"falsifier", input.Falsifier, &out.Falsifier},
		{"variable", input.Variable, &out.Variable},
		{"method", input.Method, &out.Method},
		{"outcome", input.Outcome, &out.Outcome},
	}
	for _, field := range fields {
		value := strings.TrimSpace(field.raw)
		if value != field.raw || strings.ContainsAny(field.raw, "\r\n") || len([]rune(field.raw)) > maxDiagnosticFieldRunes {
			return nil, fmt.Errorf("task step %s has invalid diagnostic %s", stepID, field.name)
		}
		*field.dst = value
	}
	if out.Observed != "" && out.Observed != DiagnosticObservedRed && out.Observed != DiagnosticObservedGreen {
		return nil, fmt.Errorf("task step %s diagnostic observed must be red or green", stepID)
	}
	if out.Outcome != "" && out.Outcome != DiagnosticOutcomeSupported && out.Outcome != DiagnosticOutcomeRefuted {
		return nil, fmt.Errorf("task step %s diagnostic outcome must be supported or refuted", stepID)
	}
	refs, err := normalizeDiagnosticRefs(stepID, input.EvidenceRefs)
	if err != nil {
		return nil, err
	}
	out.EvidenceRefs = refs
	if err := rejectDiagnosticFieldsForKind(stepID, kind, out); err != nil {
		return nil, err
	}
	return &out, nil
}

func normalizeDiagnosticRefs(stepID string, refs []string) ([]string, error) {
	if len(refs) > maxStepContextRefs {
		return nil, fmt.Errorf("task step %s diagnostic evidence refs cannot exceed %d", stepID, maxStepContextRefs)
	}
	seen := make(map[string]struct{}, len(refs))
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if strings.TrimSpace(ref) != ref || ref == "" || strings.ContainsAny(ref, "\r\n") || len([]rune(ref)) > maxStepContextRefRunes {
			return nil, fmt.Errorf("task step %s has invalid diagnostic evidence ref %q", stepID, ref)
		}
		if _, exists := seen[ref]; exists {
			return nil, fmt.Errorf("task step %s has duplicate diagnostic evidence ref %q", stepID, ref)
		}
		seen[ref] = struct{}{}
		out = append(out, ref)
	}
	return out, nil
}

func rejectDiagnosticFieldsForKind(stepID, kind string, evidence DiagnosticEvidence) error {
	nonEmpty := func(values ...string) bool {
		for _, value := range values {
			if value != "" {
				return true
			}
		}
		return false
	}
	switch kind {
	case StepKindDiagnosticFeedbackLoop:
		if nonEmpty(evidence.ReproRef, evidence.Hypothesis, evidence.Falsifier, evidence.Variable, evidence.Method, evidence.Outcome) {
			return fmt.Errorf("diagnostic feedback loop step %s contains fields owned by a later diagnostic stage", stepID)
		}
	case StepKindDiagnosticReproduction:
		if nonEmpty(evidence.Symptom, evidence.Hypothesis, evidence.Falsifier, evidence.Variable, evidence.Method, evidence.Outcome) {
			return fmt.Errorf("diagnostic reproduction step %s contains fields owned by another diagnostic stage", stepID)
		}
	case StepKindDiagnosticHypothesis:
		if nonEmpty(evidence.Command, evidence.Observed, evidence.Symptom, evidence.ReproRef, evidence.Variable, evidence.Method, evidence.Outcome) {
			return fmt.Errorf("diagnostic hypothesis step %s contains fields owned by another diagnostic stage", stepID)
		}
	case StepKindDiagnosticValidation:
		if nonEmpty(evidence.Command, evidence.Observed, evidence.Symptom, evidence.ReproRef, evidence.Hypothesis, evidence.Falsifier) {
			return fmt.Errorf("diagnostic validation step %s contains fields owned by another diagnostic stage", stepID)
		}
	case StepKindDiagnosticRegression:
		if nonEmpty(evidence.Symptom, evidence.ReproRef, evidence.Hypothesis, evidence.Falsifier, evidence.Variable, evidence.Method, evidence.Outcome) {
			return fmt.Errorf("diagnostic regression step %s contains fields owned by another diagnostic stage", stepID)
		}
	}
	return nil
}

func validateDiagnosticStepCompletion(task *Task, step *TaskStep) error {
	if step.Quality == nil || !isDiagnosticStepKind(step.Quality.Kind) {
		return nil
	}
	if step.Diagnostic == nil {
		return fmt.Errorf("diagnostic step %s requires structured diagnostic evidence before completion", step.ID)
	}
	evidence := step.Diagnostic
	switch step.Quality.Kind {
	case StepKindDiagnosticFeedbackLoop:
		if evidence.Command == "" || evidence.Observed != DiagnosticObservedRed || evidence.Symptom == "" {
			return fmt.Errorf("diagnostic feedback loop step %s requires command, observed=red, and symptom", step.ID)
		}
	case StepKindDiagnosticReproduction:
		if evidence.Command == "" || evidence.Observed != DiagnosticObservedRed || evidence.ReproRef == "" {
			return fmt.Errorf("diagnostic reproduction step %s requires command, observed=red, and repro_ref", step.ID)
		}
	case StepKindDiagnosticHypothesis:
		if evidence.Hypothesis == "" || evidence.Falsifier == "" {
			return fmt.Errorf("diagnostic hypothesis step %s requires hypothesis and falsifier", step.ID)
		}
	case StepKindDiagnosticValidation:
		if evidence.Variable == "" || evidence.Method == "" || (evidence.Outcome != DiagnosticOutcomeSupported && evidence.Outcome != DiagnosticOutcomeRefuted) {
			return fmt.Errorf("diagnostic validation step %s requires one variable, method, and supported/refuted outcome", step.ID)
		}
	case StepKindDiagnosticRegression:
		if evidence.Command == "" || evidence.Observed != DiagnosticObservedGreen {
			return fmt.Errorf("diagnostic regression step %s requires command and observed=green", step.ID)
		}
		repro := diagnosticStepByKind(task.Steps, StepKindDiagnosticReproduction)
		if repro == nil || repro.Diagnostic == nil || repro.Diagnostic.Command == "" {
			return fmt.Errorf("diagnostic regression step %s requires a completed reproduction command", step.ID)
		}
		if evidence.Command != repro.Diagnostic.Command {
			return fmt.Errorf("diagnostic regression step %s must rerun the minimal reproduction command", step.ID)
		}
		supported := false
		for i := range task.Steps {
			candidate := &task.Steps[i]
			if candidate.Quality != nil && candidate.Quality.Kind == StepKindDiagnosticValidation && candidate.Diagnostic != nil && candidate.Diagnostic.Outcome == DiagnosticOutcomeSupported {
				supported = true
				break
			}
		}
		if !supported {
			return fmt.Errorf("diagnostic regression step %s requires at least one supported single-variable validation", step.ID)
		}
	}
	return nil
}

func diagnosticStepByKind(steps []TaskStep, kind string) *TaskStep {
	for i := range steps {
		if steps[i].Quality != nil && steps[i].Quality.Kind == kind {
			return &steps[i]
		}
	}
	return nil
}
