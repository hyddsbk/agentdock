package taskstate

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	EvidenceFactObserved     = "observed"
	EvidenceFactDerived      = "derived"
	EvidenceFactInferred     = "inferred"
	EvidenceFactUnknown      = "unknown"
	EvidenceFactContradicted = "contradicted"

	EvidenceCoverageComplete = "complete"
	EvidenceCoveragePartial  = "partial"
	EvidenceCoverageUnknown  = "unknown"

	UnknownStatusOpen     = "open"
	UnknownStatusResolved = "resolved"

	maxEvidenceRecordsPerCheckpoint = 16
	maxEvidenceRecordsPerStep       = 64
	maxEvidenceLimitations          = 8
	maxEvidenceStatementBytes       = 4 << 10
	maxEvidenceLimitationBytes      = 2 << 10
	maxTaskUnknowns                 = 64
	maxUnknownQuestionBytes         = 4 << 10
	maxUnknownResolutionBytes       = 4 << 10
)

// EvidenceRecordInput is the caller-supplied semantic evidence payload.
// Identity and timestamps are server-owned and assigned atomically at checkpoint.
type EvidenceRecordInput struct {
	Statement        string   `json:"statement"`
	FactStatus       string   `json:"fact_status"`
	SourceRefs       []string `json:"source_refs,omitempty"`
	Coverage         string   `json:"coverage"`
	Limitations      []string `json:"limitations,omitempty"`
	UntrustedContent bool     `json:"untrusted_content,omitempty"`
	PartialResult    bool     `json:"partial_result,omitempty"`
}

// EvidenceRecord is a durable, provider-neutral observation/derivation record
// owned by one Task step. SourceRefs remain opaque pointers; this layer never
// executes or interprets an upstream provider.
type EvidenceRecord struct {
	ID               string    `json:"id"`
	Statement        string    `json:"statement"`
	FactStatus       string    `json:"fact_status"`
	SourceRefs       []string  `json:"source_refs,omitempty"`
	Coverage         string    `json:"coverage"`
	Limitations      []string  `json:"limitations,omitempty"`
	UntrustedContent bool      `json:"untrusted_content,omitempty"`
	PartialResult    bool      `json:"partial_result,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

// UnknownUpdate mutates the Task-owned unknown ledger during a single-step
// checkpoint. Resolution is optimistic-concurrency guarded by ExpectedRevision.
type UnknownUpdate struct {
	Action           string   `json:"action"`
	UnknownID        string   `json:"unknown_id,omitempty"`
	ExpectedRevision int      `json:"expected_revision,omitempty"`
	Question         string   `json:"question,omitempty"`
	Blocking         bool     `json:"blocking,omitempty"`
	EvidenceRefs     []string `json:"evidence_refs,omitempty"`
	Resolution       string   `json:"resolution,omitempty"`
}

// UnknownRecord is a first-class unresolved question. Open unknowns never become
// negative evidence merely because a search or page was exhausted.
type UnknownRecord struct {
	ID                     string     `json:"id"`
	Question               string     `json:"question"`
	Blocking               bool       `json:"blocking,omitempty"`
	Status                 string     `json:"status"`
	Revision               int        `json:"revision"`
	OpenedStepID           string     `json:"opened_step_id"`
	EvidenceRefs           []string   `json:"evidence_refs,omitempty"`
	Resolution             string     `json:"resolution,omitempty"`
	ResolutionEvidenceRefs []string   `json:"resolution_evidence_refs,omitempty"`
	ResolvedStepID         string     `json:"resolved_step_id,omitempty"`
	OpenedAt               time.Time  `json:"opened_at"`
	ResolvedAt             *time.Time `json:"resolved_at,omitempty"`
}

func normalizeEvidenceRecordInputs(values []EvidenceRecordInput) ([]EvidenceRecordInput, error) {
	if len(values) > maxEvidenceRecordsPerCheckpoint {
		return nil, fmt.Errorf("evidence_records cannot exceed %d per checkpoint", maxEvidenceRecordsPerCheckpoint)
	}
	out := make([]EvidenceRecordInput, 0, len(values))
	for i, raw := range values {
		statement := strings.TrimSpace(raw.Statement)
		if statement == "" || statement != raw.Statement || strings.ContainsAny(statement, "\r\n") || len([]byte(statement)) > maxEvidenceStatementBytes {
			return nil, fmt.Errorf("evidence record %d has invalid statement", i)
		}
		factStatus := strings.ToLower(strings.TrimSpace(raw.FactStatus))
		if !validEvidenceFactStatus(factStatus) {
			return nil, fmt.Errorf("evidence record %d has invalid fact_status %q", i, raw.FactStatus)
		}
		coverage := strings.ToLower(strings.TrimSpace(raw.Coverage))
		if !validEvidenceCoverage(coverage) {
			return nil, fmt.Errorf("evidence record %d has invalid coverage %q", i, raw.Coverage)
		}
		if factStatus == EvidenceFactUnknown && coverage == EvidenceCoverageComplete {
			return nil, fmt.Errorf("evidence record %d cannot mark an unknown fact as complete coverage", i)
		}
		if raw.PartialResult && coverage == EvidenceCoverageComplete {
			return nil, fmt.Errorf("evidence record %d partial_result cannot have complete coverage", i)
		}
		sourceRefs, err := normalizeSemanticRefs(fmt.Sprintf("evidence record %d source_refs", i), raw.SourceRefs, maxReviewEvidenceRefs)
		if err != nil {
			return nil, err
		}
		if factStatus != EvidenceFactUnknown && len(sourceRefs) == 0 {
			return nil, fmt.Errorf("evidence record %d fact_status %s requires at least one source_ref", i, factStatus)
		}
		limitations, err := normalizeSemanticTexts(fmt.Sprintf("evidence record %d limitations", i), raw.Limitations, maxEvidenceLimitations, maxEvidenceLimitationBytes)
		if err != nil {
			return nil, err
		}
		out = append(out, EvidenceRecordInput{
			Statement: statement, FactStatus: factStatus, SourceRefs: sourceRefs,
			Coverage: coverage, Limitations: limitations,
			UntrustedContent: raw.UntrustedContent, PartialResult: raw.PartialResult,
		})
	}
	return out, nil
}

func validEvidenceFactStatus(value string) bool {
	switch value {
	case EvidenceFactObserved, EvidenceFactDerived, EvidenceFactInferred, EvidenceFactUnknown, EvidenceFactContradicted:
		return true
	default:
		return false
	}
}

func validEvidenceCoverage(value string) bool {
	switch value {
	case EvidenceCoverageComplete, EvidenceCoveragePartial, EvidenceCoverageUnknown:
		return true
	default:
		return false
	}
}

func normalizeSemanticRefs(label string, refs []string, maxItems int) ([]string, error) {
	if len(refs) > maxItems {
		return nil, fmt.Errorf("%s cannot exceed %d", label, maxItems)
	}
	seen := make(map[string]struct{}, len(refs))
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if strings.TrimSpace(ref) != ref || ref == "" || strings.ContainsAny(ref, "\r\n") || len([]rune(ref)) > maxReviewEvidenceRefRunes {
			return nil, fmt.Errorf("%s has invalid ref %q", label, ref)
		}
		if _, ok := seen[ref]; ok {
			return nil, fmt.Errorf("%s has duplicate ref %q", label, ref)
		}
		seen[ref] = struct{}{}
		out = append(out, ref)
	}
	return out, nil
}

func normalizeSemanticTexts(label string, values []string, maxItems, maxBytes int) ([]string, error) {
	if len(values) > maxItems {
		return nil, fmt.Errorf("%s cannot exceed %d", label, maxItems)
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || trimmed != value || strings.ContainsAny(value, "\r\n") || len([]byte(value)) > maxBytes {
			return nil, fmt.Errorf("%s contains invalid text", label)
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("%s contains duplicate text", label)
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out, nil
}

func applyEvidenceSemantics(task *Task, step *TaskStep, records []EvidenceRecordInput, updates []UnknownUpdate, now time.Time) error {
	normalizedRecords, err := normalizeEvidenceRecordInputs(records)
	if err != nil {
		return err
	}
	if len(step.EvidenceRecords)+len(normalizedRecords) > maxEvidenceRecordsPerStep {
		return fmt.Errorf("step %s evidence records cannot exceed %d", step.ID, maxEvidenceRecordsPerStep)
	}
	for _, input := range normalizedRecords {
		id, err := newOpaqueID("ev_")
		if err != nil {
			return err
		}
		step.EvidenceRecords = append(step.EvidenceRecords, EvidenceRecord{
			ID: id, Statement: input.Statement, FactStatus: input.FactStatus,
			SourceRefs: input.SourceRefs, Coverage: input.Coverage, Limitations: input.Limitations,
			UntrustedContent: input.UntrustedContent, PartialResult: input.PartialResult, CreatedAt: now,
		})
	}
	return applyUnknownUpdates(task, step, updates, now)
}

func applyUnknownUpdates(task *Task, step *TaskStep, updates []UnknownUpdate, now time.Time) error {
	if len(updates) > 16 {
		return errors.New("unknown_updates cannot exceed 16 per checkpoint")
	}
	for i, update := range updates {
		action := strings.ToLower(strings.TrimSpace(update.Action))
		switch action {
		case "open":
			if len(task.Unknowns) >= maxTaskUnknowns {
				return fmt.Errorf("task unknown ledger cannot exceed %d records", maxTaskUnknowns)
			}
			question := strings.TrimSpace(update.Question)
			if question == "" || question != update.Question || strings.ContainsAny(question, "\r\n") || len([]byte(question)) > maxUnknownQuestionBytes {
				return fmt.Errorf("unknown update %d has invalid question", i)
			}
			if strings.TrimSpace(update.UnknownID) != "" || update.ExpectedRevision != 0 || strings.TrimSpace(update.Resolution) != "" {
				return fmt.Errorf("unknown update %d open cannot set unknown_id, expected_revision, or resolution", i)
			}
			refs, err := normalizeSemanticRefs(fmt.Sprintf("unknown update %d evidence_refs", i), update.EvidenceRefs, maxReviewEvidenceRefs)
			if err != nil {
				return err
			}
			id, err := newOpaqueID("unk_")
			if err != nil {
				return err
			}
			task.Unknowns = append(task.Unknowns, UnknownRecord{
				ID: id, Question: question, Blocking: update.Blocking, Status: UnknownStatusOpen,
				Revision: 1, OpenedStepID: step.ID, EvidenceRefs: refs, OpenedAt: now,
			})
		case "resolve":
			id := strings.TrimSpace(update.UnknownID)
			if id == "" || id != update.UnknownID || !validOpaqueEvidenceID(id, "unk_") {
				return fmt.Errorf("unknown update %d has invalid unknown_id", i)
			}
			if update.ExpectedRevision < 1 {
				return fmt.Errorf("unknown update %d resolve requires expected_revision", i)
			}
			resolution := strings.TrimSpace(update.Resolution)
			if resolution == "" || resolution != update.Resolution || strings.ContainsAny(resolution, "\r\n") || len([]byte(resolution)) > maxUnknownResolutionBytes {
				return fmt.Errorf("unknown update %d has invalid resolution", i)
			}
			if strings.TrimSpace(update.Question) != "" {
				return fmt.Errorf("unknown update %d resolve cannot replace question", i)
			}
			refs, err := normalizeSemanticRefs(fmt.Sprintf("unknown update %d evidence_refs", i), update.EvidenceRefs, maxReviewEvidenceRefs)
			if err != nil {
				return err
			}
			if len(refs) == 0 {
				return fmt.Errorf("unknown update %d resolve requires evidence_refs", i)
			}
			index := -1
			for j := range task.Unknowns {
				if task.Unknowns[j].ID == id {
					index = j
					break
				}
			}
			if index < 0 {
				return fmt.Errorf("unknown %s not found", id)
			}
			current := &task.Unknowns[index]
			if current.Status != UnknownStatusOpen {
				return fmt.Errorf("unknown %s is already resolved", id)
			}
			if current.Revision != update.ExpectedRevision {
				return fmt.Errorf("unknown %s revision mismatch: expected %d, current %d", id, update.ExpectedRevision, current.Revision)
			}
			owned := map[string]struct{}{}
			for _, ref := range stepOutcomeEvidenceRefs(*step) {
				owned[ref] = struct{}{}
			}
			for _, ref := range refs {
				if _, ok := owned[ref]; !ok {
					return fmt.Errorf("unknown %s resolution evidence ref %q is not owned by step %s", id, ref, step.ID)
				}
			}
			current.Status = UnknownStatusResolved
			current.Revision++
			current.Resolution = resolution
			current.ResolutionEvidenceRefs = refs
			current.ResolvedStepID = step.ID
			resolvedAt := now
			current.ResolvedAt = &resolvedAt
		default:
			return fmt.Errorf("unknown update %d action must be open or resolve", i)
		}
	}
	return nil
}

func normalizeAcknowledgedUnknownIDs(task Task, status string, ids []string) ([]string, error) {
	if status != FinalReviewPass && len(ids) > 0 {
		return nil, errors.New("acknowledged_unknown_ids are only valid for a passing final review")
	}
	normalized, err := normalizeSemanticRefs("acknowledged_unknown_ids", ids, maxTaskUnknowns)
	if err != nil {
		return nil, err
	}
	if status != FinalReviewPass {
		return nil, nil
	}
	byID := make(map[string]UnknownRecord, len(task.Unknowns))
	for _, unknown := range task.Unknowns {
		byID[unknown.ID] = unknown
	}
	acknowledged := make(map[string]struct{}, len(normalized))
	for _, id := range normalized {
		unknown, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("acknowledged unknown %s not found", id)
		}
		if unknown.Status != UnknownStatusOpen {
			return nil, fmt.Errorf("acknowledged unknown %s is not open", id)
		}
		if unknown.Blocking {
			return nil, fmt.Errorf("blocking unknown %s cannot be acknowledged past final review", id)
		}
		acknowledged[id] = struct{}{}
	}
	for _, unknown := range task.Unknowns {
		if unknown.Status != UnknownStatusOpen {
			continue
		}
		if unknown.Blocking {
			return nil, fmt.Errorf("passing final review is blocked by unresolved unknown %s", unknown.ID)
		}
		if _, ok := acknowledged[unknown.ID]; !ok {
			return nil, fmt.Errorf("passing final review must explicitly acknowledge unresolved unknown %s", unknown.ID)
		}
	}
	return normalized, nil
}

func validOpaqueEvidenceID(id, prefix string) bool {
	if !strings.HasPrefix(id, prefix) || len(id) != len(prefix)+16 {
		return false
	}
	for _, ch := range id[len(prefix):] {
		if !strings.ContainsRune("0123456789abcdef", ch) {
			return false
		}
	}
	return true
}
