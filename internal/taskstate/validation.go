package taskstate

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/textutil"
)

func normalizeTaskSteps(values []TaskStepInput, now time.Time) ([]TaskStep, error) {
	if len(values) > 12 {
		return nil, errors.New("task steps cannot exceed 12")
	}
	seen := make(map[string]struct{}, len(values))
	steps := make([]TaskStep, 0, len(values))
	for _, value := range values {
		id := strings.TrimSpace(value.ID)
		title := strings.TrimSpace(value.Title)
		if id == "" || title == "" {
			return nil, errors.New("each task step requires id and title")
		}
		if err := validateTextLimit("task step title", title, maxTaskStepTitleBytes); err != nil {
			return nil, err
		}
		if !validStepID(id) {
			return nil, fmt.Errorf("invalid task step id %q", id)
		}
		if _, exists := seen[id]; exists {
			return nil, fmt.Errorf("duplicate task step id %q", id)
		}
		phase := value.Phase
		if phase == "" {
			phase = PhaseExecute
		}
		if phase != PhaseCheck && phase != PhaseExecute && phase != PhaseVerify && phase != PhaseCloseout {
			return nil, fmt.Errorf("invalid task step phase %q", phase)
		}
		dependsOn, err := normalizeDependencyIDs(id, value.DependsOn)
		if err != nil {
			return nil, err
		}
		completionConditions, contextRefs, err := normalizeStepContract(id, value.CompletionConditions, value.ContextRefs)
		if err != nil {
			return nil, err
		}
		requirementRefs, err := normalizeRequirementRefs("task step "+id, value.RequirementRefs, maxStepRequirementRefs)
		if err != nil {
			return nil, err
		}
		quality, err := normalizeStepQuality(id, value.Quality)
		if err != nil {
			return nil, err
		}
		var seam *TDDSeam
		if value.TDDTestCommand != "" {
			command := strings.TrimSpace(value.TDDTestCommand)
			if command == "" || len(command) > 2048 {
				return nil, fmt.Errorf("invalid tdd test command for step %s", id)
			}
			seam = &TDDSeam{TestCommand: command, DeclaredAt: now}
		}
		executionContract, err := normalizeExecutionContract(id, value.ExecutionContract, now)
		if err != nil {
			return nil, err
		}
		if seam != nil && executionContract != nil {
			return nil, fmt.Errorf("step %s cannot declare both tdd_test_command and execution_contract", id)
		}
		seen[id] = struct{}{}
		steps = append(steps, TaskStep{TDD: seam, ExecutionContract: executionContract, ID: id, Title: title, Phase: phase, DependsOn: dependsOn, CompletionConditions: completionConditions, ContextRefs: contextRefs, RequirementRefs: requirementRefs, Quality: quality, Status: StepPending, UpdatedAt: now})
	}
	if err := validateTaskStepGraph(steps); err != nil {
		return nil, err
	}
	if err := validateTaskStepContracts(steps); err != nil {
		return nil, err
	}
	if err := validateTaskStepQuality(steps); err != nil {
		return nil, err
	}
	return steps, nil
}

const maxStepCompletionConditions = 4
const maxStepContextRefs = 8
const maxStepCompletionConditionRunes = 240
const maxStepContextRefRunes = 512

func normalizeStepContract(stepID string, conditions, refs []string) ([]string, []string, error) {
	if len(conditions) > maxStepCompletionConditions {
		return nil, nil, fmt.Errorf("task step %s has %d completion conditions; max %d", stepID, len(conditions), maxStepCompletionConditions)
	}
	normalizedConditions := normalizeTexts(conditions)
	if len(normalizedConditions) != len(conditions) {
		return nil, nil, fmt.Errorf("task step %s completion_conditions must be non-empty and distinct", stepID)
	}
	for _, condition := range conditions {
		if strings.TrimSpace(condition) != condition || len([]rune(condition)) > maxStepCompletionConditionRunes {
			return nil, nil, fmt.Errorf("task step %s has invalid completion condition", stepID)
		}
	}
	if len(refs) > maxStepContextRefs {
		return nil, nil, fmt.Errorf("task step %s has %d context refs; max %d", stepID, len(refs), maxStepContextRefs)
	}
	seenRefs := make(map[string]struct{}, len(refs))
	normalizedRefs := make([]string, 0, len(refs))
	for _, ref := range refs {
		trimmed := strings.TrimSpace(ref)
		if trimmed == "" || trimmed != ref || strings.ContainsAny(ref, "\r\n") || len([]rune(ref)) > maxStepContextRefRunes {
			return nil, nil, fmt.Errorf("task step %s has invalid context ref %q", stepID, ref)
		}
		if _, duplicate := seenRefs[ref]; duplicate {
			return nil, nil, fmt.Errorf("task step %s has duplicate context ref %q", stepID, ref)
		}
		seenRefs[ref] = struct{}{}
		normalizedRefs = append(normalizedRefs, ref)
	}
	return normalizedConditions, normalizedRefs, nil
}

func validateTaskStepContracts(steps []TaskStep) error {
	hasDAG := false
	for _, step := range steps {
		if len(step.DependsOn) > 0 {
			hasDAG = true
			break
		}
	}
	if hasDAG {
		for _, step := range steps {
			if len(step.CompletionConditions) == 0 {
				return fmt.Errorf("DAG task step %s requires at least one completion condition", step.ID)
			}
		}
	}
	return nil
}

const (
	StepKindDiscovery              = "discovery"
	StepKindVerticalSlice          = "vertical_slice"
	StepKindWideRefactorExpand     = "wide_refactor_expand"
	StepKindWideRefactorMigrate    = "wide_refactor_migrate"
	StepKindWideRefactorContract   = "wide_refactor_contract"
	StepKindVerification           = "verification"
	StepKindRelease                = "release"
	StepKindDiagnosticFeedbackLoop = "diagnostic_feedback_loop"
	StepKindDiagnosticReproduction = "diagnostic_reproduction"
	StepKindDiagnosticHypothesis   = "diagnostic_hypothesis"
	StepKindDiagnosticValidation   = "diagnostic_validation"
	StepKindDiagnosticRegression   = "diagnostic_regression"
	maxStepQualityOutcomeRunes     = 320
	maxDependencyReasonRunes       = 240
	maxRefactorGroupRunes          = 80
)

func normalizeStepQuality(stepID string, input *StepQuality) (*StepQuality, error) {
	if input == nil {
		return nil, nil
	}
	q := *input
	q.Kind = strings.TrimSpace(q.Kind)
	q.Outcome = strings.TrimSpace(q.Outcome)
	q.RefactorGroup = strings.TrimSpace(q.RefactorGroup)
	if !validStepKind(q.Kind) {
		return nil, fmt.Errorf("task step %s has invalid quality kind %q", stepID, q.Kind)
	}
	if q.Outcome == "" || q.Outcome != input.Outcome || strings.ContainsAny(q.Outcome, "\r\n") || len([]rune(q.Outcome)) > maxStepQualityOutcomeRunes {
		return nil, fmt.Errorf("task step %s has invalid quality outcome", stepID)
	}
	wide := isWideRefactorKind(q.Kind)
	if wide {
		if q.RefactorGroup == "" || q.RefactorGroup != input.RefactorGroup || len([]rune(q.RefactorGroup)) > maxRefactorGroupRunes || !validStepID(q.RefactorGroup) {
			return nil, fmt.Errorf("task step %s wide refactor quality requires a valid refactor_group", stepID)
		}
	} else if q.RefactorGroup != "" {
		return nil, fmt.Errorf("task step %s non-wide quality cannot set refactor_group", stepID)
	}
	if len(q.DependencyReasons) > 12 {
		return nil, fmt.Errorf("task step %s dependency_reasons cannot exceed 12", stepID)
	}
	if q.DependencyReasons != nil {
		reasons := make(map[string]string, len(q.DependencyReasons))
		for dependency, reason := range q.DependencyReasons {
			trimmedDependency := strings.TrimSpace(dependency)
			trimmedReason := strings.TrimSpace(reason)
			if trimmedDependency == "" || trimmedDependency != dependency || !validStepID(dependency) || trimmedReason == "" || trimmedReason != reason || strings.ContainsAny(reason, "\r\n") || len([]rune(reason)) > maxDependencyReasonRunes {
				return nil, fmt.Errorf("task step %s has invalid dependency reason for %q", stepID, dependency)
			}
			reasons[dependency] = reason
		}
		q.DependencyReasons = reasons
	}
	return &q, nil
}

func validStepKind(kind string) bool {
	switch kind {
	case StepKindDiscovery, StepKindVerticalSlice, StepKindWideRefactorExpand, StepKindWideRefactorMigrate, StepKindWideRefactorContract, StepKindVerification, StepKindRelease,
		StepKindDiagnosticFeedbackLoop, StepKindDiagnosticReproduction, StepKindDiagnosticHypothesis, StepKindDiagnosticValidation, StepKindDiagnosticRegression:
		return true
	default:
		return false
	}
}

func isWideRefactorKind(kind string) bool {
	return kind == StepKindWideRefactorExpand || kind == StepKindWideRefactorMigrate || kind == StepKindWideRefactorContract
}

func validateTaskStepQuality(steps []TaskStep) error {
	qualityEnabled := false
	for _, step := range steps {
		if step.Quality != nil {
			qualityEnabled = true
			break
		}
	}
	if !qualityEnabled {
		return nil
	}
	byID := make(map[string]TaskStep, len(steps))
	for _, step := range steps {
		byID[step.ID] = step
		if step.Quality == nil {
			return fmt.Errorf("step quality contract is partial; step %s is missing quality", step.ID)
		}
		if len(step.CompletionConditions) == 0 {
			return fmt.Errorf("quality-enabled step %s requires at least one completion condition", step.ID)
		}
		if len(step.Quality.DependencyReasons) != len(step.DependsOn) {
			return fmt.Errorf("quality-enabled step %s must explain every dependency edge exactly once", step.ID)
		}
		for _, dependency := range step.DependsOn {
			if _, ok := step.Quality.DependencyReasons[dependency]; !ok {
				return fmt.Errorf("quality-enabled step %s is missing dependency reason for %s", step.ID, dependency)
			}
		}
		for dependency := range step.Quality.DependencyReasons {
			if !containsString(step.DependsOn, dependency) {
				return fmt.Errorf("quality-enabled step %s explains non-dependency %s", step.ID, dependency)
			}
		}
	}
	if err := rejectRedundantDependencyEdges(steps); err != nil {
		return err
	}
	if err := validateWideRefactorGroups(steps, byID); err != nil {
		return err
	}
	return validateDiagnosticStepFlow(steps, byID)
}

func rejectRedundantDependencyEdges(steps []TaskStep) error {
	byID := make(map[string]TaskStep, len(steps))
	for _, step := range steps {
		byID[step.ID] = step
	}
	for _, step := range steps {
		for _, dependency := range step.DependsOn {
			for _, other := range step.DependsOn {
				if other == dependency {
					continue
				}
				if stepDependsTransitively(byID, other, dependency, map[string]bool{}) {
					return fmt.Errorf("step %s has redundant dependency %s; it is already implied by %s", step.ID, dependency, other)
				}
			}
		}
	}
	return nil
}

func stepDependsTransitively(byID map[string]TaskStep, from, target string, seen map[string]bool) bool {
	if from == target {
		return true
	}
	if seen[from] {
		return false
	}
	seen[from] = true
	for _, dependency := range byID[from].DependsOn {
		if stepDependsTransitively(byID, dependency, target, seen) {
			return true
		}
	}
	return false
}

func validateWideRefactorGroups(steps []TaskStep, byID map[string]TaskStep) error {
	type group struct{ expands, migrates, contracts []TaskStep }
	groups := map[string]*group{}
	for _, step := range steps {
		if !isWideRefactorKind(step.Quality.Kind) {
			continue
		}
		g := groups[step.Quality.RefactorGroup]
		if g == nil {
			g = &group{}
			groups[step.Quality.RefactorGroup] = g
		}
		switch step.Quality.Kind {
		case StepKindWideRefactorExpand:
			g.expands = append(g.expands, step)
		case StepKindWideRefactorMigrate:
			g.migrates = append(g.migrates, step)
		case StepKindWideRefactorContract:
			g.contracts = append(g.contracts, step)
		}
	}
	for name, g := range groups {
		if len(g.expands) == 0 || len(g.migrates) == 0 || len(g.contracts) != 1 {
			return fmt.Errorf("wide refactor group %s requires at least one expand, at least one migrate, and exactly one contract step", name)
		}
		for _, migrate := range g.migrates {
			downstream := false
			for _, expand := range g.expands {
				if stepDependsTransitively(byID, migrate.ID, expand.ID, map[string]bool{}) {
					downstream = true
					break
				}
			}
			if !downstream {
				return fmt.Errorf("wide refactor migrate step %s must depend on an expand step in group %s", migrate.ID, name)
			}
		}
		contract := g.contracts[0]
		for _, migrate := range g.migrates {
			if !containsString(contract.DependsOn, migrate.ID) {
				return fmt.Errorf("wide refactor contract step %s must directly depend on migrate step %s", contract.ID, migrate.ID)
			}
		}
	}
	return nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func normalizeDependencyIDs(stepID string, values []string) ([]string, error) {
	if len(values) > 12 {
		return nil, fmt.Errorf("task step %s dependencies cannot exceed 12", stepID)
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, raw := range values {
		dependency := strings.TrimSpace(raw)
		if dependency == "" || dependency != raw || !validStepID(dependency) {
			return nil, fmt.Errorf("task step %s has invalid dependency %q", stepID, raw)
		}
		if dependency == stepID {
			return nil, fmt.Errorf("task step %s cannot depend on itself", stepID)
		}
		if _, exists := seen[dependency]; exists {
			return nil, fmt.Errorf("task step %s has duplicate dependency %q", stepID, dependency)
		}
		seen[dependency] = struct{}{}
		out = append(out, dependency)
	}
	return out, nil
}

func validateTaskStepGraph(steps []TaskStep) error {
	byID := make(map[string]TaskStep, len(steps))
	for _, step := range steps {
		byID[step.ID] = step
	}
	for _, step := range steps {
		for _, dependency := range step.DependsOn {
			if _, exists := byID[dependency]; !exists {
				return fmt.Errorf("task step %s depends on unknown step %q", step.ID, dependency)
			}
		}
	}
	state := make(map[string]uint8, len(steps))
	var visit func(string) error
	visit = func(id string) error {
		switch state[id] {
		case 1:
			return fmt.Errorf("task dependency graph contains a cycle at step %q", id)
		case 2:
			return nil
		}
		state[id] = 1
		for _, dependency := range byID[id].DependsOn {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		state[id] = 2
		return nil
	}
	for _, step := range steps {
		if err := visit(step.ID); err != nil {
			return err
		}
	}
	return nil
}

// ReadyStepIDs returns pending steps whose dependency edges are all completed.
// A task-level external block suppresses the frontier without mutating step state.
func ReadyStepIDs(task Task) []string {
	if task.Status != StatusActive {
		return nil
	}
	ready := make([]string, 0, len(task.Steps))
	for _, step := range task.Steps {
		if step.Status != StepPending {
			continue
		}
		if len(unsatisfiedStepDependencies(task.Steps, step, nil)) == 0 {
			ready = append(ready, step.ID)
		}
	}
	return ready
}

func unsatisfiedStepDependencies(steps []TaskStep, step TaskStep, completing map[string]struct{}) []string {
	statusByID := make(map[string]string, len(steps))
	for _, candidate := range steps {
		statusByID[candidate.ID] = candidate.Status
	}
	blocked := make([]string, 0, len(step.DependsOn))
	for _, dependency := range step.DependsOn {
		if statusByID[dependency] == StepCompleted {
			continue
		}
		if completing != nil {
			if _, completesNow := completing[dependency]; completesNow {
				continue
			}
		}
		blocked = append(blocked, dependency)
	}
	return blocked
}

func normalizeTemplateReferences(values []TemplateReference) ([]TemplateReference, error) {
	if len(values) > 3 {
		return nil, errors.New("source templates cannot exceed 3")
	}
	seen := make(map[string]struct{}, len(values))
	refs := make([]TemplateReference, 0, len(values))
	for _, value := range values {
		value.ID = strings.TrimSpace(value.ID)
		value.Version = strings.TrimSpace(value.Version)
		value.Hash = strings.TrimSpace(value.Hash)
		if value.ID == "" || value.Version == "" {
			return nil, errors.New("source template id and version are required")
		}
		if _, exists := seen[value.ID]; exists {
			return nil, fmt.Errorf("duplicate source template %q", value.ID)
		}
		seen[value.ID] = struct{}{}
		refs = append(refs, value)
	}
	return refs, nil
}

func validStepID(id string) bool {
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func validStepTransition(from, to string) bool {
	if from == to {
		return true
	}
	switch from {
	case StepPending:
		return to == StepInProgress || to == StepCompleted
	case StepInProgress:
		return to == StepCompleted
	default:
		return false
	}
}

func incompleteStepIDs(steps []TaskStep) []string {
	ids := make([]string, 0)
	for _, step := range steps {
		if step.Status != StepCompleted {
			ids = append(ids, step.ID)
		}
	}
	return ids
}

func normalizeStepIDs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func normalizeTexts(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		duplicate := false
		for _, existing := range out {
			if similarConditionText(existing, value) {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func similarConditionText(a, b string) bool {
	a = conditionCompareText(a)
	b = conditionCompareText(b)
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	aRuneLen := len([]rune(a))
	bRuneLen := len([]rune(b))
	if aRuneLen >= 8 && bRuneLen >= 8 && (strings.Contains(a, b) || strings.Contains(b, a)) {
		return true
	}
	if aRuneLen < 8 || bRuneLen < 8 {
		return false
	}
	aPairs := conditionBigramSet(a)
	bPairs := conditionBigramSet(b)
	if len(aPairs) == 0 || len(bPairs) == 0 {
		return false
	}
	shared := 0
	for pair := range aPairs {
		if _, ok := bPairs[pair]; ok {
			shared++
		}
	}
	return float64(shared*2)/float64(len(aPairs)+len(bPairs)) >= 0.62
}

func conditionCompareText(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		switch r {
		case '，', '。', '！', '？', '、', '；', '：', '（', '）', '(', ')', '[', ']', '【', '】', '{', '}', '《', '》', '“', '”', '‘', '’', '"', '\'', '`':
			continue
		}
		if r > ' ' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func conditionBigramSet(value string) map[string]struct{} {
	runes := []rune(value)
	out := map[string]struct{}{}
	if len(runes) < 2 {
		if value != "" {
			out[value] = struct{}{}
		}
		return out
	}
	for i := 0; i < len(runes)-1; i++ {
		out[string(runes[i:i+2])] = struct{}{}
	}
	return out
}

func requireMutable(task *Task) error {
	if task.Status == StatusCompleted {
		return errors.New("completed tasks are immutable")
	}
	return nil
}

func requireActive(task *Task) error {
	if task.Status != StatusActive {
		return fmt.Errorf("task status must be active, got %s", task.Status)
	}
	return nil
}

func requireFinalReviewOpen(task *Task) error {
	if task.FinalReview != nil && task.FinalReview.Status == FinalReviewPass {
		return errors.New("task already passed final review")
	}
	return nil
}

func normalizeReviewItems(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func appendTaskEvent(task *Task, event Event) {
	event.Summary = textutil.SafeTruncateString(strings.TrimSpace(event.Summary), maxTaskEventSummaryBytes).Text
	task.Events = append(task.Events, event)
	if len(task.Events) > maxTaskEvents {
		task.Events = append([]Event(nil), task.Events[len(task.Events)-maxTaskEvents:]...)
	}
}

func validateTextLimit(label, value string, maxBytes int) error {
	if len([]byte(value)) > maxBytes {
		return fmt.Errorf("%s exceeds %d bytes", label, maxBytes)
	}
	return nil
}

func validateID(id string) error {
	if !strings.HasPrefix(id, "tsk_") || len(id) != 20 {
		return errors.New("invalid task id")
	}
	for _, ch := range id[4:] {
		if !strings.ContainsRune("0123456789abcdef", ch) {
			return errors.New("invalid task id")
		}
	}
	return nil
}

func newID() (string, error) {
	return newOpaqueID("tsk_")
}

func newOpaqueID(prefix string) (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate %sid: %w", prefix, err)
	}
	return prefix + hex.EncodeToString(raw), nil
}
