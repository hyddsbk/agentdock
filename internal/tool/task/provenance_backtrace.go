package task

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	evolutionpkg "github.com/uvwt/agentdock/internal/evolution"
	"github.com/uvwt/agentdock/internal/taskstate"
)

const provenanceBacktraceContract = "provenance-backtrace/v1"

type ProvenanceRoot struct {
	Kind           string `json:"kind"`
	Ref            string `json:"ref"`
	NodeID         string `json:"node_id"`
	TaskID         string `json:"task_id"`
	EvolutionID    string `json:"evolution_id,omitempty"`
	ReviewRevision string `json:"review_revision"`
}

type ProvenanceNode struct {
	ID             string `json:"id"`
	Kind           string `json:"kind"`
	Ref            string `json:"ref,omitempty"`
	Label          string `json:"label,omitempty"`
	Status         string `json:"status,omitempty"`
	Scope          string `json:"scope,omitempty"`
	StepID         string `json:"step_id,omitempty"`
	EvolutionID    string `json:"evolution_id,omitempty"`
	ReviewRevision string `json:"review_revision,omitempty"`
	Relation       string `json:"relation,omitempty"`
	Revision       int64  `json:"revision,omitempty"`
	Issuer         string `json:"issuer,omitempty"`
	CommandSHA256  string `json:"command_sha256,omitempty"`
	ReceiptSHA256  string `json:"receipt_sha256,omitempty"`
	ExitCode       *int   `json:"exit_code,omitempty"`
	TimedOut       *bool  `json:"timed_out,omitempty"`
	StartedAt      string `json:"started_at,omitempty"`
	FinishedAt     string `json:"finished_at,omitempty"`
	ClaimedAt      string `json:"claimed_at,omitempty"`
}

type ProvenanceEdge struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Relation string `json:"relation"`
}

type ProvenancePath struct {
	NodeIDs []string `json:"node_ids"`
}

type ProvenanceBacktrace struct {
	Contract       string           `json:"contract"`
	Root           ProvenanceRoot   `json:"root"`
	Accepted       bool             `json:"accepted"`
	Verdict        string           `json:"verdict"`
	TaskID         string           `json:"task_id"`
	ReviewRevision string           `json:"review_revision"`
	ReviewStatus   string           `json:"review_status"`
	Nodes          []ProvenanceNode `json:"nodes"`
	Edges          []ProvenanceEdge `json:"edges"`
	Paths          []ProvenancePath `json:"paths"`
}

type reviewClaim struct {
	Ref   string
	Scope string
	Claim string
}

type evolutionEvidenceMatch struct {
	Record   evolutionpkg.Record
	Evidence evolutionpkg.Evidence
	Index    int
}

func (s *Service) provenanceBacktrace(ctx context.Context, raw *ProvenanceQueryRequest) (ProvenanceBacktrace, error) {
	query, err := normalizeProvenanceQuery(raw)
	if err != nil {
		return ProvenanceBacktrace{}, err
	}

	var (
		task       taskstate.Task
		claim      reviewClaim
		bindings   []taskstate.OutcomeProvenanceBinding
		rootNodeID string
		evoMatch   *evolutionEvidenceMatch
	)

	switch query.Kind {
	case "final_review_claim":
		taskID := query.TaskID
		if parsedTaskID, _, _, _, parseErr := parseReviewEvidenceRef(query.Ref); parseErr == nil {
			if taskID != "" && taskID != parsedTaskID {
				return ProvenanceBacktrace{}, errors.New("cross-task final review claim scope")
			}
			taskID = parsedTaskID
		}
		if taskID == "" {
			return ProvenanceBacktrace{}, errors.New("task_id is required for a plain-text final_review_claim")
		}
		task, err = s.tasks.Get(taskID)
		if err != nil {
			return ProvenanceBacktrace{}, err
		}
		if err := validatePassingReview(task, query.ReviewRevision); err != nil {
			return ProvenanceBacktrace{}, err
		}
		claim, err = resolveReviewClaim(task, query.Ref)
		if err != nil {
			return ProvenanceBacktrace{}, err
		}
		bindings = bindingsForClaim(task, claim)
		if len(bindings) == 0 {
			return ProvenanceBacktrace{}, errors.New("final review claim is not bound to passing provenance")
		}
		rootNodeID = provenanceNodeID("final_review_claim", claim.Ref)

	case "requirement":
		if query.TaskID == "" {
			return ProvenanceBacktrace{}, errors.New("task_id is required for requirement backtrace")
		}
		task, err = s.tasks.Get(query.TaskID)
		if err != nil {
			return ProvenanceBacktrace{}, err
		}
		if err := validatePassingReview(task, query.ReviewRevision); err != nil {
			return ProvenanceBacktrace{}, err
		}
		if !stringIn(query.Ref, task.RequirementRefs) {
			return ProvenanceBacktrace{}, errors.New("requirement ref does not belong to task")
		}
		bindings = bindingsForRequirement(task, query.Ref)
		if len(bindings) == 0 {
			return ProvenanceBacktrace{}, errors.New("requirement ref is not bound to passing final_review provenance")
		}
		rootNodeID = provenanceNodeID("requirement", task.ID+"|"+query.Ref)

	case "exec_receipt":
		if s.receipts == nil {
			return ProvenanceBacktrace{}, errors.New("execution evidence store is not configured")
		}
		if !strings.HasPrefix(query.Ref, "exec://") {
			return ProvenanceBacktrace{}, errors.New("exec_receipt root must use exec://")
		}
		if _, err := s.receipts.Resolve(query.Ref); err != nil {
			return ProvenanceBacktrace{}, fmt.Errorf("resolve execution receipt: %w", err)
		}
		owner, err := s.receipts.ResolveClaim(query.Ref)
		if err != nil {
			return ProvenanceBacktrace{}, fmt.Errorf("resolve execution receipt claim: %w", err)
		}
		taskID, stepID, err := parseTaskStepClaimKey(owner.ClaimKey)
		if err != nil {
			return ProvenanceBacktrace{}, err
		}
		if query.TaskID != "" && query.TaskID != taskID {
			return ProvenanceBacktrace{}, errors.New("cross-task exec receipt scope")
		}
		task, err = s.tasks.Get(taskID)
		if err != nil {
			return ProvenanceBacktrace{}, err
		}
		if err := validatePassingReview(task, query.ReviewRevision); err != nil {
			return ProvenanceBacktrace{}, err
		}
		step, ok := taskStepByID(task, stepID)
		if !ok || step.Status != taskstate.StepCompleted || !stringIn(query.Ref, taskstate.OutcomeEvidenceRefs(step)) {
			return ProvenanceBacktrace{}, errors.New("execution receipt claim points to a dangling or non-completed task step")
		}
		bindings = bindingsForEvidence(task, query.Ref, stepID)
		if len(bindings) == 0 {
			return ProvenanceBacktrace{}, errors.New("execution receipt was not used by passing final_review provenance")
		}
		rootNodeID = provenanceNodeID("execution_receipt", query.Ref)

	case "evolution_evidence":
		provider, ok := s.evolution.(EvolutionEvidenceProvider)
		if !ok || provider == nil {
			return ProvenanceBacktrace{}, errors.New("Evolution evidence backtrace is unavailable")
		}
		records, err := provider.EvidenceRecords(ctx, query.Ref)
		if err != nil {
			return ProvenanceBacktrace{}, err
		}
		matches := make([]evolutionEvidenceMatch, 0, 2)
		for _, record := range records {
			if query.EvolutionID != "" && record.EvolutionID != query.EvolutionID {
				continue
			}
			for i, evidence := range record.Evidence {
				if evidence.Ref != query.Ref {
					continue
				}
				if query.TaskID != "" && evidence.TaskID != query.TaskID {
					continue
				}
				if query.ReviewRevision != "" && evidence.ReviewRevision != query.ReviewRevision {
					continue
				}
				matches = append(matches, evolutionEvidenceMatch{Record: record, Evidence: evidence, Index: i})
			}
		}
		if len(matches) == 0 {
			return ProvenanceBacktrace{}, errors.New("Evolution evidence ref was not found in the requested scope")
		}
		if len(matches) != 1 {
			return ProvenanceBacktrace{}, errors.New("ambiguous Evolution evidence ref; add evolution_id, task_id, or review_revision")
		}
		evoMatch = &matches[0]
		if strings.TrimSpace(evoMatch.Evidence.TaskID) == "" || strings.TrimSpace(evoMatch.Evidence.ReviewRevision) == "" {
			return ProvenanceBacktrace{}, errors.New("Evolution evidence is missing task/review provenance")
		}
		task, err = s.tasks.Get(evoMatch.Evidence.TaskID)
		if err != nil {
			return ProvenanceBacktrace{}, err
		}
		if err := validatePassingReview(task, evoMatch.Evidence.ReviewRevision); err != nil {
			return ProvenanceBacktrace{}, err
		}
		claim, err = resolveReviewClaim(task, query.Ref)
		if err != nil {
			return ProvenanceBacktrace{}, err
		}
		bindings = bindingsForClaim(task, claim)
		if len(bindings) == 0 {
			return ProvenanceBacktrace{}, errors.New("Evolution evidence does not resolve to passing review provenance")
		}
		rootNodeID = provenanceEvolutionEvidenceNodeID(*evoMatch)

	default:
		return ProvenanceBacktrace{}, fmt.Errorf("unsupported provenance query kind %q", query.Kind)
	}

	trace, err := s.buildProvenanceGraph(task, query, bindings)
	if err != nil {
		return ProvenanceBacktrace{}, err
	}
	trace.Root.NodeID = rootNodeID
	if evoMatch != nil {
		addEvolutionMatch(&trace, *evoMatch, claim)
		trace.Root.EvolutionID = evoMatch.Record.EvolutionID
	}
	return trace, nil
}

func normalizeProvenanceQuery(raw *ProvenanceQueryRequest) (ProvenanceQueryRequest, error) {
	if raw == nil {
		return ProvenanceQueryRequest{}, errors.New("provenance_query is required")
	}
	query := *raw
	query.Kind = strings.ToLower(strings.TrimSpace(query.Kind))
	query.Ref = strings.TrimSpace(query.Ref)
	query.TaskID = strings.TrimSpace(query.TaskID)
	query.EvolutionID = strings.TrimSpace(query.EvolutionID)
	query.ReviewRevision = strings.TrimSpace(query.ReviewRevision)
	if query.Ref == "" || len(query.Ref) > 2048 || strings.ContainsAny(query.Ref, "\r\n") {
		return ProvenanceQueryRequest{}, errors.New("provenance query ref is invalid")
	}
	switch query.Kind {
	case "final_review_claim", "evolution_evidence", "requirement", "exec_receipt":
	default:
		return ProvenanceQueryRequest{}, fmt.Errorf("unsupported provenance query kind %q", query.Kind)
	}
	return query, nil
}

func validatePassingReview(task taskstate.Task, expectedRevision string) error {
	if task.FinalReview == nil || strings.TrimSpace(task.FinalReview.ReviewRevision) == "" {
		return errors.New("task has no immutable final_review revision")
	}
	if expectedRevision != "" && expectedRevision != task.FinalReview.ReviewRevision {
		return errors.New("review_revision does not match current task final_review")
	}
	if task.FinalReview.Status != taskstate.FinalReviewPass {
		return errors.New("provenance root was not accepted by a passing final_review")
	}
	if task.FinalReview.Provenance == nil {
		return errors.New("passing final_review has no provenance graph")
	}
	return nil
}

func resolveReviewClaim(task taskstate.Task, ref string) (reviewClaim, error) {
	if task.FinalReview == nil {
		return reviewClaim{}, errors.New("task has no final_review")
	}
	if taskID, revision, scope, index, err := parseReviewEvidenceRef(ref); err == nil {
		if taskID != task.ID {
			return reviewClaim{}, errors.New("cross-task final review claim ref")
		}
		if revision != task.FinalReview.ReviewRevision {
			return reviewClaim{}, errors.New("final review claim ref points to a stale review_revision")
		}
		values := reviewScopeValues(task, scope)
		if index < 0 || index >= len(values) {
			return reviewClaim{}, errors.New("final review claim ref index is out of range")
		}
		return reviewClaim{Ref: ref, Scope: scope, Claim: values[index]}, nil
	}

	var scopes []string
	seen := map[string]bool{}
	for _, binding := range task.FinalReview.Provenance.Bindings {
		if binding.Claim != ref || seen[binding.Scope] {
			continue
		}
		seen[binding.Scope] = true
		scopes = append(scopes, binding.Scope)
	}
	if len(scopes) == 0 {
		return reviewClaim{}, errors.New("final review claim was not found")
	}
	if len(scopes) != 1 {
		return reviewClaim{}, errors.New("ambiguous final review claim text; use canonical review claim ref")
	}
	canonical, err := canonicalReviewClaim(task, scopes[0], ref)
	if err != nil {
		return reviewClaim{}, err
	}
	return canonical, nil
}

func parseReviewEvidenceRef(ref string) (taskID, revision, scope string, index int, err error) {
	parts := strings.Split(ref, ":")
	if len(parts) != 6 || parts[0] != "task" || parts[2] != "review" || parts[1] == "" || parts[3] == "" {
		return "", "", "", 0, errors.New("not a canonical final review evidence ref")
	}
	switch parts[4] {
	case "verified", "intent_spec", "engineering_policy", "risk", "missing":
	default:
		return "", "", "", 0, errors.New("unsupported final review evidence scope")
	}
	index, err = strconv.Atoi(parts[5])
	if err != nil || index < 0 {
		return "", "", "", 0, errors.New("invalid final review evidence index")
	}
	return parts[1], parts[3], parts[4], index, nil
}

func canonicalReviewClaim(task taskstate.Task, scope, claim string) (reviewClaim, error) {
	values := reviewScopeValues(task, scope)
	found := -1
	for i, value := range values {
		if value == claim {
			if found >= 0 {
				return reviewClaim{}, errors.New("ambiguous duplicate final review claim")
			}
			found = i
		}
	}
	if found < 0 {
		return reviewClaim{}, errors.New("provenance binding points to a missing final review claim")
	}
	return reviewClaim{
		Ref:   fmt.Sprintf("task:%s:review:%s:%s:%d", task.ID, task.FinalReview.ReviewRevision, scope, found),
		Scope: scope, Claim: claim,
	}, nil
}

func reviewScopeValues(task taskstate.Task, scope string) []string {
	if task.FinalReview == nil {
		return nil
	}
	switch scope {
	case "verified":
		return task.FinalReview.VerifiedFacts
	case "intent_spec":
		if task.FinalReview.IntentSpec != nil {
			return task.FinalReview.IntentSpec.Evidence
		}
	case "engineering_policy":
		if task.FinalReview.EngineeringPolicy != nil {
			return task.FinalReview.EngineeringPolicy.Evidence
		}
	case "risk":
		return task.FinalReview.OpenRisks
	case "missing":
		return task.FinalReview.MissingChecks
	}
	return nil
}

func bindingsForClaim(task taskstate.Task, claim reviewClaim) []taskstate.OutcomeProvenanceBinding {
	var out []taskstate.OutcomeProvenanceBinding
	for _, binding := range task.FinalReview.Provenance.Bindings {
		if binding.Scope == claim.Scope && binding.Claim == claim.Claim {
			out = append(out, binding)
		}
	}
	return out
}

func bindingsForRequirement(task taskstate.Task, ref string) []taskstate.OutcomeProvenanceBinding {
	var out []taskstate.OutcomeProvenanceBinding
	for _, binding := range task.FinalReview.Provenance.Bindings {
		if stringIn(ref, binding.RequirementRefs) {
			out = append(out, binding)
		}
	}
	return out
}

func bindingsForEvidence(task taskstate.Task, ref, ownerStepID string) []taskstate.OutcomeProvenanceBinding {
	var out []taskstate.OutcomeProvenanceBinding
	for _, binding := range task.FinalReview.Provenance.Bindings {
		if binding.StepID == ownerStepID && stringIn(ref, binding.EvidenceRefs) {
			out = append(out, binding)
		}
	}
	return out
}

func (s *Service) buildProvenanceGraph(task taskstate.Task, query ProvenanceQueryRequest, bindings []taskstate.OutcomeProvenanceBinding) (ProvenanceBacktrace, error) {
	review := task.FinalReview
	trace := ProvenanceBacktrace{
		Contract: provenanceBacktraceContract,
		Root:     ProvenanceRoot{Kind: query.Kind, Ref: query.Ref, TaskID: task.ID, ReviewRevision: review.ReviewRevision},
		Accepted: true, Verdict: "pass", TaskID: task.ID, ReviewRevision: review.ReviewRevision, ReviewStatus: review.Status,
	}
	nodeSeen := map[string]int{}
	edgeSeen := map[string]bool{}
	addNode := func(node ProvenanceNode) {
		if _, exists := nodeSeen[node.ID]; exists {
			return
		}
		nodeSeen[node.ID] = len(trace.Nodes)
		trace.Nodes = append(trace.Nodes, node)
	}
	addEdge := func(from, to, relation string) {
		key := from + "\x00" + relation + "\x00" + to
		if edgeSeen[key] {
			return
		}
		edgeSeen[key] = true
		trace.Edges = append(trace.Edges, ProvenanceEdge{From: from, To: to, Relation: relation})
	}

	taskNode := provenanceNodeID("task", task.ID)
	reviewNode := provenanceNodeID("final_review", task.ID+"|"+review.ReviewRevision)
	addNode(ProvenanceNode{ID: taskNode, Kind: "task", Ref: task.ID, Label: task.Title, Status: string(task.Status)})
	addNode(ProvenanceNode{ID: reviewNode, Kind: "final_review", Ref: review.ReviewRevision, Label: review.Summary, Status: review.Status, ReviewRevision: review.ReviewRevision})
	addEdge(taskNode, reviewNode, "has_review")

	for _, binding := range bindings {
		claim, err := canonicalReviewClaim(task, binding.Scope, binding.Claim)
		if err != nil {
			return ProvenanceBacktrace{}, err
		}
		claimNode := provenanceNodeID("final_review_claim", claim.Ref)
		addNode(ProvenanceNode{ID: claimNode, Kind: "final_review_claim", Ref: claim.Ref, Label: claim.Claim, Status: "accepted", Scope: claim.Scope, ReviewRevision: review.ReviewRevision})
		addEdge(reviewNode, claimNode, "accepts")

		step, ok := taskStepByID(task, binding.StepID)
		if !ok || step.Status != taskstate.StepCompleted {
			return ProvenanceBacktrace{}, fmt.Errorf("provenance binding points to dangling or non-completed step %q", binding.StepID)
		}
		stepNode := provenanceNodeID("step", task.ID+"|"+step.ID)
		addNode(ProvenanceNode{ID: stepNode, Kind: "step", Ref: step.ID, Label: step.Title, Status: step.Status, StepID: step.ID})
		addEdge(claimNode, stepNode, "bound_to")

		basePath := []string{taskNode, reviewNode, claimNode, stepNode}
		pathAdded := false
		for _, ref := range binding.RequirementRefs {
			if !stringIn(ref, task.RequirementRefs) || !stringIn(ref, step.RequirementRefs) {
				return ProvenanceBacktrace{}, fmt.Errorf("provenance requirement %q is no longer owned by step %q", ref, step.ID)
			}
			reqNode := provenanceNodeID("requirement", task.ID+"|"+ref)
			addNode(ProvenanceNode{ID: reqNode, Kind: "requirement", Ref: ref, Label: ref, Status: "covered"})
			addEdge(claimNode, reqNode, "covers")
			addEdge(stepNode, reqNode, "owns_requirement")
			trace.Paths = append(trace.Paths, ProvenancePath{NodeIDs: append(append([]string(nil), basePath...), reqNode)})
			pathAdded = true
		}

		ownedEvidence := taskstate.OutcomeEvidenceRefs(step)
		for _, ref := range binding.EvidenceRefs {
			if !stringIn(ref, ownedEvidence) {
				return ProvenanceBacktrace{}, fmt.Errorf("provenance evidence %q is no longer owned by step %q", ref, step.ID)
			}
			evidenceNode, err := s.provenanceEvidenceNode(task.ID, step, ref)
			if err != nil {
				return ProvenanceBacktrace{}, err
			}
			addNode(evidenceNode)
			addEdge(claimNode, evidenceNode.ID, "cites")
			addEdge(stepNode, evidenceNode.ID, "owns_evidence")
			trace.Paths = append(trace.Paths, ProvenancePath{NodeIDs: append(append([]string(nil), basePath...), evidenceNode.ID)})
			pathAdded = true
		}
		if !pathAdded {
			trace.Paths = append(trace.Paths, ProvenancePath{NodeIDs: append([]string(nil), basePath...)})
		}
	}
	return trace, nil
}

func (s *Service) provenanceEvidenceNode(taskID string, step taskstate.TaskStep, ref string) (ProvenanceNode, error) {
	kind := provenanceEvidenceKind(ref)
	id := provenanceNodeID(kind, ref)
	node := ProvenanceNode{ID: id, Kind: kind, Ref: ref, Label: ref, Status: "bound", Scope: provenanceEvidenceRole(step, ref)}
	if kind != "execution_receipt" {
		return node, nil
	}
	if s.receipts == nil {
		return ProvenanceNode{}, errors.New("execution evidence store is not configured")
	}
	receipt, err := s.receipts.Resolve(ref)
	if err != nil {
		return ProvenanceNode{}, fmt.Errorf("resolve execution receipt %s: %w", ref, err)
	}
	claim, err := s.receipts.ResolveClaim(ref)
	if err != nil {
		return ProvenanceNode{}, fmt.Errorf("resolve execution receipt claim %s: %w", ref, err)
	}
	ownerTask, ownerStep, err := parseTaskStepClaimKey(claim.ClaimKey)
	if err != nil {
		return ProvenanceNode{}, err
	}
	if ownerTask != taskID || ownerStep != step.ID {
		return ProvenanceNode{}, fmt.Errorf("cross-task or cross-step execution receipt claim for %s", ref)
	}
	digest, ok := persistedReceiptDigest(step, ref)
	if !ok {
		return ProvenanceNode{}, fmt.Errorf("execution receipt %s has no persisted step attestation", ref)
	}
	if digest != receipt.ReceiptSHA256 {
		return ProvenanceNode{}, fmt.Errorf("execution receipt digest no longer matches persisted step attestation for %s", ref)
	}
	exitCode := receipt.ExitCode
	timedOut := receipt.TimedOut
	node.Status = receipt.Status
	node.StepID = step.ID
	node.Issuer = receipt.Issuer
	node.CommandSHA256 = receipt.CommandSHA256
	node.ReceiptSHA256 = receipt.ReceiptSHA256
	node.ExitCode = &exitCode
	node.TimedOut = &timedOut
	node.StartedAt = receipt.StartedAt.UTC().Format(time.RFC3339Nano)
	node.FinishedAt = receipt.FinishedAt.UTC().Format(time.RFC3339Nano)
	node.ClaimedAt = claim.ClaimedAt.UTC().Format(time.RFC3339Nano)
	return node, nil
}

func persistedReceiptDigest(step taskstate.TaskStep, ref string) (string, bool) {
	if step.ExecutionEvidence != nil {
		for _, attestation := range step.ExecutionEvidence.Attestations {
			if attestation != nil && attestation.EvidenceRef == ref {
				return attestation.ReceiptSHA256, true
			}
		}
	}
	if step.TDD != nil {
		for _, run := range []*taskstate.TDDRun{step.TDD.Red, step.TDD.Green, step.TDD.Regression} {
			if run != nil && run.EvidenceRef == ref {
				return run.ReceiptSHA256, true
			}
		}
	}
	return "", false
}

func provenanceEvidenceRole(step taskstate.TaskStep, ref string) string {
	if step.TDD != nil {
		for _, item := range []struct {
			name string
			run  *taskstate.TDDRun
		}{
			{"tdd_red", step.TDD.Red},
			{"tdd_green", step.TDD.Green},
			{"tdd_regression", step.TDD.Regression},
		} {
			if item.run != nil && item.run.EvidenceRef == ref {
				return item.name
			}
		}
	}
	if step.Diagnostic != nil {
		if step.Diagnostic.ReproRef == ref || stringIn(ref, step.Diagnostic.EvidenceRefs) || ref == "diagnostic://"+step.ID {
			return "diagnostic"
		}
	}
	if step.ExecutionEvidence != nil {
		for _, attestation := range step.ExecutionEvidence.Attestations {
			if attestation != nil && attestation.EvidenceRef == ref {
				return "execution_contract"
			}
		}
		if stringIn(ref, step.ExecutionEvidence.EvidenceRefs) {
			return "execution_evidence"
		}
	}
	return "step_evidence"
}

func provenanceEvidenceKind(ref string) string {
	switch {
	case strings.HasPrefix(ref, "exec://"):
		return "execution_receipt"
	case strings.HasPrefix(ref, "diagnostic://"):
		return "diagnostic_evidence"
	case strings.HasPrefix(ref, "artifact://"):
		return "artifact"
	case strings.HasPrefix(ref, "commit://"):
		return "commit"
	case strings.HasPrefix(ref, "test://"):
		return "test_evidence"
	default:
		return "evidence"
	}
}

func addEvolutionMatch(trace *ProvenanceBacktrace, match evolutionEvidenceMatch, claim reviewClaim) {
	recordNode := provenanceNodeID("evolution_revision", fmt.Sprintf("%s|%d", match.Record.EvolutionID, match.Record.Revision))
	evidenceNode := provenanceEvolutionEvidenceNodeID(match)
	claimNode := provenanceNodeID("final_review_claim", claim.Ref)
	trace.Nodes = append(trace.Nodes,
		ProvenanceNode{ID: recordNode, Kind: "evolution_revision", Ref: match.Record.EvolutionID, Label: match.Record.Statement, Status: match.Record.Status, EvolutionID: match.Record.EvolutionID, Revision: match.Record.Revision},
		ProvenanceNode{ID: evidenceNode, Kind: "evolution_evidence", Ref: match.Evidence.Ref, Label: match.Evidence.Rationale, Status: "recorded", EvolutionID: match.Record.EvolutionID, ReviewRevision: match.Evidence.ReviewRevision, Relation: match.Evidence.Relation},
	)
	trace.Edges = append(trace.Edges,
		ProvenanceEdge{From: recordNode, To: evidenceNode, Relation: "records"},
		ProvenanceEdge{From: evidenceNode, To: claimNode, Relation: "uses_review_claim"},
	)
}

func provenanceEvolutionEvidenceNodeID(match evolutionEvidenceMatch) string {
	return provenanceNodeID("evolution_evidence", fmt.Sprintf("%s|%d|%d", match.Record.EvolutionID, match.Record.Revision, match.Index))
}

func taskStepByID(task taskstate.Task, stepID string) (taskstate.TaskStep, bool) {
	for _, step := range task.Steps {
		if step.ID == stepID {
			return step, true
		}
	}
	return taskstate.TaskStep{}, false
}

func parseTaskStepClaimKey(key string) (string, string, error) {
	parts := strings.Split(strings.TrimSpace(key), ":")
	if len(parts) != 4 || parts[0] != "task" || parts[1] == "" || parts[2] != "step" || parts[3] == "" {
		return "", "", errors.New("execution receipt claim key is not a task-step owner")
	}
	return parts[1], parts[3], nil
}

func provenanceNodeID(kind, ref string) string {
	return kind + ":" + ref
}

func stringIn(want string, values []string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
