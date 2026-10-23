package taskstate

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	ImprovementSchemaVersion = 1

	ImprovementModeParallel   = "parallel"
	ImprovementModeSequential = "sequential"

	ImprovementStatusOpen     = "open"
	ImprovementStatusSelected = "selected"
	ImprovementStatusComposed = "composed"

	ImprovementCandidatePending = "pending"
	ImprovementCandidateProbed  = "probed"

	ImprovementScopeBase        = "base"
	ImprovementScopeInvariant   = "invariant"
	ImprovementScopeProject     = "project"
	ImprovementScopeAlternative = "alternative"

	ImprovementSurfacePolicyCostAwareV1 = "cost_aware_v1"
	ImprovementIsolationBlindAliasV1    = "blind_alias_v1"

	maxImprovementCandidates       = 8
	maxImprovementProbeCriteria    = 12
	maxImprovementEvidenceRefs     = 16
	maxImprovementDescriptionBytes = 4 << 10
)

type ImprovementPlanInput struct {
	Target        string   `json:"target"`
	Mode          string   `json:"mode"`
	ProbeCriteria []string `json:"probe_criteria"`
	SurfacePolicy string   `json:"surface_policy,omitempty"`
}

type ImprovementCandidateInput struct {
	Scope            string `json:"scope"`
	Description      string `json:"description"`
	ArtifactRef      string `json:"artifact_ref"`
	BlindArtifactRef string `json:"blind_artifact_ref"`
	ParentID         string `json:"parent_id,omitempty"`
}

type ImprovementProbeInput struct {
	CandidateID  string   `json:"candidate_id"`
	Summary      string   `json:"summary"`
	EvidenceRefs []string `json:"evidence_refs"`
}

type ImprovementSelectionInput struct {
	Aliases      []string `json:"aliases"`
	Rationale    string   `json:"rationale"`
	EvidenceRefs []string `json:"evidence_refs"`
}

type ImprovementCompositionInput struct {
	Summary      string   `json:"summary"`
	Lesson       string   `json:"lesson,omitempty"`
	ArtifactRef  string   `json:"artifact_ref"`
	EvidenceRefs []string `json:"evidence_refs"`
}

type ImprovementUpdate struct {
	Action      string                       `json:"action"`
	Candidate   *ImprovementCandidateInput   `json:"candidate,omitempty"`
	Probe       *ImprovementProbeInput       `json:"probe,omitempty"`
	Selection   *ImprovementSelectionInput   `json:"selection,omitempty"`
	Composition *ImprovementCompositionInput `json:"composition,omitempty"`
}

type ImprovementCandidate struct {
	ID               string    `json:"id"`
	BlindAlias       string    `json:"blind_alias"`
	Scope            string    `json:"scope"`
	Description      string    `json:"description"`
	ArtifactRef      string    `json:"artifact_ref"`
	BlindArtifactRef string    `json:"blind_artifact_ref"`
	ParentID         string    `json:"parent_id,omitempty"`
	Status           string    `json:"status"`
	ProbeSummary     string    `json:"probe_summary,omitempty"`
	EvidenceRefs     []string  `json:"evidence_refs,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type ImprovementSelection struct {
	BlindAliases []string  `json:"blind_aliases"`
	CandidateIDs []string  `json:"candidate_ids"`
	Rationale    string    `json:"rationale"`
	EvidenceRefs []string  `json:"evidence_refs"`
	SelectedAt   time.Time `json:"selected_at"`
}

type ImprovementComposition struct {
	SourceCandidateIDs []string  `json:"source_candidate_ids"`
	Summary            string    `json:"summary"`
	Lesson             string    `json:"lesson,omitempty"`
	ArtifactRef        string    `json:"artifact_ref"`
	EvidenceRefs       []string  `json:"evidence_refs"`
	ComposedAt         time.Time `json:"composed_at"`
}

type ImprovementExperiment struct {
	SchemaVersion     int                     `json:"schema_version"`
	Target            string                  `json:"target"`
	Mode              string                  `json:"mode"`
	ProbeCriteria     []string                `json:"probe_criteria"`
	SurfacePolicy     string                  `json:"surface_policy"`
	EvidenceIsolation string                  `json:"evidence_isolation"`
	Status            string                  `json:"status"`
	Candidates        []ImprovementCandidate  `json:"candidates,omitempty"`
	Selection         *ImprovementSelection   `json:"selection,omitempty"`
	Composition       *ImprovementComposition `json:"composition,omitempty"`
	CreatedAt         time.Time               `json:"created_at"`
	UpdatedAt         time.Time               `json:"updated_at"`
}

type BlindImprovementCandidate struct {
	Alias         string `json:"alias"`
	ArtifactRef   string `json:"artifact_ref"`
	ProbeSummary  string `json:"probe_summary"`
	EvidenceCount int    `json:"evidence_count"`
}

type ImprovementBlindView struct {
	Target        string                      `json:"target"`
	Mode          string                      `json:"mode"`
	ProbeCriteria []string                    `json:"probe_criteria"`
	Candidates    []BlindImprovementCandidate `json:"candidates"`
}

func normalizeImprovementPlan(input *ImprovementPlanInput, now time.Time) (*ImprovementExperiment, error) {
	if input == nil {
		return nil, nil
	}
	target := strings.TrimSpace(input.Target)
	mode := strings.ToLower(strings.TrimSpace(input.Mode))
	criteria := normalizeTexts(input.ProbeCriteria)
	surfacePolicy := strings.ToLower(strings.TrimSpace(input.SurfacePolicy))
	if target == "" {
		return nil, errors.New("improvement_experiment target is required")
	}
	if err := validateTextLimit("improvement target", target, maxTaskTitleBytes); err != nil {
		return nil, err
	}
	if mode != ImprovementModeParallel && mode != ImprovementModeSequential {
		return nil, errors.New("improvement_experiment mode must be parallel or sequential")
	}
	if len(criteria) == 0 {
		return nil, errors.New("improvement_experiment requires probe_criteria")
	}
	if len(criteria) > maxImprovementProbeCriteria {
		return nil, fmt.Errorf("improvement probe_criteria cannot exceed %d", maxImprovementProbeCriteria)
	}
	for _, criterion := range criteria {
		if err := validateTextLimit("improvement probe criterion", criterion, maxTaskConditionBytes); err != nil {
			return nil, err
		}
	}
	if surfacePolicy == "" {
		surfacePolicy = ImprovementSurfacePolicyCostAwareV1
	}
	if surfacePolicy != ImprovementSurfacePolicyCostAwareV1 {
		return nil, errors.New("unsupported improvement surface_policy")
	}
	return &ImprovementExperiment{
		SchemaVersion:     ImprovementSchemaVersion,
		Target:            target,
		Mode:              mode,
		ProbeCriteria:     criteria,
		SurfacePolicy:     surfacePolicy,
		EvidenceIsolation: ImprovementIsolationBlindAliasV1,
		Status:            ImprovementStatusOpen,
		CreatedAt:         now,
		UpdatedAt:         now,
	}, nil
}

func (s *Store) UpdateImprovement(id string, update ImprovementUpdate) (Task, error) {
	return s.mutate(id, func(task *Task, now time.Time) error {
		if err := requireActive(task); err != nil {
			return err
		}
		if err := requireFinalReviewOpen(task); err != nil {
			return err
		}
		exp := task.ImprovementExperiment
		if exp == nil {
			return errors.New("task has no improvement_experiment")
		}
		action := strings.ToLower(strings.TrimSpace(update.Action))
		switch action {
		case "register_candidate":
			if update.Candidate == nil || update.Probe != nil || update.Selection != nil || update.Composition != nil {
				return errors.New("register_candidate requires only candidate")
			}
			if exp.Status != ImprovementStatusOpen {
				return errors.New("cannot register candidate after selection")
			}
			candidate, err := normalizeImprovementCandidate(task.ID, *exp, *update.Candidate, now)
			if err != nil {
				return err
			}
			exp.Candidates = append(exp.Candidates, candidate)
			appendTaskEvent(task, Event{Type: "improvement_candidate_registered", Summary: candidate.ID + " as " + candidate.BlindAlias, CreatedAt: now})
		case "record_probe":
			if update.Probe == nil || update.Candidate != nil || update.Selection != nil || update.Composition != nil {
				return errors.New("record_probe requires only probe")
			}
			if exp.Status != ImprovementStatusOpen {
				return errors.New("cannot record probe after selection")
			}
			if err := applyImprovementProbe(exp, *update.Probe, now); err != nil {
				return err
			}
			appendTaskEvent(task, Event{Type: "improvement_probe_recorded", Summary: strings.TrimSpace(update.Probe.CandidateID), CreatedAt: now})
		case "select":
			if update.Selection == nil || update.Candidate != nil || update.Probe != nil || update.Composition != nil {
				return errors.New("select requires only selection")
			}
			if exp.Status != ImprovementStatusOpen {
				return errors.New("improvement selection is already frozen")
			}
			selection, err := normalizeImprovementSelection(*exp, *update.Selection, now)
			if err != nil {
				return err
			}
			exp.Selection = selection
			exp.Status = ImprovementStatusSelected
			appendTaskEvent(task, Event{Type: "improvement_selection_frozen", Summary: strings.Join(selection.BlindAliases, ","), CreatedAt: now})
		case "compose":
			if update.Composition == nil || update.Candidate != nil || update.Probe != nil || update.Selection != nil {
				return errors.New("compose requires only composition")
			}
			if exp.Status != ImprovementStatusSelected || exp.Selection == nil {
				return errors.New("compose requires a frozen blind selection")
			}
			composition, err := normalizeImprovementComposition(*exp, *update.Composition, now)
			if err != nil {
				return err
			}
			exp.Composition = composition
			exp.Status = ImprovementStatusComposed
			appendTaskEvent(task, Event{Type: "improvement_composed", Summary: composition.Summary, CreatedAt: now})
		default:
			return errors.New("improvement_update action must be register_candidate, record_probe, select, or compose")
		}
		exp.UpdatedAt = now
		clearHandoff(task)
		task.Summary = "improvement " + action
		return nil
	})
}

func normalizeImprovementCandidate(taskID string, exp ImprovementExperiment, input ImprovementCandidateInput, now time.Time) (ImprovementCandidate, error) {
	if len(exp.Candidates) >= maxImprovementCandidates {
		return ImprovementCandidate{}, fmt.Errorf("improvement candidates cannot exceed %d", maxImprovementCandidates)
	}
	scope := strings.ToLower(strings.TrimSpace(input.Scope))
	if !oneOf(scope, ImprovementScopeBase, ImprovementScopeInvariant, ImprovementScopeProject, ImprovementScopeAlternative) {
		return ImprovementCandidate{}, errors.New("improvement candidate scope must be base, invariant, project, or alternative")
	}
	description := strings.TrimSpace(input.Description)
	if description == "" {
		return ImprovementCandidate{}, errors.New("improvement candidate description is required")
	}
	if err := validateTextLimit("improvement candidate description", description, maxImprovementDescriptionBytes); err != nil {
		return ImprovementCandidate{}, err
	}
	artifactRef := strings.TrimSpace(input.ArtifactRef)
	blindArtifactRef := strings.TrimSpace(input.BlindArtifactRef)
	if artifactRef == "" || strings.ContainsAny(artifactRef, "\r\n") {
		return ImprovementCandidate{}, errors.New("improvement candidate artifact_ref is required and must be single-line")
	}
	if blindArtifactRef == "" || strings.ContainsAny(blindArtifactRef, "\r\n") {
		return ImprovementCandidate{}, errors.New("improvement candidate blind_artifact_ref is required and must be single-line")
	}
	if blindArtifactRef == artifactRef {
		return ImprovementCandidate{}, errors.New("blind_artifact_ref must be a separately staged identity-neutral artifact")
	}
	parentID := strings.TrimSpace(input.ParentID)
	if exp.Mode == ImprovementModeParallel && parentID != "" {
		return ImprovementCandidate{}, errors.New("parallel improvement candidates cannot declare parent_id")
	}
	if exp.Mode == ImprovementModeSequential {
		if len(exp.Candidates) == 0 {
			if parentID != "" {
				return ImprovementCandidate{}, errors.New("first sequential candidate cannot declare parent_id")
			}
		} else {
			if parentID == "" {
				return ImprovementCandidate{}, errors.New("later sequential candidates require parent_id")
			}
			latest := exp.Candidates[len(exp.Candidates)-1]
			if parentID != latest.ID {
				return ImprovementCandidate{}, errors.New("sequential parent_id must reference the latest candidate")
			}
			if latest.Status != ImprovementCandidateProbed {
				return ImprovementCandidate{}, errors.New("sequential parent candidate must be probed before refinement")
			}
		}
	}
	id, err := newOpaqueID("imp_")
	if err != nil {
		return ImprovementCandidate{}, err
	}
	alias := blindImprovementAlias(taskID, id)
	for _, candidate := range exp.Candidates {
		if candidate.BlindAlias == alias {
			alias = blindImprovementAlias(taskID+"|collision", id)
			break
		}
	}
	return ImprovementCandidate{
		ID: id, BlindAlias: alias, Scope: scope, Description: description, ArtifactRef: artifactRef, BlindArtifactRef: blindArtifactRef,
		ParentID: parentID, Status: ImprovementCandidatePending, CreatedAt: now, UpdatedAt: now,
	}, nil
}

func applyImprovementProbe(exp *ImprovementExperiment, input ImprovementProbeInput, now time.Time) error {
	candidateID := strings.TrimSpace(input.CandidateID)
	summary := strings.TrimSpace(input.Summary)
	if candidateID == "" || summary == "" {
		return errors.New("improvement probe candidate_id and summary are required")
	}
	if err := validateTextLimit("improvement probe summary", summary, maxTaskSummaryBytes); err != nil {
		return err
	}
	refs, err := normalizeSemanticRefs("improvement probe evidence_refs", input.EvidenceRefs, maxImprovementEvidenceRefs)
	if err != nil {
		return err
	}
	if len(refs) == 0 {
		return errors.New("improvement probe requires evidence_refs")
	}
	for i := range exp.Candidates {
		if exp.Candidates[i].ID != candidateID {
			continue
		}
		if strings.Contains(summary, exp.Candidates[i].ID) || strings.Contains(summary, exp.Candidates[i].BlindAlias) {
			return errors.New("improvement probe summary must describe behavior without candidate identity")
		}
		if exp.Candidates[i].Status == ImprovementCandidateProbed {
			if exp.Candidates[i].ProbeSummary == summary && sameStringSet(exp.Candidates[i].EvidenceRefs, refs) {
				return nil
			}
			return errors.New("improvement candidate probe is already frozen")
		}
		exp.Candidates[i].ProbeSummary = summary
		exp.Candidates[i].EvidenceRefs = refs
		exp.Candidates[i].Status = ImprovementCandidateProbed
		exp.Candidates[i].UpdatedAt = now
		return nil
	}
	return errors.New("improvement probe candidate_id not found")
}

func normalizeImprovementSelection(exp ImprovementExperiment, input ImprovementSelectionInput, now time.Time) (*ImprovementSelection, error) {
	if len(exp.Candidates) == 0 {
		return nil, errors.New("improvement selection requires candidates")
	}
	for _, candidate := range exp.Candidates {
		if candidate.Status != ImprovementCandidateProbed {
			return nil, errors.New("all improvement candidates must be probed before blind selection")
		}
	}
	aliases := normalizeTexts(input.Aliases)
	if len(aliases) == 0 {
		return nil, errors.New("improvement selection requires aliases")
	}
	if exp.Mode == ImprovementModeSequential && len(aliases) != 1 {
		return nil, errors.New("sequential improvement selection must choose exactly one alias")
	}
	if len(aliases) > len(exp.Candidates) {
		return nil, errors.New("improvement selection aliases exceed candidate count")
	}
	rationale := strings.TrimSpace(input.Rationale)
	if rationale == "" {
		return nil, errors.New("improvement selection rationale is required")
	}
	if err := validateTextLimit("improvement selection rationale", rationale, maxTaskSummaryBytes); err != nil {
		return nil, err
	}
	refs, err := normalizeSemanticRefs("improvement selection evidence_refs", input.EvidenceRefs, maxImprovementEvidenceRefs)
	if err != nil {
		return nil, err
	}
	if len(refs) == 0 {
		return nil, errors.New("improvement selection requires evidence_refs")
	}
	byAlias := make(map[string]string, len(exp.Candidates))
	for _, candidate := range exp.Candidates {
		byAlias[candidate.BlindAlias] = candidate.ID
	}
	ids := make([]string, 0, len(aliases))
	seen := make(map[string]struct{}, len(aliases))
	for _, alias := range aliases {
		if _, duplicate := seen[alias]; duplicate {
			return nil, errors.New("improvement selection aliases must be unique")
		}
		seen[alias] = struct{}{}
		id, ok := byAlias[alias]
		if !ok {
			return nil, fmt.Errorf("unknown blind improvement alias %q", alias)
		}
		ids = append(ids, id)
	}
	return &ImprovementSelection{BlindAliases: aliases, CandidateIDs: ids, Rationale: rationale, EvidenceRefs: refs, SelectedAt: now}, nil
}

func normalizeImprovementComposition(exp ImprovementExperiment, input ImprovementCompositionInput, now time.Time) (*ImprovementComposition, error) {
	summary := strings.TrimSpace(input.Summary)
	lesson := strings.TrimSpace(input.Lesson)
	artifactRef := strings.TrimSpace(input.ArtifactRef)
	if summary == "" || artifactRef == "" || strings.ContainsAny(artifactRef, "\r\n") {
		return nil, errors.New("improvement composition summary and artifact_ref are required")
	}
	if err := validateTextLimit("improvement composition summary", summary, maxTaskSummaryBytes); err != nil {
		return nil, err
	}
	if lesson != "" {
		if err := validateTextLimit("improvement composition lesson", lesson, maxTaskConditionBytes); err != nil {
			return nil, err
		}
	}
	refs, err := normalizeSemanticRefs("improvement composition evidence_refs", input.EvidenceRefs, maxImprovementEvidenceRefs)
	if err != nil {
		return nil, err
	}
	if len(refs) == 0 {
		return nil, errors.New("improvement composition requires evidence_refs")
	}
	return &ImprovementComposition{
		SourceCandidateIDs: append([]string(nil), exp.Selection.CandidateIDs...),
		Summary:            summary, Lesson: lesson, ArtifactRef: artifactRef, EvidenceRefs: refs, ComposedAt: now,
	}, nil
}

func ImprovementBlindProjection(task Task) (*ImprovementBlindView, error) {
	exp := task.ImprovementExperiment
	if exp == nil {
		return nil, errors.New("task has no improvement_experiment")
	}
	candidates := make([]BlindImprovementCandidate, 0, len(exp.Candidates))
	for _, candidate := range exp.Candidates {
		if candidate.Status != ImprovementCandidateProbed {
			continue
		}
		candidates = append(candidates, BlindImprovementCandidate{
			Alias: candidate.BlindAlias, ArtifactRef: candidate.BlindArtifactRef, ProbeSummary: candidate.ProbeSummary,
			EvidenceCount: len(candidate.EvidenceRefs),
		})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Alias < candidates[j].Alias })
	return &ImprovementBlindView{
		Target: exp.Target, Mode: exp.Mode, ProbeCriteria: append([]string(nil), exp.ProbeCriteria...), Candidates: candidates,
	}, nil
}

func improvementCandidateByID(candidates []ImprovementCandidate, id string) (ImprovementCandidate, bool) {
	for _, candidate := range candidates {
		if candidate.ID == id {
			return candidate, true
		}
	}
	return ImprovementCandidate{}, false
}

func blindImprovementAlias(taskID, candidateID string) string {
	sum := sha256.Sum256([]byte("agentdock-improvement-blind-v1|" + taskID + "|" + candidateID))
	return "blind_" + hex.EncodeToString(sum[:6])
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[string]int, len(a))
	for _, value := range a {
		counts[value]++
	}
	for _, value := range b {
		counts[value]--
		if counts[value] < 0 {
			return false
		}
	}
	for _, count := range counts {
		if count != 0 {
			return false
		}
	}
	return true
}
