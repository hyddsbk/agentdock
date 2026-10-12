package evolution

import "github.com/uvwt/agentdock/internal/taskstate"

const PolicyVersion = "v1"

const (
	StatusProvisional = "provisional"
	StatusActive      = "active"
	StatusVerified    = "verified"
	StatusQuarantine  = "quarantine"
	StatusRetired     = "retired"
)

const (
	RelationSupport       = "support"
	RelationContradict    = "contradict"
	RelationNone          = "none"
	RelationNotApplicable = "not_applicable"
	RelationUncertain     = "uncertain"
)

type Candidate struct {
	Type         string   `json:"type"`
	Statement    string   `json:"statement"`
	Scope        string   `json:"scope,omitempty"`
	Project      string   `json:"project,omitempty"`
	Device       string   `json:"device,omitempty"`
	CanonicalKey string   `json:"canonical_key,omitempty"`
	Source       string   `json:"source,omitempty"`
	Tags         []string `json:"tags,omitempty"`
}

type LearningCheck struct {
	OnSuccess string `json:"on_success"`
	OnFailure string `json:"on_failure"`
}

type Request struct {
	Intent         string         `json:"intent"`
	Candidate      *Candidate     `json:"candidate,omitempty"`
	LearningCheck  *LearningCheck `json:"learning_check,omitempty"`
	EvolutionID    string         `json:"evolution_id,omitempty"`
	TaskID         string         `json:"task_id,omitempty"`
	ReviewRevision string         `json:"review_revision,omitempty"`
	Relation       string         `json:"relation,omitempty"`
	EvidenceRefs   []string       `json:"evidence_refs,omitempty"`
	Rationale      string         `json:"rationale,omitempty"`
	SupersededBy   string         `json:"superseded_by,omitempty"`
}

type Evidence struct {
	Ref            string `json:"ref"`
	Relation       string `json:"relation"`
	TaskID         string `json:"task_id,omitempty"`
	ReviewRevision string `json:"review_revision,omitempty"`
	Rationale      string `json:"rationale,omitempty"`
	RecordedAt     string `json:"recorded_at"`
}

type AppliedOperation struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
}

type Record struct {
	EvolutionID       string             `json:"evolution_id"`
	Title             string             `json:"title"`
	Statement         string             `json:"statement"`
	Type              string             `json:"type"`
	Scope             string             `json:"scope"`
	Project           string             `json:"project"`
	Device            string             `json:"device,omitempty"`
	CanonicalKey      string             `json:"canonical_key,omitempty"`
	Status            string             `json:"status"`
	PolicyVersion     string             `json:"policy_version"`
	Revision          int64              `json:"revision"`
	SupportCount      int                `json:"support_count"`
	ContradictCount   int                `json:"contradict_count"`
	Source            string             `json:"source,omitempty"`
	Tags              []string           `json:"tags,omitempty"`
	Evidence          []Evidence         `json:"evidence,omitempty"`
	SupersededBy      string             `json:"superseded_by,omitempty"`
	AppliedOperations []AppliedOperation `json:"applied_operations,omitempty"`
	CreatedAt         string             `json:"created_at"`
	UpdatedAt         string             `json:"updated_at"`
}

type Result struct {
	Intent     string  `json:"intent"`
	Record     *Record `json:"record,omitempty"`
	Relation   string  `json:"relation,omitempty"`
	Changed    bool    `json:"changed"`
	Idempotent bool    `json:"idempotent,omitempty"`
	Message    string  `json:"message,omitempty"`
}

type Query struct {
	Query       string   `json:"query"`
	EvolutionID string   `json:"evolution_id,omitempty"`
	EvidenceRef string   `json:"evidence_ref,omitempty"`
	Statuses    []string `json:"statuses,omitempty"`
	Types       []string `json:"types,omitempty"`
	Scope       string   `json:"scope,omitempty"`
	Project     string   `json:"project,omitempty"`
	Device      string   `json:"device,omitempty"`
	Limit       int      `json:"limit,omitempty"`
}

type transitionRequest struct {
	OperationID      string   `json:"operation_id"`
	ExpectedRevision int64    `json:"expected_revision"`
	PolicyVersion    string   `json:"policy_version"`
	NextState        string   `json:"next_state"`
	EvidenceRefs     []string `json:"evidence_refs,omitempty"`
	Record           Record   `json:"record"`
}

type transitionResult struct {
	Record     Record `json:"record"`
	Idempotent bool   `json:"idempotent"`
}

type queryResult struct {
	Records []Record `json:"records"`
	Count   int      `json:"count"`
}

func contextItem(record Record, guided bool, reviewRevision string) taskstate.EvolutionContextItem {
	return taskstate.EvolutionContextItem{
		EvolutionID:    record.EvolutionID,
		Type:           record.Type,
		Statement:      record.Statement,
		Scope:          record.Scope,
		Project:        record.Project,
		Device:         record.Device,
		Status:         record.Status,
		Guided:         guided,
		ReviewRevision: reviewRevision,
	}
}
