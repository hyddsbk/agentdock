package task

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/uvwt/agentdock/internal/taskstate"
)

func (s *Service) WorkflowManage(ctx context.Context, request WorkflowRequest) (Result, error) {
	input := normalizeWorkflowRequest(request)
	switch input.Action {
	case "publish":
		request := workflowTemplatePublishRequest{Template: input.Template}
		return compactNexusTemplateMutationResult(s.nexusWorkflowJSON(ctx, "POST", "/v1/workflow-templates/publish", request))
	case "retire":
		if input.TemplateID == "" || input.TemplateVersion == "" {
			return nil, toolErrorDetails("VALIDATION_ERROR", "template_id and template_version are required", "validation", map[string]any{
				"action": input.Action,
			})
		}
		return compactNexusTemplateMutationResult(s.nexusWorkflowJSON(ctx, "POST", input.escapedTemplatePath("retire"), struct{}{}))
	case "get":
		if input.TemplateID == "" {
			return nil, toolErrorDetails("VALIDATION_ERROR", "template_id is required", "validation", nil)
		}
		if input.TemplateVersion == "" {
			version, err := s.nexusActiveWorkflowTemplateVersion(ctx, input.TemplateID)
			if err != nil {
				return nil, err
			}
			input.TemplateVersion = version
		}
		result, err := s.nexusWorkflowJSON(ctx, "GET", input.escapedTemplatePath(""), nil)
		if err != nil {
			return nil, err
		}
		var template taskstate.Template
		if err := remarshal(result["template"], &template); err == nil {
			result["template"] = template
		}
		return result, nil
	case "get_many":
		templates, err := s.nexusActiveWorkflowTemplates(ctx, input.TemplateIDs)
		if err != nil {
			return nil, err
		}
		return Result{
			"action": input.Action, "templates": templates, "count": len(templates), "composition_required": true,
			"next_required_action": "Combine these templates for the frozen Admission: prune irrelevant steps, deduplicate them, preserve or rebuild valid depends_on edges, and preserve quality metadata. Compose only step-level completion conditions; top-level task acceptance remains the Admission success_conditions. Then call task_manage create with admission_ref, source_template_ids, and composed steps.",
		}, nil
	case "list":
		path := "/v1/workflow-templates"
		if input.TemplateStatus != "" {
			path += "?status=" + url.QueryEscape(input.TemplateStatus)
		}
		result, err := s.nexusWorkflowJSON(ctx, "GET", path, nil)
		if err != nil {
			return nil, err
		}
		if items, ok := result["items"]; ok {
			var summaries []map[string]any
			if err := remarshal(items, &summaries); err == nil {
				for i := range summaries {
					for key, value := range summaries[i] {
						summaries[i][key] = normalizeJSONToolValue(value)
					}
				}
				result["templates"] = summaries
			} else {
				result["templates"] = items
			}
		}
		return result, nil
	case "match":
		if input.AdmissionRef == "" {
			return nil, toolErrorDetails("VALIDATION_ERROR", "admission_ref is required for workflow matching", "validation", map[string]any{"field": "admission_ref"})
		}
		return s.matchWorkflowTemplates(ctx, input)
	case "vector_index":
		result, err := s.nexusWorkflowJSON(ctx, "GET", "/v1/workflow-templates/vector-index", nil)
		if err != nil {
			return nil, err
		}
		result["action"] = input.Action
		if available, exists := result["available"]; exists {
			result["vector_index_available"] = available
			delete(result, "available")
		}
		return result, nil
	default:
		return nil, toolErrorDetails("INVALID_ACTION", "unsupported workflow_template_manage action", "validation", map[string]any{"action": input.Action, "allowed": workflowTemplateActions})
	}
}

func compactNexusTemplateMutationResult(result Result, err error) (Result, error) {
	if err != nil {
		return nil, err
	}
	delete(result, "template")
	return result, nil
}

func (s *Service) matchWorkflowTemplates(ctx context.Context, input workflowTemplateInput) (Result, error) {
	admission, err := s.tasks.GetAdmission(input.AdmissionRef)
	if err != nil {
		return nil, taskToolError(err)
	}
	if admission.Status != taskstate.AdmissionPass {
		return nil, toolErrorDetails("ADMISSION_NOT_PASS", "workflow matching requires a passing admission", "validation", map[string]any{"admission_ref": admission.ID, "status": admission.Status})
	}
	if strings.TrimSpace(input.Goal) != "" && strings.TrimSpace(input.Goal) != admission.Contract.Goal {
		return nil, toolErrorDetails("ADMISSION_MISMATCH", "workflow match goal cannot differ from frozen admission goal", "validation", map[string]any{"admission_ref": admission.ID})
	}
	if strings.TrimSpace(input.Device) != "" && strings.TrimSpace(input.Device) != admission.Contract.Device {
		return nil, toolErrorDetails("ADMISSION_MISMATCH", "workflow match device cannot differ from frozen admission device", "validation", map[string]any{"admission_ref": admission.ID})
	}
	if strings.TrimSpace(input.Type) != "" && strings.TrimSpace(input.Type) != admission.Contract.WorkflowType {
		return nil, toolErrorDetails("ADMISSION_MISMATCH", "workflow match type cannot differ from frozen admission workflow_type", "validation", map[string]any{"admission_ref": admission.ID})
	}
	request := workflowTemplateMatchRequest{Goal: admission.Contract.Goal, Device: admission.Contract.Device, Type: admission.Contract.WorkflowType}
	result, err := s.nexusWorkflowJSON(ctx, "POST", "/v1/workflow-templates/match", request)
	if err != nil {
		return nil, err
	}
	var candidates []taskstate.TemplateCandidate
	if raw := result["candidates"]; raw != nil {
		_ = remarshal(raw, &candidates)
	}
	candidateIDs := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidateIDs = append(candidateIDs, candidate.ID)
	}
	receipt, err := s.tasks.RecordAdmissionWorkflowMatch(admission.ID, taskstate.WorkflowMatchReceiptInput{
		Type: admission.Contract.WorkflowType, Recommended: fmt.Sprint(result["recommended"]), CandidateIDs: candidateIDs,
	})
	if err != nil {
		return nil, taskToolError(err)
	}
	result["admission_ref"] = admission.ID
	result["admission_digest"] = admission.Digest
	result["workflow_match_digest"] = receipt.Digest
	result["next_required_action"] = "Create the task with task_manage create using this admission_ref. Goal, project, device, and acceptance conditions are derived from the frozen admission."
	return result, nil
}

func (s *Service) nexusActiveWorkflowTemplates(ctx context.Context, ids []string) ([]taskstate.Template, error) {
	ids = normalizeTemplateIDs(ids)
	if len(ids) < 2 || len(ids) > 3 {
		return nil, taskToolError(fmt.Errorf("template_ids must contain 2 or 3 distinct ids"))
	}
	templates := make([]taskstate.Template, 0, len(ids))
	for _, id := range ids {
		template, err := s.nexusActiveWorkflowTemplate(ctx, id)
		if err != nil {
			return nil, err
		}
		templates = append(templates, template)
	}
	return templates, nil
}

func (s *Service) nexusActiveWorkflowTemplate(ctx context.Context, id string) (taskstate.Template, error) {
	version, err := s.nexusActiveWorkflowTemplateVersion(ctx, id)
	if err != nil {
		return taskstate.Template{}, err
	}
	return s.nexusWorkflowTemplate(ctx, strings.TrimSpace(id), version)
}

func (s *Service) nexusActiveWorkflowTemplateVersion(ctx context.Context, id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", taskToolError(fmt.Errorf("template_id is required"))
	}
	result, err := s.nexusWorkflowJSON(ctx, "GET", "/v1/workflow-templates?status=active", nil)
	if err != nil {
		return "", err
	}
	raw := result["templates"]
	if raw == nil {
		raw = result["items"]
	}
	var summaries []struct {
		ID      string `json:"id"`
		Version string `json:"version"`
	}
	if err := remarshal(raw, &summaries); err != nil {
		return "", taskToolError(fmt.Errorf("decode active workflow templates: %w", err))
	}
	for _, summary := range summaries {
		if summary.ID == id {
			return summary.Version, nil
		}
	}
	return "", taskToolError(fmt.Errorf("active workflow template %s not found", id))
}

func (s *Service) nexusWorkflowTemplate(ctx context.Context, id, version string) (taskstate.Template, error) {
	result, err := s.nexusWorkflowJSON(ctx, "GET", fmt.Sprintf("/v1/workflow-templates/%s/%s", url.PathEscape(id), url.PathEscape(version)), nil)
	if err != nil {
		return taskstate.Template{}, err
	}
	var template taskstate.Template
	if err := remarshal(result["template"], &template); err != nil {
		return taskstate.Template{}, taskToolError(fmt.Errorf("decode NexusDock workflow template: %w", err))
	}
	if template.Status != taskstate.TemplateActive {
		return taskstate.Template{}, taskToolError(fmt.Errorf("workflow template %s is not active", id))
	}
	return template, nil
}

func normalizeTemplateIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func taskStepInputsFromTemplate(template taskstate.Template) []taskstate.TaskStepInput {
	steps := make([]taskstate.TaskStepInput, 0, len(template.Steps))
	for _, step := range template.Steps {
		steps = append(steps, taskstate.TaskStepInput{ID: step.ID, Title: step.Title, Phase: step.Phase, DependsOn: step.DependsOn, CompletionConditions: step.CompletionConditions, ContextRefs: step.ContextRefs, RequirementRefs: step.RequirementRefs, Quality: step.Quality, TDDTestCommand: step.TDDTestCommand, ExecutionContract: step.ExecutionContract})
	}
	return steps
}

func templateReferences(templates []taskstate.Template) []taskstate.TemplateReference {
	refs := make([]taskstate.TemplateReference, 0, len(templates))
	for _, template := range templates {
		refs = append(refs, taskstate.TemplateReference{ID: template.ID, Version: template.Version, Hash: template.Hash, SourceEvolutionID: template.SourceEvolutionID})
	}
	return refs
}
