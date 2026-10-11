package taskstate

import "time"

type TemplateStatus string

const (
	TemplateActive  TemplateStatus = "active"
	TemplateRetired TemplateStatus = "retired"
)

type MatchRule struct {
	Keywords []string `json:"keywords,omitempty"`
	Devices  []string `json:"devices,omitempty"`
	Type     string   `json:"type,omitempty"`
}

type StepQuality struct {
	Kind              string            `json:"kind"`
	Outcome           string            `json:"outcome"`
	RefactorGroup     string            `json:"refactor_group,omitempty"`
	DependencyReasons map[string]string `json:"dependency_reasons,omitempty"`
}

type TemplateStep struct {
	ID                   string       `json:"id"`
	Title                string       `json:"title"`
	Phase                Phase        `json:"phase"`
	DependsOn            []string     `json:"depends_on,omitempty"`
	CompletionConditions []string     `json:"completion_conditions,omitempty"`
	ContextRefs          []string     `json:"context_refs,omitempty"`
	RequirementRefs      []string     `json:"requirement_refs,omitempty"`
	Quality              *StepQuality `json:"quality,omitempty"`
}

type Template struct {
	ID                   string         `json:"id"`
	Version              string         `json:"version"`
	Title                string         `json:"title"`
	Description          string         `json:"description,omitempty"`
	SourceEvolutionID    string         `json:"source_evolution_id,omitempty"`
	Status               TemplateStatus `json:"status"`
	Match                MatchRule      `json:"match,omitempty"`
	CompletionConditions []string       `json:"completion_conditions"`
	RequirementRefs      []string       `json:"requirement_refs,omitempty"`
	Steps                []TemplateStep `json:"steps"`
	AllowLongTemplate    bool           `json:"allow_long_template,omitempty"`
	LongTemplateReason   string         `json:"long_template_reason,omitempty"`
	Hash                 string         `json:"hash,omitempty"`
	PublishedAt          *time.Time     `json:"published_at,omitempty"`
	RetiredAt            *time.Time     `json:"retired_at,omitempty"`
}

type TemplateCandidate struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Score   int    `json:"score"`
	Reason  string `json:"reason"`
}

type TemplateSelection struct {
	ID             string              `json:"id"`
	Version        string              `json:"version"`
	Hash           string              `json:"hash"`
	SelectedReason string              `json:"selected_reason"`
	Candidates     []TemplateCandidate `json:"candidates,omitempty"`
}

type TemplateReference struct {
	ID                string `json:"id"`
	Version           string `json:"version"`
	Hash              string `json:"hash,omitempty"`
	SourceEvolutionID string `json:"source_evolution_id,omitempty"`
}

type TaskStepInput struct {
	ID                   string       `json:"id"`
	Title                string       `json:"title"`
	Phase                Phase        `json:"phase,omitempty"`
	DependsOn            []string     `json:"depends_on,omitempty"`
	CompletionConditions []string     `json:"completion_conditions,omitempty"`
	ContextRefs          []string     `json:"context_refs,omitempty"`
	RequirementRefs      []string     `json:"requirement_refs,omitempty"`
	Quality              *StepQuality `json:"quality,omitempty"`
}

const (
	StepPending    = "pending"
	StepInProgress = "in_progress"
	StepCompleted  = "completed"
)

type DiagnosticEvidence struct {
	Command      string   `json:"command,omitempty"`
	Observed     string   `json:"observed,omitempty"`
	Symptom      string   `json:"symptom,omitempty"`
	ReproRef     string   `json:"repro_ref,omitempty"`
	Hypothesis   string   `json:"hypothesis,omitempty"`
	Falsifier    string   `json:"falsifier,omitempty"`
	Variable     string   `json:"variable,omitempty"`
	Method       string   `json:"method,omitempty"`
	Outcome      string   `json:"outcome,omitempty"`
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
}

type TaskStep struct {
	ID                   string              `json:"id"`
	Title                string              `json:"title"`
	Phase                Phase               `json:"phase"`
	DependsOn            []string            `json:"depends_on,omitempty"`
	CompletionConditions []string            `json:"completion_conditions,omitempty"`
	ContextRefs          []string            `json:"context_refs,omitempty"`
	RequirementRefs      []string            `json:"requirement_refs,omitempty"`
	Quality              *StepQuality        `json:"quality,omitempty"`
	Diagnostic           *DiagnosticEvidence `json:"diagnostic,omitempty"`
	ExecutionEvidence    *ExecutionEvidence  `json:"execution_evidence,omitempty"`
	Status               string              `json:"status"`
	UpdatedAt            time.Time           `json:"updated_at"`
}
