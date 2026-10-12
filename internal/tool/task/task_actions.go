package task

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/audittrust"
	"github.com/uvwt/agentdock/internal/taskstate"
)

var taskActions = []string{"create", "list", "get", "backtrace", "revalidate", "export_audit_bundle", "verify_audit_bundle", "export_signed_audit_bundle", "verify_signed_audit_bundle", "checkpoint", "block", "resume", "handoff", "final_review", "complete"}

var workflowTemplateActions = []string{"publish", "retire", "list", "get", "get_many", "match", "vector_index"}

type taskManageInput struct {
	Action                string
	Title                 string
	Goal                  string
	Project               string
	Device                string
	CompletionConditions  []string
	RequirementRefs       []string
	Steps                 []taskstate.TaskStepInput
	TemplateID            string
	SourceTemplateIDs     []string
	LearningChecks        []taskstate.EvolutionBinding
	Status                string
	Limit                 int
	TaskID                string
	StepID                string
	TDDSeam               *TDDSeamRequest
	ExecutionReceipts     []ExecutionReceiptRequest
	CompletedStepIDs      []string
	CompletedStepIDsSet   bool
	CurrentStepID         string
	NextStepID            string
	Summary               string
	ContextRefs           []string
	Diagnostic            *taskstate.DiagnosticEvidence
	ExecutionEvidence     *taskstate.ExecutionEvidence
	Verified              []string
	VerifiedEvidence      []taskstate.ReviewEvidenceBinding
	Risks                 []string
	IntentSpec            *taskstate.OutcomeAxis
	EngineeringPolicy     *taskstate.OutcomeAxis
	ProvenanceQuery       *ProvenanceQueryRequest
	ReviewRevision        string
	AuditBundle           *PortableAuditBundle
	AuditSignature        *audittrust.DetachedSignature
	AuditTrustRegistry    *audittrust.Registry
	ExpectedRootKeyID     string
	ExpectedTrustRevision int
	ExpectedBundleSHA256  string
}

type workflowTemplateInput struct {
	Action               string
	TemplateID           string
	TemplateIDs          []string
	TemplateVersion      string
	TemplateStatus       string
	Goal                 string
	Device               string
	Type                 string
	Template             taskstate.Template
	AllowLongTemplateSet bool
	AllowLongTemplate    bool
	LongTemplateReason   string
}

type workflowTemplatePublishRequest struct {
	Template taskstate.Template `json:"template"`
}

type workflowTemplateMatchRequest struct {
	Goal   string `json:"goal"`
	Device string `json:"device"`
	Type   string `json:"type"`
}

func normalizeTaskManageRequest(request ManageRequest) (taskManageInput, error) {
	steps := make([]taskstate.TaskStepInput, len(request.Steps))
	for i, step := range request.Steps {
		steps[i] = taskstate.TaskStepInput{ID: step.ID, Title: step.Title, DependsOn: step.DependsOn, CompletionConditions: step.CompletionConditions, ContextRefs: step.ContextRefs, RequirementRefs: step.RequirementRefs, Quality: step.Quality, TDDTestCommand: step.TDDTestCommand, ExecutionContract: step.ExecutionContract}
	}
	learningChecks := make([]taskstate.EvolutionBinding, len(request.LearningChecks))
	for i, check := range request.LearningChecks {
		learningChecks[i] = taskstate.EvolutionBinding{
			EvolutionID: check.EvolutionID,
			OnSuccess:   check.OnSuccess,
			OnFailure:   check.OnFailure,
		}
	}

	var executionEvidence *taskstate.ExecutionEvidence
	if request.ExecutionEvidence != nil {
		executionEvidence = &taskstate.ExecutionEvidence{EvidenceRefs: append([]string(nil), request.ExecutionEvidence.EvidenceRefs...)}
	}

	input := taskManageInput{
		Action:                strings.ToLower(strings.TrimSpace(request.Action)),
		Title:                 request.Title,
		Goal:                  request.Goal,
		Project:               strings.ToLower(strings.TrimSpace(request.Project)),
		Device:                strings.TrimSpace(request.Device),
		CompletionConditions:  append([]string(nil), request.CompletionConditions...),
		RequirementRefs:       append([]string(nil), request.RequirementRefs...),
		Steps:                 steps,
		TemplateID:            strings.TrimSpace(request.TemplateID),
		SourceTemplateIDs:     append([]string(nil), request.SourceTemplateIDs...),
		LearningChecks:        learningChecks,
		Status:                strings.ToLower(strings.TrimSpace(request.Status)),
		Limit:                 intValue(request.Limit, 50),
		TaskID:                request.TaskID,
		StepID:                request.StepID,
		TDDSeam:               request.TDDSeam,
		ExecutionReceipts:     append([]ExecutionReceiptRequest(nil), request.ExecutionReceipts...),
		CurrentStepID:         request.CurrentStepID,
		NextStepID:            request.NextStepID,
		Summary:               request.Summary,
		ContextRefs:           append([]string(nil), request.ContextRefs...),
		Diagnostic:            request.Diagnostic,
		ExecutionEvidence:     executionEvidence,
		Verified:              append([]string(nil), request.Verified...),
		VerifiedEvidence:      append([]taskstate.ReviewEvidenceBinding(nil), request.VerifiedEvidence...),
		Risks:                 append([]string(nil), request.Risks...),
		IntentSpec:            request.IntentSpec,
		EngineeringPolicy:     request.EngineeringPolicy,
		ProvenanceQuery:       request.ProvenanceQuery,
		ReviewRevision:        strings.TrimSpace(request.ReviewRevision),
		AuditBundle:           request.AuditBundle,
		AuditSignature:        request.AuditSignature,
		AuditTrustRegistry:    request.AuditTrustRegistry,
		ExpectedRootKeyID:     strings.TrimSpace(request.ExpectedRootKeyID),
		ExpectedTrustRevision: request.ExpectedTrustRevision,
		ExpectedBundleSHA256:  strings.ToLower(strings.TrimSpace(request.ExpectedBundleSHA256)),
	}
	if request.CompletedStepIDs != nil {
		input.CompletedStepIDsSet = true
		input.CompletedStepIDs = append([]string(nil), (*request.CompletedStepIDs)...)
	}
	if input.Diagnostic != nil && input.Action != "checkpoint" {
		return input, toolErrorDetails("VALIDATION_ERROR", "diagnostic evidence is only valid for checkpoint", "validation", map[string]any{"field": "diagnostic", "action": input.Action})
	}
	if input.ExecutionEvidence != nil && input.Action != "checkpoint" {
		return input, toolErrorDetails("VALIDATION_ERROR", "execution_evidence is only valid for checkpoint", "validation", map[string]any{"field": "execution_evidence", "action": input.Action})
	}
	if input.TDDSeam != nil && input.Action != "checkpoint" {
		return input, toolErrorDetails("VALIDATION_ERROR", "tdd_seam is only valid for checkpoint", "validation", map[string]any{"field": "tdd_seam", "action": input.Action})
	}
	if len(input.ExecutionReceipts) > 0 && input.Action != "checkpoint" {
		return input, toolErrorDetails("VALIDATION_ERROR", "execution_receipts are only valid for checkpoint", "validation", map[string]any{"field": "execution_receipts", "action": input.Action})
	}
	if input.TDDSeam != nil && len(input.ExecutionReceipts) > 0 {
		return input, toolErrorDetails("VALIDATION_ERROR", "tdd_seam and execution_receipts cannot be combined", "validation", nil)
	}
	if len(input.RequirementRefs) > 0 && input.Action != "create" {
		return input, toolErrorDetails("VALIDATION_ERROR", "top-level requirement_refs are only valid for create", "validation", map[string]any{"field": "requirement_refs", "action": input.Action})
	}
	if len(input.VerifiedEvidence) > 0 && input.Action != "final_review" {
		return input, toolErrorDetails("VALIDATION_ERROR", "verified_evidence is only valid for final_review", "validation", map[string]any{"field": "verified_evidence", "action": input.Action})
	}
	if input.ProvenanceQuery != nil && input.Action != "backtrace" {
		return input, toolErrorDetails("VALIDATION_ERROR", "provenance_query is only valid for backtrace", "validation", map[string]any{"field": "provenance_query", "action": input.Action})
	}
	if input.Action == "backtrace" && input.ProvenanceQuery == nil {
		return input, toolErrorDetails("VALIDATION_ERROR", "provenance_query is required for backtrace", "validation", map[string]any{"field": "provenance_query"})
	}
	if input.ReviewRevision != "" && input.Action != "revalidate" && input.Action != "export_audit_bundle" && input.Action != "export_signed_audit_bundle" {
		return input, toolErrorDetails("VALIDATION_ERROR", "review_revision is only valid for revalidate, export_audit_bundle, or export_signed_audit_bundle", "validation", map[string]any{"field": "review_revision", "action": input.Action})
	}
	if input.AuditBundle != nil && input.Action != "verify_audit_bundle" && input.Action != "verify_signed_audit_bundle" {
		return input, toolErrorDetails("VALIDATION_ERROR", "audit_bundle is only valid for verify_audit_bundle or verify_signed_audit_bundle", "validation", map[string]any{"field": "audit_bundle", "action": input.Action})
	}
	if input.AuditSignature != nil && input.Action != "verify_signed_audit_bundle" {
		return input, toolErrorDetails("VALIDATION_ERROR", "audit_signature is only valid for verify_signed_audit_bundle", "validation", map[string]any{"field": "audit_signature", "action": input.Action})
	}
	if input.AuditTrustRegistry != nil && input.Action != "verify_signed_audit_bundle" {
		return input, toolErrorDetails("VALIDATION_ERROR", "audit_trust_registry is only valid for verify_signed_audit_bundle", "validation", map[string]any{"field": "audit_trust_registry", "action": input.Action})
	}
	if input.ExpectedRootKeyID != "" && input.Action != "verify_signed_audit_bundle" {
		return input, toolErrorDetails("VALIDATION_ERROR", "expected_root_key_id is only valid for verify_signed_audit_bundle", "validation", map[string]any{"field": "expected_root_key_id", "action": input.Action})
	}
	if input.ExpectedTrustRevision != 0 && input.Action != "verify_signed_audit_bundle" {
		return input, toolErrorDetails("VALIDATION_ERROR", "expected_trust_revision is only valid for verify_signed_audit_bundle", "validation", map[string]any{"field": "expected_trust_revision", "action": input.Action})
	}
	if input.Action == "verify_audit_bundle" && input.AuditBundle == nil {
		return input, toolErrorDetails("VALIDATION_ERROR", "audit_bundle is required for verify_audit_bundle", "validation", map[string]any{"field": "audit_bundle"})
	}
	if input.Action == "verify_signed_audit_bundle" {
		if input.AuditBundle == nil || input.AuditSignature == nil || input.AuditTrustRegistry == nil || input.ExpectedRootKeyID == "" || input.ExpectedTrustRevision <= 0 {
			return input, toolErrorDetails("VALIDATION_ERROR", "verify_signed_audit_bundle requires audit_bundle, audit_signature, audit_trust_registry, expected_root_key_id, and expected_trust_revision > 0", "validation", map[string]any{"action": input.Action})
		}
	}
	if input.ExpectedBundleSHA256 != "" && input.Action != "verify_audit_bundle" && input.Action != "verify_signed_audit_bundle" {
		return input, toolErrorDetails("VALIDATION_ERROR", "expected_bundle_sha256 is only valid for audit bundle verification actions", "validation", map[string]any{"field": "expected_bundle_sha256", "action": input.Action})
	}
	if (input.Action == "revalidate" || input.Action == "export_audit_bundle" || input.Action == "export_signed_audit_bundle") && strings.TrimSpace(input.TaskID) == "" {
		return input, toolErrorDetails("VALIDATION_ERROR", "task_id is required for provenance integrity export actions", "validation", map[string]any{"field": "task_id", "action": input.Action})
	}
	if (strings.TrimSpace(input.NextStepID) != "" || len(input.ContextRefs) > 0) && input.Action != "handoff" {
		return input, toolErrorDetails("VALIDATION_ERROR", "next_step_id and top-level context_refs are only valid for handoff", "validation", map[string]any{"action": input.Action})
	}
	if len(input.LearningChecks) > 3 {
		return input, toolErrorDetails("VALIDATION_ERROR", "learning_checks cannot exceed 3", "validation", map[string]any{"field": "learning_checks"})
	}
	return input, nil
}

func normalizeWorkflowRequest(request WorkflowRequest) workflowTemplateInput {
	input := workflowTemplateInput{
		Action:             strings.ToLower(strings.TrimSpace(request.Action)),
		TemplateID:         strings.TrimSpace(request.TemplateID),
		TemplateIDs:        append([]string(nil), request.TemplateIDs...),
		TemplateVersion:    strings.TrimSpace(request.TemplateVersion),
		TemplateStatus:     strings.TrimSpace(request.TemplateStatus),
		Goal:               request.Goal,
		Device:             request.Device,
		Type:               request.Type,
		LongTemplateReason: strings.TrimSpace(request.LongTemplateReason),
	}
	if request.AllowLongTemplate != nil {
		input.AllowLongTemplateSet = true
		input.AllowLongTemplate = *request.AllowLongTemplate
	}
	if request.Template != nil {
		input.Template = *request.Template
	}
	if input.Action == "publish" {
		input.applyTemplateGuardrails()
	}
	return input
}

func (input workflowTemplateInput) escapedTemplatePath(action string) string {
	id := url.PathEscape(input.TemplateID)
	version := url.PathEscape(input.TemplateVersion)
	if action == "" {
		return fmt.Sprintf("/v1/workflow-templates/%s/%s", id, version)
	}
	return fmt.Sprintf("/v1/workflow-templates/%s/%s/%s", id, version, action)
}

func (input *workflowTemplateInput) applyTemplateGuardrails() {
	if input.AllowLongTemplateSet {
		input.Template.AllowLongTemplate = input.AllowLongTemplate
	}
	if input.LongTemplateReason != "" {
		input.Template.LongTemplateReason = input.LongTemplateReason
	}
}

func (s *Service) Manage(ctx context.Context, request ManageRequest) (Result, error) {
	input, err := normalizeTaskManageRequest(request)
	if err != nil {
		return nil, err
	}
	var task taskstate.Task
	var evolutionWarning string
	switch input.Action {
	case "create":
		steps := append([]taskstate.TaskStepInput(nil), input.Steps...)
		conditions := append([]string(nil), input.CompletionConditions...)
		requirementRefs := append([]string(nil), input.RequirementRefs...)
		var sourceTemplates []taskstate.TemplateReference
		if input.TemplateID != "" && len(input.SourceTemplateIDs) > 0 {
			return nil, taskToolError(fmt.Errorf("template_id and source_template_ids cannot be used together"))
		}
		if input.TemplateID != "" {
			template, fetchErr := s.nexusActiveWorkflowTemplate(ctx, input.TemplateID)
			if fetchErr != nil {
				return nil, fetchErr
			}
			if len(steps) == 0 {
				steps = taskStepInputsFromTemplate(template)
			}
			conditions = append(append([]string{}, template.CompletionConditions...), conditions...)
			requirementRefs = mergeUniqueStrings(template.RequirementRefs, requirementRefs)
			sourceTemplates = []taskstate.TemplateReference{{ID: template.ID, Version: template.Version, Hash: template.Hash, SourceEvolutionID: template.SourceEvolutionID}}
		}
		if len(input.SourceTemplateIDs) > 0 {
			if len(input.SourceTemplateIDs) < 2 || len(input.SourceTemplateIDs) > 3 {
				return nil, taskToolError(fmt.Errorf("source_template_ids must contain 2 or 3 template ids"))
			}
			if len(steps) == 0 || len(conditions) == 0 {
				return nil, toolErrorDetails("TEMPLATE_COMPOSITION_REQUIRED", "multiple source templates require composed steps and completion_conditions", "validation", map[string]any{"source_template_ids": input.SourceTemplateIDs})
			}
			templates, fetchErr := s.nexusActiveWorkflowTemplates(ctx, input.SourceTemplateIDs)
			if fetchErr != nil {
				return nil, fetchErr
			}
			sourceTemplates = templateReferences(templates)
			for _, template := range templates {
				requirementRefs = mergeUniqueStrings(requirementRefs, template.RequirementRefs)
			}
		}
		bindings := append([]taskstate.EvolutionBinding(nil), input.LearningChecks...)
		if len(bindings) > 0 {
			if s.evolution == nil || s.config().NexusEndpoint == "" {
				return nil, taskToolError(fmt.Errorf("learning_checks require Evolution with Nexus configured"))
			}
			preflightTask := taskstate.Task{Project: input.Project, Device: input.Device, SourceTemplates: sourceTemplates}
			bindings, err = s.evolution.ValidateBindings(ctx, preflightTask, bindings)
			if err != nil {
				return nil, taskToolError(err)
			}
		}
		task, err = s.tasks.CreateWithTraceability(input.Title, input.Goal, input.Project, input.Device, conditions, steps, sourceTemplates, requirementRefs, bindings...)
		if err != nil {
			return nil, taskToolError(err)
		}
		task, evolutionWarning = s.refreshGuidanceBestEffort(ctx, task)
		result := Result{
			"action": input.Action, "task_id": task.ID, "task_summary": compactTaskSummary(task), "state_dir": s.tasks.Root(),
			"next_required_action": "Work ready_frontier steps first; independent ready steps may progress concurrently. Use checkpoint at meaningful recovery points. Use block only for a real blocker. After all steps and real verification are complete, call final_review, then complete.",
		}
		if len(task.GuidanceContext) > 0 {
			result["guidance_context"] = task.GuidanceContext
		}
		if evolutionWarning != "" {
			result["evolution_warning"] = evolutionWarning
		}
		return result, nil
	case "list":
		status := taskstate.Status(input.Status)
		if status != "" && status != taskstate.StatusActive && status != taskstate.StatusBlocked && status != taskstate.StatusCompleted {
			return nil, toolErrorDetails("INVALID_STATUS", "unsupported task status filter", "validation", map[string]any{"status": status, "allowed": []string{"active", "blocked", "completed"}})
		}
		tasks, listErr := s.tasks.List(status, input.Limit)
		if listErr != nil {
			return nil, taskToolError(listErr)
		}
		items := make([]map[string]any, 0, len(tasks))
		for _, item := range tasks {
			items = append(items, compactTaskListItem(item))
		}
		return Result{"action": input.Action, "tasks": items, "count": len(items), "state_dir": s.tasks.Root()}, nil
	case "backtrace":
		trace, traceErr := s.provenanceBacktrace(ctx, input.ProvenanceQuery)
		if traceErr != nil {
			return nil, taskToolError(traceErr)
		}
		return Result{"action": input.Action, "backtrace": trace, "state_dir": s.tasks.Root()}, nil
	case "revalidate":
		report, _ := s.provenanceIntegrity(ctx, input.TaskID, input.ReviewRevision)
		return Result{"action": input.Action, "integrity": report, "state_dir": s.tasks.Root()}, nil
	case "export_audit_bundle":
		report, bundle := s.provenanceIntegrity(ctx, input.TaskID, input.ReviewRevision)
		result := Result{"action": input.Action, "integrity": report, "state_dir": s.tasks.Root()}
		if report.Valid && bundle != nil {
			result["audit_bundle"] = *bundle
			result["bundle_sha256"] = bundle.Manifest.BundleSHA256
		}
		return result, nil
	case "verify_audit_bundle":
		report := VerifyPortableAuditBundleExpected(*input.AuditBundle, input.ExpectedBundleSHA256)
		return Result{"action": input.Action, "integrity": report, "bundle_sha256": report.BundleSHA256, "state_dir": s.tasks.Root()}, nil
	case "export_signed_audit_bundle":
		report, bundle := s.provenanceIntegrity(ctx, input.TaskID, input.ReviewRevision)
		result := Result{"action": input.Action, "integrity": report, "state_dir": s.tasks.Root()}
		if !report.Valid || bundle == nil {
			return result, nil
		}
		cfg := s.config()
		signature, registry, signErr := audittrust.SignWithRegistry(cfg.AgentDockHome, audittrust.SignInput{
			BundleContract: bundle.Contract,
			BundleSHA256:   bundle.Manifest.BundleSHA256,
			TaskID:         bundle.Manifest.TaskID,
			ReviewRevision: bundle.Manifest.ReviewRevision,
		}, time.Now())
		if signErr != nil {
			return nil, taskToolError(signErr)
		}
		result["audit_bundle"] = *bundle
		result["bundle_sha256"] = bundle.Manifest.BundleSHA256
		result["audit_signature"] = signature
		result["audit_trust_registry"] = registry
		result["root_key_id"] = registry.RootKeyID
		result["trust_registry_revision"] = registry.Revision
		return result, nil
	case "verify_signed_audit_bundle":
		report := VerifySignedPortableAuditBundleExpected(*input.AuditBundle, *input.AuditSignature, *input.AuditTrustRegistry, input.ExpectedRootKeyID, input.ExpectedBundleSHA256, input.ExpectedTrustRevision, time.Now())
		return Result{
			"action": input.Action, "signed_verification": report, "integrity": report.Integrity,
			"bundle_sha256": report.Integrity.BundleSHA256, "root_key_id": report.Trust.RootKeyID,
			"trust_registry_revision": report.Trust.RegistryRevision, "state_dir": s.tasks.Root(),
		}, nil
	case "get":
		task, err = s.tasks.Get(input.TaskID)
		if err != nil {
			return nil, taskToolError(err)
		}
		result := Result{"action": input.Action, "task": task, "state_dir": s.tasks.Root()}
		if resume := compactResumeContext(task); resume != nil {
			result["resume_context"] = resume
		}
		return result, nil
	case "checkpoint":
		singleStepMode := strings.TrimSpace(input.StepID) != "" || input.Status != ""
		batchMode := input.CompletedStepIDsSet || strings.TrimSpace(input.CurrentStepID) != ""
		if singleStepMode && batchMode {
			return nil, toolErrorDetails("VALIDATION_ERROR", "single-step and batch checkpoint fields cannot be combined", "validation", map[string]any{
				"single_step_fields": []string{"step_id", "status"},
				"batch_fields":       []string{"completed_step_ids", "current_step_id"},
			})
		}
		if batchMode {
			if input.TDDSeam != nil {
				return nil, toolErrorDetails("VALIDATION_ERROR", "tdd_seam is only accepted by single-step checkpoint", "validation", map[string]any{"field": "tdd_seam"})
			}
			if len(input.ExecutionReceipts) > 0 {
				return nil, toolErrorDetails("VALIDATION_ERROR", "execution_receipts are only accepted by single-step checkpoint", "validation", map[string]any{"field": "execution_receipts"})
			}
			if input.Diagnostic != nil {
				return nil, toolErrorDetails("VALIDATION_ERROR", "diagnostic evidence is only accepted by single-step checkpoint", "validation", map[string]any{"field": "diagnostic"})
			}
			if input.ExecutionEvidence != nil {
				return nil, toolErrorDetails("VALIDATION_ERROR", "execution_evidence is only accepted by single-step checkpoint", "validation", map[string]any{"field": "execution_evidence"})
			}
			task, err = s.tasks.BatchCheckpoint(input.TaskID, input.CompletedStepIDs, input.CurrentStepID, input.Summary)
		} else {
			task, err = s.checkpointWithReceiptEvidence(input.TaskID, input.StepID, input.Status, input.Summary, input.TDDSeam, input.ExecutionReceipts, input.Diagnostic, input.ExecutionEvidence)
		}
	case "block":
		task, err = s.tasks.Block(input.TaskID, input.Summary)
	case "resume":
		task, err = s.tasks.Resume(input.TaskID, input.Summary)
		if err == nil {
			task, evolutionWarning = s.refreshGuidanceBestEffort(ctx, task)
		}
	case "handoff":
		task, err = s.tasks.Handoff(input.TaskID, input.NextStepID, input.Summary, input.ContextRefs, input.Risks)
	case "final_review":
		review := taskstate.FinalReviewInput{Status: input.Status, Summary: input.Summary, VerifiedFacts: input.Verified, VerifiedEvidence: input.VerifiedEvidence, OpenRisks: input.Risks, IntentSpec: input.IntentSpec, EngineeringPolicy: input.EngineeringPolicy}
		task, err = s.tasks.FinalReview(input.TaskID, review)
		if err == nil {
			warnings := make([]string, 0, 2)
			if warning := s.resolveBindingsBestEffort(ctx, task); warning != "" {
				warnings = append(warnings, warning)
			}
			var warning string
			task, warning = s.refreshCandidatesBestEffort(ctx, task)
			if warning != "" {
				warnings = append(warnings, warning)
			}
			evolutionWarning = strings.Join(warnings, "; ")
		}
	case "complete":
		task, err = s.tasks.Complete(input.TaskID)
	default:
		return nil, toolErrorDetails("INVALID_ACTION", "unsupported task_manage action", "validation", map[string]any{"action": input.Action, "allowed": taskActions})
	}
	if err != nil {
		return nil, taskToolError(err)
	}
	result := Result{"action": input.Action, "task_id": task.ID, "task_summary": compactTaskSummary(task), "state_dir": s.tasks.Root()}
	if input.Action == "handoff" {
		result["resume_context"] = compactResumeContext(task)
		result["next_required_action"] = "Start the next session from resume_context; load its context_refs first, then continue next_step without restating source material."
	}
	if input.Action == "resume" && len(task.GuidanceContext) > 0 {
		result["guidance_context"] = task.GuidanceContext
	}
	if input.Action == "final_review" && task.FinalReview != nil {
		result["review_revision"] = task.FinalReview.ReviewRevision
		if len(task.EvolutionCandidates) > 0 {
			result["evolution_candidates"] = task.EvolutionCandidates
		}
	}
	if evolutionWarning != "" {
		result["evolution_warning"] = evolutionWarning
	}
	return result, nil
}
