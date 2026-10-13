package task

import (
	"encoding/json"
	"errors"

	"github.com/uvwt/agentdock/internal/taskstate"
)

func normalizeJSONToolValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			typed[key] = normalizeJSONToolValue(child)
		}
		return typed
	case []any:
		for i, child := range typed {
			typed[i] = normalizeJSONToolValue(child)
		}
		return typed
	case float64:
		if typed == float64(int(typed)) {
			return int(typed)
		}
		return typed
	default:
		return value
	}
}

func compactTaskSummary(task taskstate.Task) map[string]any {
	completedSteps := 0
	readyIDs := taskstate.ReadyStepIDs(task)
	readySet := make(map[string]struct{}, len(readyIDs))
	for _, id := range readyIDs {
		readySet[id] = struct{}{}
	}
	steps := make([]map[string]any, 0, len(task.Steps))
	var currentStep map[string]any
	for _, step := range task.Steps {
		if step.Status == taskstate.StepCompleted {
			completedSteps++
		}
		item := map[string]any{"id": step.ID, "title": truncateString(step.Title, 120), "status": step.Status}
		if len(step.DependsOn) > 0 {
			item["depends_on"] = append([]string(nil), step.DependsOn...)
		}
		if len(step.CompletionConditions) > 0 {
			item["completion_conditions"] = append([]string(nil), step.CompletionConditions...)
		}
		if len(step.ContextRefs) > 0 {
			item["context_refs"] = append([]string(nil), step.ContextRefs...)
		}
		if len(step.RequirementRefs) > 0 {
			item["requirement_refs"] = append([]string(nil), step.RequirementRefs...)
		}
		if step.ExecutionContract != nil {
			item["execution_contract"] = step.ExecutionContract
		}
		if step.ExecutionEvidence != nil {
			item["execution_evidence"] = step.ExecutionEvidence
		}
		if len(step.EvidenceRecords) > 0 {
			item["evidence_records"] = step.EvidenceRecords
		}
		if refs := taskstate.OutcomeEvidenceRefs(step); len(refs) > 0 {
			item["outcome_evidence_refs"] = refs
		}
		if step.Quality != nil {
			item["quality"] = step.Quality
		}
		if step.Diagnostic != nil {
			item["diagnostic"] = step.Diagnostic
		}
		steps = append(steps, item)
		if currentStep == nil && step.Status == taskstate.StepInProgress {
			currentStep = item
		}
	}
	if currentStep == nil {
		for _, item := range steps {
			id, _ := item["id"].(string)
			if _, ready := readySet[id]; ready {
				currentStep = item
				break
			}
		}
	}
	readyFrontier := make([]map[string]any, 0, len(readyIDs))
	for _, item := range steps {
		id, _ := item["id"].(string)
		if _, ready := readySet[id]; ready {
			readyFrontier = append(readyFrontier, item)
		}
	}
	conditionRefs := make([]map[string]any, 0, len(task.Conditions))
	for _, condition := range task.Conditions {
		conditionRefs = append(conditionRefs, map[string]any{"id": condition.ID, "text": truncateString(condition.Text, 160)})
	}
	summary := map[string]any{
		"id": task.ID, "title": task.Title, "status": task.Status, "phase": task.Phase,
		"completed_step_count": completedSteps, "step_count": len(task.Steps), "steps": steps,
		"ready_step_count": len(readyFrontier), "ready_frontier": readyFrontier,
		"condition_count": len(task.Conditions), "condition_refs": conditionRefs, "review_status": reviewStatus(task),
		"updated_at": task.UpdatedAt,
	}
	if len(task.Unknowns) > 0 {
		openUnknowns := compactOpenUnknowns(task)
		summary["unknown_count"] = len(task.Unknowns)
		summary["open_unknown_count"] = len(openUnknowns)
		if len(openUnknowns) > 0 {
			summary["open_unknowns"] = openUnknowns
		}
	}
	if currentStep != nil {
		summary["current_step"] = currentStep
	}
	if task.Summary != "" {
		summary["summary"] = truncateString(task.Summary, 240)
	}
	if len(task.RequirementRefs) > 0 {
		summary["requirement_refs"] = append([]string(nil), task.RequirementRefs...)
		summary["requirement_ref_count"] = len(task.RequirementRefs)
	}
	if len(task.SourceTemplates) > 0 {
		summary["source_templates"] = task.SourceTemplates
	}
	if task.Blocker != "" {
		summary["blocker"] = truncateString(task.Blocker, 240)
	}
	if task.Handoff != nil {
		summary["handoff"] = map[string]any{
			"next_step_id":      task.Handoff.NextStepID,
			"context_ref_count": len(task.Handoff.ContextRefs),
			"risk_count":        len(task.Handoff.Risks),
			"revision":          task.Handoff.Revision,
			"created_at":        task.Handoff.CreatedAt,
		}
	}
	if task.FinalReview != nil {
		review := map[string]any{
			"status": task.FinalReview.Status, "summary": truncateString(task.FinalReview.Summary, 200),
			"verified_count": len(task.FinalReview.VerifiedFacts), "risk_count": len(task.FinalReview.OpenRisks), "reviewed_at": task.FinalReview.ReviewedAt,
		}
		if task.FinalReview.IntentSpec != nil {
			review["intent_spec"] = task.FinalReview.IntentSpec
		}
		if task.FinalReview.EngineeringPolicy != nil {
			review["engineering_policy"] = task.FinalReview.EngineeringPolicy
		}
		if task.FinalReview.Traceability != nil {
			review["traceability"] = task.FinalReview.Traceability
		}
		if task.FinalReview.Provenance != nil {
			review["provenance"] = task.FinalReview.Provenance
		}
		if len(task.FinalReview.AcknowledgedUnknownIDs) > 0 {
			review["acknowledged_unknown_ids"] = append([]string(nil), task.FinalReview.AcknowledgedUnknownIDs...)
		}
		summary["final_review"] = review
	}
	if task.CompletedAt != nil {
		summary["completed_at"] = *task.CompletedAt
	}
	return summary
}

func compactResumeContext(task taskstate.Task) map[string]any {
	if task.Handoff == nil {
		return nil
	}

	readyIDs := taskstate.ReadyStepIDs(task)
	readySet := make(map[string]struct{}, len(readyIDs))
	for _, id := range readyIDs {
		readySet[id] = struct{}{}
	}

	var nextStep map[string]any
	readyFrontier := make([]map[string]any, 0, len(readyIDs))
	refs := append([]string(nil), task.Handoff.ContextRefs...)
	seenRefs := make(map[string]struct{}, len(refs)+8)
	for _, ref := range refs {
		seenRefs[ref] = struct{}{}
	}
	for _, step := range task.Steps {
		item := map[string]any{"id": step.ID, "title": truncateString(step.Title, 120), "status": step.Status}
		if len(step.DependsOn) > 0 {
			item["depends_on"] = append([]string(nil), step.DependsOn...)
		}
		if len(step.CompletionConditions) > 0 {
			item["completion_conditions"] = append([]string(nil), step.CompletionConditions...)
		}
		if len(step.ContextRefs) > 0 {
			item["context_refs"] = append([]string(nil), step.ContextRefs...)
		}
		if len(step.RequirementRefs) > 0 {
			item["requirement_refs"] = append([]string(nil), step.RequirementRefs...)
		}
		if step.ExecutionContract != nil {
			item["execution_contract"] = step.ExecutionContract
		}
		if step.ExecutionEvidence != nil {
			item["execution_evidence"] = step.ExecutionEvidence
		}
		if len(step.EvidenceRecords) > 0 {
			item["evidence_records"] = step.EvidenceRecords
		}
		if refs := taskstate.OutcomeEvidenceRefs(step); len(refs) > 0 {
			item["outcome_evidence_refs"] = refs
		}
		if step.Quality != nil {
			item["quality"] = step.Quality
		}
		if step.ID == task.Handoff.NextStepID {
			nextStep = item
			for _, ref := range step.ContextRefs {
				if _, exists := seenRefs[ref]; exists {
					continue
				}
				seenRefs[ref] = struct{}{}
				refs = append(refs, ref)
			}
		}
		if _, ready := readySet[step.ID]; ready {
			readyFrontier = append(readyFrontier, item)
		}
	}

	resume := map[string]any{
		"task_id":          task.ID,
		"title":            task.Title,
		"goal":             truncateString(task.Goal, 480),
		"status":           task.Status,
		"phase":            task.Phase,
		"summary":          truncateString(task.Summary, 480),
		"ready_frontier":   readyFrontier,
		"context_refs":     refs,
		"requirement_refs": append([]string(nil), task.RequirementRefs...),
		"risks":            append([]string(nil), task.Handoff.Risks...),
		"handoff_revision": task.Handoff.Revision,
		"created_at":       task.Handoff.CreatedAt,
	}
	if openUnknowns := compactOpenUnknowns(task); len(openUnknowns) > 0 {
		resume["open_unknowns"] = openUnknowns
	}
	if nextStep != nil {
		resume["next_step"] = nextStep
	}
	if task.Blocker != "" {
		resume["blocker"] = truncateString(task.Blocker, 480)
	}
	return resume
}

func compactTaskListItem(task taskstate.Task) map[string]any {
	summary := compactTaskSummary(task)
	item := map[string]any{
		"id": summary["id"], "title": summary["title"], "goal": task.Goal, "status": summary["status"], "phase": summary["phase"],
		"completed_step_count": summary["completed_step_count"], "step_count": summary["step_count"],
		"ready_step_count": summary["ready_step_count"], "review_status": summary["review_status"], "updated_at": summary["updated_at"],
	}
	for _, key := range []string{"current_step", "summary", "blocker", "handoff", "requirement_refs", "requirement_ref_count"} {
		if value, ok := summary[key]; ok {
			item[key] = value
		}
	}
	return item
}

func reviewStatus(task taskstate.Task) string {
	if task.FinalReview == nil {
		return "not_started"
	}
	return task.FinalReview.Status
}

func compactOpenUnknowns(task taskstate.Task) []map[string]any {
	out := make([]map[string]any, 0)
	for _, unknown := range task.Unknowns {
		if unknown.Status != taskstate.UnknownStatusOpen {
			continue
		}
		out = append(out, map[string]any{
			"id": unknown.ID, "question": truncateString(unknown.Question, 240),
			"status": unknown.Status, "revision": unknown.Revision, "blocking": unknown.Blocking,
			"opened_step_id": unknown.OpenedStepID,
		})
	}
	return out
}

func templateMatchRecommendation(candidates []taskstate.TemplateCandidate) map[string]any {
	bestScore := 0
	if len(candidates) > 0 {
		bestScore = candidates[0].Score
	}
	recommended := "plain_task"
	reason := "no active template is specific enough; create a plain recoverable task"
	if bestScore >= 85 {
		recommended = "use_template"
		reason = "top candidate score is strong enough to select by default"
	} else if bestScore >= 60 {
		recommended = "consider_template"
		reason = "top candidate is plausible but should be checked against the user goal"
	}
	return map[string]any{
		"recommended":           recommended,
		"recommendation_reason": reason,
		"best_candidate_score":  bestScore,
		"score_thresholds": map[string]any{
			"use_template":      85,
			"consider_template": 60,
			"plain_task_below":  60,
		},
	}
}
func compactTemplateSummary(template taskstate.Template) map[string]any {
	return map[string]any{
		"id":                   template.ID,
		"version":              template.Version,
		"title":                truncateString(template.Title, 120),
		"status":               template.Status,
		"keyword_count":        len(template.Match.Keywords),
		"device_count":         len(template.Match.Devices),
		"type":                 template.Match.Type,
		"condition_count":      len(template.CompletionConditions),
		"step_count":           len(template.Steps),
		"allow_long_template":  template.AllowLongTemplate,
		"long_template_reason": truncateString(template.LongTemplateReason, 160),
		"hash":                 template.Hash,
		"published_at":         template.PublishedAt,
		"retired_at":           template.RetiredAt,
	}
}

func taskToolError(err error) error {
	if errors.Is(err, taskstate.ErrTaskNotFound) {
		return toolErrorDetails("TASK_NOT_FOUND", err.Error(), "not_found", map[string]any{"retryable": false})
	}
	return toolErrorDetails("TASK_STATE_ERROR", err.Error(), "validation", map[string]any{"retryable": false})
}

func remarshal(input any, out any) error {
	data, err := json.Marshal(input)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}
