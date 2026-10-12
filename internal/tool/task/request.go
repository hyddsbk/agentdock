package task

import "github.com/uvwt/agentdock/internal/taskstate"

type StepRequest struct {
	ID                   string                           `json:"id"`
	Title                string                           `json:"title"`
	DependsOn            []string                         `json:"depends_on,omitempty"`
	CompletionConditions []string                         `json:"completion_conditions,omitempty"`
	ContextRefs          []string                         `json:"context_refs,omitempty"`
	RequirementRefs      []string                         `json:"requirement_refs,omitempty"`
	Quality              *taskstate.StepQuality           `json:"quality,omitempty"`
	TDDTestCommand       string                           `json:"tdd_test_command,omitempty"`
	ExecutionContract    *taskstate.ExecutionContractSpec `json:"execution_contract,omitempty"`
}

type LearningCheckRequest struct {
	EvolutionID string `json:"evolution_id"`
	OnSuccess   string `json:"on_success"`
	OnFailure   string `json:"on_failure"`
}

type TDDSeamRequest struct {
	RedRef        string `json:"red_ref"`
	GreenRef      string `json:"green_ref"`
	RegressionRef string `json:"regression_ref"`
}

type ExecutionEvidenceRequest struct {
	EvidenceRefs []string `json:"evidence_refs"`
}

type ExecutionReceiptRequest struct {
	CheckID string `json:"check_id"`
	Ref     string `json:"ref"`
}

type ProvenanceQueryRequest struct {
	Kind           string `json:"kind"`
	Ref            string `json:"ref"`
	TaskID         string `json:"task_id,omitempty"`
	EvolutionID    string `json:"evolution_id,omitempty"`
	ReviewRevision string `json:"review_revision,omitempty"`
}

// ManageRequest 只表达 task_manage 对模型公开的输入字段。
// 持久化状态中的 phase、bound_at 等内部字段在规范化阶段由 Task 域模型补齐，不能从边界注入。
type ManageRequest struct {
	Action               string                            `json:"action"`
	Title                string                            `json:"title,omitempty"`
	Goal                 string                            `json:"goal,omitempty"`
	Project              string                            `json:"project,omitempty"`
	Device               string                            `json:"device,omitempty"`
	CompletionConditions []string                          `json:"completion_conditions,omitempty"`
	RequirementRefs      []string                          `json:"requirement_refs,omitempty"`
	Steps                []StepRequest                     `json:"steps,omitempty"`
	TemplateID           string                            `json:"template_id,omitempty"`
	SourceTemplateIDs    []string                          `json:"source_template_ids,omitempty"`
	LearningChecks       []LearningCheckRequest            `json:"learning_checks,omitempty"`
	Status               string                            `json:"status,omitempty"`
	Limit                *int                              `json:"limit,omitempty"`
	TaskID               string                            `json:"task_id,omitempty"`
	StepID               string                            `json:"step_id,omitempty"`
	TDDSeam              *TDDSeamRequest                   `json:"tdd_seam,omitempty"`
	CompletedStepIDs     *[]string                         `json:"completed_step_ids,omitempty"`
	CurrentStepID        string                            `json:"current_step_id,omitempty"`
	NextStepID           string                            `json:"next_step_id,omitempty"`
	Summary              string                            `json:"summary,omitempty"`
	ContextRefs          []string                          `json:"context_refs,omitempty"`
	Diagnostic           *taskstate.DiagnosticEvidence     `json:"diagnostic,omitempty"`
	ExecutionEvidence    *ExecutionEvidenceRequest         `json:"execution_evidence,omitempty"`
	ExecutionReceipts    []ExecutionReceiptRequest         `json:"execution_receipts,omitempty"`
	Verified             []string                          `json:"verified,omitempty"`
	VerifiedEvidence     []taskstate.ReviewEvidenceBinding `json:"verified_evidence,omitempty"`
	Risks                []string                          `json:"risks,omitempty"`
	IntentSpec           *taskstate.OutcomeAxis            `json:"intent_spec,omitempty"`
	EngineeringPolicy    *taskstate.OutcomeAxis            `json:"engineering_policy,omitempty"`
	ProvenanceQuery      *ProvenanceQueryRequest           `json:"provenance_query,omitempty"`
}

// WorkflowRequest 是 workflow_template_manage 的强类型输入。
type WorkflowRequest struct {
	Action             string              `json:"action"`
	TemplateID         string              `json:"template_id,omitempty"`
	TemplateIDs        []string            `json:"template_ids,omitempty"`
	TemplateVersion    string              `json:"template_version,omitempty"`
	TemplateStatus     string              `json:"template_status,omitempty"`
	Goal               string              `json:"goal,omitempty"`
	Device             string              `json:"device,omitempty"`
	Type               string              `json:"type,omitempty"`
	Template           *taskstate.Template `json:"template,omitempty"`
	AllowLongTemplate  *bool               `json:"allow_long_template,omitempty"`
	LongTemplateReason string              `json:"long_template_reason,omitempty"`
}

func intValue(value *int, fallback int) int {
	if value == nil {
		return fallback
	}
	return *value
}
