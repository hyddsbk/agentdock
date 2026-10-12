package task

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	evolutionpkg "github.com/uvwt/agentdock/internal/evolution"
	"github.com/uvwt/agentdock/internal/execevidence"
	"github.com/uvwt/agentdock/internal/taskstate"
)

const (
	provenanceIntegrityContract = "provenance-integrity/v1"
	portableAuditBundleContract = "provenance-audit-bundle/v1"
)

type ProvenanceIntegrityFinding struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	NodeID  string `json:"node_id,omitempty"`
	Ref     string `json:"ref,omitempty"`
}

type ProvenanceIntegrityReport struct {
	Contract       string                       `json:"contract"`
	Valid          bool                         `json:"valid"`
	Verdict        string                       `json:"verdict"`
	TaskID         string                       `json:"task_id,omitempty"`
	ReviewRevision string                       `json:"review_revision,omitempty"`
	GraphSHA256    string                       `json:"graph_sha256,omitempty"`
	BundleSHA256   string                       `json:"bundle_sha256,omitempty"`
	NodeCount      int                          `json:"node_count,omitempty"`
	EdgeCount      int                          `json:"edge_count,omitempty"`
	PathCount      int                          `json:"path_count,omitempty"`
	Findings       []ProvenanceIntegrityFinding `json:"findings,omitempty"`
}

type PortableAuditManifest struct {
	TaskID           string `json:"task_id"`
	ReviewRevision   string `json:"review_revision"`
	GraphContract    string `json:"graph_contract"`
	EvolutionChecked bool   `json:"evolution_checked"`
	NodeCount        int    `json:"node_count"`
	EdgeCount        int    `json:"edge_count"`
	PathCount        int    `json:"path_count"`
	TaskSHA256       string `json:"task_sha256"`
	ReceiptsSHA256   string `json:"receipts_sha256"`
	ClaimsSHA256     string `json:"claims_sha256"`
	EvolutionSHA256  string `json:"evolution_sha256"`
	GraphSHA256      string `json:"graph_sha256"`
	BundleSHA256     string `json:"bundle_sha256"`
}

type PortableAuditBundle struct {
	Contract          string                 `json:"contract"`
	Manifest          PortableAuditManifest  `json:"manifest"`
	Task              taskstate.Task         `json:"task"`
	ExecutionReceipts []execevidence.Receipt `json:"execution_receipts,omitempty"`
	ExecutionClaims   []execevidence.Claim   `json:"execution_claims,omitempty"`
	EvolutionRecords  []evolutionpkg.Record  `json:"evolution_records,omitempty"`
	Graph             ProvenanceBacktrace    `json:"graph"`
}

type portableBundleDigestPayload struct {
	Contract          string                 `json:"contract"`
	Manifest          PortableAuditManifest  `json:"manifest"`
	Task              taskstate.Task         `json:"task"`
	ExecutionReceipts []execevidence.Receipt `json:"execution_receipts,omitempty"`
	ExecutionClaims   []execevidence.Claim   `json:"execution_claims,omitempty"`
	EvolutionRecords  []evolutionpkg.Record  `json:"evolution_records,omitempty"`
	Graph             ProvenanceBacktrace    `json:"graph"`
}

func (s *Service) provenanceIntegrity(ctx context.Context, taskID, expectedRevision string) (ProvenanceIntegrityReport, *PortableAuditBundle) {
	bundle, err := s.buildPortableAuditBundle(ctx, taskID, expectedRevision)
	if err != nil {
		report := failedIntegrityReport(taskID, expectedRevision, err)
		return report, nil
	}
	report := VerifyPortableAuditBundle(bundle)
	return report, &bundle
}

func (s *Service) buildPortableAuditBundle(ctx context.Context, taskID, expectedRevision string) (PortableAuditBundle, error) {
	taskID = strings.TrimSpace(taskID)
	expectedRevision = strings.TrimSpace(expectedRevision)
	if taskID == "" {
		return PortableAuditBundle{}, fmt.Errorf("task_id is required for provenance revalidation")
	}
	task, err := s.tasks.Get(taskID)
	if err != nil {
		return PortableAuditBundle{}, err
	}
	if err := validatePassingReview(task, expectedRevision); err != nil {
		return PortableAuditBundle{}, err
	}
	if len(task.FinalReview.Provenance.Bindings) == 0 {
		return PortableAuditBundle{}, fmt.Errorf("passing final_review provenance has no bindings")
	}

	query := ProvenanceQueryRequest{
		Kind:           "final_review",
		Ref:            task.FinalReview.ReviewRevision,
		TaskID:         task.ID,
		ReviewRevision: task.FinalReview.ReviewRevision,
	}
	graph, err := s.buildProvenanceGraph(task, query, task.FinalReview.Provenance.Bindings)
	if err != nil {
		return PortableAuditBundle{}, err
	}
	graph.Root.NodeID = provenanceNodeID("final_review", task.ID+"|"+task.FinalReview.ReviewRevision)

	claimRefs := make(map[string]reviewClaim)
	for _, binding := range task.FinalReview.Provenance.Bindings {
		claim, claimErr := canonicalReviewClaim(task, binding.Scope, binding.Claim)
		if claimErr != nil {
			return PortableAuditBundle{}, claimErr
		}
		claimRefs[claim.Ref] = claim
	}

	provider, evolutionChecked := s.evolution.(EvolutionEvidenceProvider)
	if s.config != nil && strings.TrimSpace(s.config().NexusEndpoint) != "" && (!evolutionChecked || provider == nil) {
		return PortableAuditBundle{}, fmt.Errorf("Evolution evidence authority is unavailable")
	}
	recordByKey := map[string]evolutionpkg.Record{}
	recordDigestByKey := map[string]string{}
	if evolutionChecked && provider != nil {
		refs := sortedMapKeys(claimRefs)
		for _, ref := range refs {
			records, queryErr := provider.EvidenceRecords(ctx, ref)
			if queryErr != nil {
				return PortableAuditBundle{}, fmt.Errorf("query Evolution evidence %s: %w", ref, queryErr)
			}
			for _, record := range records {
				if strings.TrimSpace(record.EvolutionID) == "" || record.Revision <= 0 {
					return PortableAuditBundle{}, fmt.Errorf("Evolution record has invalid identity or revision")
				}
				matched := false
				for i, evidence := range record.Evidence {
					if evidence.Ref != ref {
						continue
					}
					matched = true
					if evidence.TaskID != task.ID || evidence.ReviewRevision != task.FinalReview.ReviewRevision {
						return PortableAuditBundle{}, fmt.Errorf("Evolution evidence review_revision or task scope mismatch for %s", ref)
					}
					addEvolutionMatch(&graph, evolutionEvidenceMatch{Record: record, Evidence: evidence, Index: i}, claimRefs[ref])
				}
				if !matched {
					return PortableAuditBundle{}, fmt.Errorf("Evolution evidence query returned a record without the requested evidence_ref")
				}
				key := fmt.Sprintf("%s|%d", record.EvolutionID, record.Revision)
				digest, digestErr := digestJSON(record)
				if digestErr != nil {
					return PortableAuditBundle{}, digestErr
				}
				if prior, ok := recordDigestByKey[key]; ok && prior != digest {
					return PortableAuditBundle{}, fmt.Errorf("Evolution revision %s is inconsistent across evidence queries", key)
				}
				recordDigestByKey[key] = digest
				recordByKey[key] = record
			}
		}
	}

	receiptRefs := map[string]bool{}
	for _, binding := range task.FinalReview.Provenance.Bindings {
		for _, ref := range binding.EvidenceRefs {
			if strings.HasPrefix(ref, "exec://") {
				receiptRefs[ref] = true
			}
		}
	}
	receipts := make([]execevidence.Receipt, 0, len(receiptRefs))
	claims := make([]execevidence.Claim, 0, len(receiptRefs))
	if len(receiptRefs) > 0 && s.receipts == nil {
		return PortableAuditBundle{}, fmt.Errorf("execution evidence store is not configured")
	}
	for _, ref := range sortedBoolMapKeys(receiptRefs) {
		receipt, resolveErr := s.receipts.Resolve(ref)
		if resolveErr != nil {
			return PortableAuditBundle{}, fmt.Errorf("resolve execution receipt %s: %w", ref, resolveErr)
		}
		claim, claimErr := s.receipts.ResolveClaim(ref)
		if claimErr != nil {
			return PortableAuditBundle{}, fmt.Errorf("resolve execution receipt claim %s: %w", ref, claimErr)
		}
		receipts = append(receipts, receipt)
		claims = append(claims, claim)
	}

	evolutionRecords := make([]evolutionpkg.Record, 0, len(recordByKey))
	for _, key := range sortedMapKeys(recordByKey) {
		evolutionRecords = append(evolutionRecords, recordByKey[key])
	}
	sort.Slice(receipts, func(i, j int) bool { return receipts[i].SessionID < receipts[j].SessionID })
	sort.Slice(claims, func(i, j int) bool { return claims[i].Ref < claims[j].Ref })
	graph, err = canonicalizeProvenanceGraph(graph)
	if err != nil {
		return PortableAuditBundle{}, err
	}

	bundle := PortableAuditBundle{
		Contract:          portableAuditBundleContract,
		Task:              task,
		ExecutionReceipts: receipts,
		ExecutionClaims:   claims,
		EvolutionRecords:  evolutionRecords,
		Graph:             graph,
	}
	manifest := PortableAuditManifest{
		TaskID:           task.ID,
		ReviewRevision:   task.FinalReview.ReviewRevision,
		GraphContract:    graph.Contract,
		EvolutionChecked: evolutionChecked && provider != nil,
		NodeCount:        len(graph.Nodes),
		EdgeCount:        len(graph.Edges),
		PathCount:        len(graph.Paths),
	}
	if manifest.TaskSHA256, err = digestJSON(task); err != nil {
		return PortableAuditBundle{}, err
	}
	if manifest.ReceiptsSHA256, err = digestCanonicalSlice(receipts); err != nil {
		return PortableAuditBundle{}, err
	}
	if manifest.ClaimsSHA256, err = digestCanonicalSlice(claims); err != nil {
		return PortableAuditBundle{}, err
	}
	if manifest.EvolutionSHA256, err = digestCanonicalSlice(evolutionRecords); err != nil {
		return PortableAuditBundle{}, err
	}
	if manifest.GraphSHA256, err = digestJSON(graph); err != nil {
		return PortableAuditBundle{}, err
	}
	bundle.Manifest = manifest
	if bundle.Manifest.BundleSHA256, err = portableBundleDigest(bundle); err != nil {
		return PortableAuditBundle{}, err
	}
	return bundle, nil
}

func VerifyPortableAuditBundle(bundle PortableAuditBundle) ProvenanceIntegrityReport {
	report := ProvenanceIntegrityReport{
		Contract:       provenanceIntegrityContract,
		TaskID:         strings.TrimSpace(bundle.Manifest.TaskID),
		ReviewRevision: strings.TrimSpace(bundle.Manifest.ReviewRevision),
		NodeCount:      len(bundle.Graph.Nodes),
		EdgeCount:      len(bundle.Graph.Edges),
		PathCount:      len(bundle.Graph.Paths),
	}
	if bundle.Contract != portableAuditBundleContract {
		addIntegrityFinding(&report, "BUNDLE_CONTRACT_MISMATCH", "unsupported portable audit bundle contract", "", bundle.Contract)
	}

	graph, graphErr := canonicalizeProvenanceGraph(bundle.Graph)
	if graphErr != nil {
		addIntegrityFinding(&report, "GRAPH_STRUCTURE_INVALID", graphErr.Error(), "", "")
	} else {
		graphDigest, err := digestJSON(graph)
		if err != nil {
			addIntegrityFinding(&report, "GRAPH_DIGEST_ERROR", err.Error(), "", "")
		} else {
			report.GraphSHA256 = graphDigest
			if !digestEqual(bundle.Manifest.GraphSHA256, graphDigest) {
				addIntegrityFinding(&report, "GRAPH_DIGEST_MISMATCH", "graph digest does not match the manifest", "", "")
			}
		}
	}

	verifyComponentDigest(&report, "TASK_DIGEST_MISMATCH", bundle.Manifest.TaskSHA256, bundle.Task)
	verifyCanonicalSliceDigest(&report, "RECEIPT_SET_DIGEST_MISMATCH", bundle.Manifest.ReceiptsSHA256, bundle.ExecutionReceipts)
	verifyCanonicalSliceDigest(&report, "CLAIM_SET_DIGEST_MISMATCH", bundle.Manifest.ClaimsSHA256, bundle.ExecutionClaims)
	verifyCanonicalSliceDigest(&report, "EVOLUTION_SET_DIGEST_MISMATCH", bundle.Manifest.EvolutionSHA256, bundle.EvolutionRecords)

	bundleDigest, err := portableBundleDigest(bundle)
	if err != nil {
		addIntegrityFinding(&report, "BUNDLE_DIGEST_ERROR", err.Error(), "", "")
	} else {
		report.BundleSHA256 = bundleDigest
		if !digestEqual(bundle.Manifest.BundleSHA256, bundleDigest) {
			addIntegrityFinding(&report, "BUNDLE_DIGEST_MISMATCH", "bundle digest does not match the manifest", "", "")
		}
	}

	if bundle.Manifest.NodeCount != len(bundle.Graph.Nodes) || bundle.Manifest.EdgeCount != len(bundle.Graph.Edges) || bundle.Manifest.PathCount != len(bundle.Graph.Paths) {
		addIntegrityFinding(&report, "GRAPH_COUNT_MISMATCH", "graph counts do not match the manifest", "", "")
	}
	verifyPortableSemantics(bundle, &report)

	report.Valid = len(report.Findings) == 0
	if report.Valid {
		report.Verdict = "pass"
	} else {
		report.Verdict = "fail"
	}
	return report
}

func VerifyPortableAuditBundleExpected(bundle PortableAuditBundle, expectedSHA256 string) ProvenanceIntegrityReport {
	report := VerifyPortableAuditBundle(bundle)
	expectedSHA256 = strings.ToLower(strings.TrimSpace(expectedSHA256))
	if expectedSHA256 != "" {
		if len(expectedSHA256) != 64 {
			addIntegrityFinding(&report, "EXPECTED_DIGEST_INVALID", "expected bundle SHA-256 must be 64 hex characters", "", "")
		} else if _, err := hex.DecodeString(expectedSHA256); err != nil {
			addIntegrityFinding(&report, "EXPECTED_DIGEST_INVALID", "expected bundle SHA-256 is not hexadecimal", "", "")
		} else if !digestEqual(expectedSHA256, report.BundleSHA256) {
			addIntegrityFinding(&report, "EXPECTED_DIGEST_MISMATCH", "bundle does not match the externally pinned SHA-256", "", "")
		}
	}
	report.Valid = len(report.Findings) == 0
	if report.Valid {
		report.Verdict = "pass"
	} else {
		report.Verdict = "fail"
	}
	return report
}

func verifyPortableSemantics(bundle PortableAuditBundle, report *ProvenanceIntegrityReport) {
	task := bundle.Task
	if strings.TrimSpace(task.ID) == "" {
		addIntegrityFinding(report, "TASK_ID_MISSING", "task snapshot has no id", "", "")
		return
	}
	if task.FinalReview == nil {
		addIntegrityFinding(report, "FINAL_REVIEW_MISSING", "task snapshot has no final_review", "", "")
		return
	}
	review := task.FinalReview
	if review.Status != taskstate.FinalReviewPass {
		addIntegrityFinding(report, "FINAL_REVIEW_NOT_PASSING", "portable audit bundle must be rooted in a passing final_review", "", "")
	}
	if review.Provenance == nil || len(review.Provenance.Bindings) == 0 {
		addIntegrityFinding(report, "PROVENANCE_BINDINGS_MISSING", "passing final_review has no provenance bindings", "", "")
		return
	}
	if bundle.Manifest.TaskID != task.ID || bundle.Graph.TaskID != task.ID || bundle.Graph.Root.TaskID != task.ID {
		addIntegrityFinding(report, "TASK_SCOPE_MISMATCH", "task ids disagree across bundle manifest and graph", "", task.ID)
	}
	if bundle.Manifest.ReviewRevision != review.ReviewRevision || bundle.Graph.ReviewRevision != review.ReviewRevision || bundle.Graph.Root.ReviewRevision != review.ReviewRevision {
		addIntegrityFinding(report, "REVIEW_REVISION_MISMATCH", "review_revision disagrees across bundle snapshot and graph", "", review.ReviewRevision)
	}
	if bundle.Manifest.GraphContract != bundle.Graph.Contract || bundle.Graph.Contract != provenanceBacktraceContract {
		addIntegrityFinding(report, "GRAPH_CONTRACT_MISMATCH", "graph contract does not match the expected P7 provenance contract", "", bundle.Graph.Contract)
	}

	nodes := map[string]ProvenanceNode{}
	for _, node := range bundle.Graph.Nodes {
		if prior, ok := nodes[node.ID]; ok {
			if !jsonEqual(prior, node) {
				addIntegrityFinding(report, "DUPLICATE_NODE_CONFLICT", "duplicate graph node id has conflicting content", node.ID, node.Ref)
			}
			continue
		}
		nodes[node.ID] = node
	}
	edgeSet := map[string]bool{}
	pairSet := map[string]bool{}
	for _, edge := range bundle.Graph.Edges {
		if _, ok := nodes[edge.From]; !ok {
			addIntegrityFinding(report, "DANGLING_EDGE", "graph edge source node is missing", edge.From, "")
		}
		if _, ok := nodes[edge.To]; !ok {
			addIntegrityFinding(report, "DANGLING_EDGE", "graph edge target node is missing", edge.To, "")
		}
		edgeSet[edgeKey(edge.From, edge.Relation, edge.To)] = true
		pairSet[edge.From+"\x00"+edge.To] = true
	}
	if _, ok := nodes[bundle.Graph.Root.NodeID]; !ok {
		addIntegrityFinding(report, "ROOT_NODE_MISSING", "graph root node is missing", bundle.Graph.Root.NodeID, bundle.Graph.Root.Ref)
	}
	for _, path := range bundle.Graph.Paths {
		for i, nodeID := range path.NodeIDs {
			if _, ok := nodes[nodeID]; !ok {
				addIntegrityFinding(report, "DANGLING_PATH", "graph path references a missing node", nodeID, "")
			}
			if i > 0 && !pairSet[path.NodeIDs[i-1]+"\x00"+nodeID] {
				addIntegrityFinding(report, "BROKEN_PATH", "graph path contains nodes without a connecting edge", nodeID, "")
			}
		}
	}

	taskNode := provenanceNodeID("task", task.ID)
	reviewNode := provenanceNodeID("final_review", task.ID+"|"+review.ReviewRevision)
	requireNode(report, nodes, taskNode, "task", task.ID)
	requireNode(report, nodes, reviewNode, "final_review", review.ReviewRevision)
	requireEdge(report, edgeSet, taskNode, "has_review", reviewNode)

	steps := map[string]taskstate.TaskStep{}
	for _, step := range task.Steps {
		steps[step.ID] = step
	}
	claimRefs := map[string]reviewClaim{}
	expectedExec := map[string]string{}
	for _, binding := range review.Provenance.Bindings {
		claim, err := canonicalReviewClaim(task, binding.Scope, binding.Claim)
		if err != nil {
			addIntegrityFinding(report, "CLAIM_REVISION_MISMATCH", err.Error(), "", binding.Claim)
			continue
		}
		claimRefs[claim.Ref] = claim
		claimNode := provenanceNodeID("final_review_claim", claim.Ref)
		requireNode(report, nodes, claimNode, "final_review_claim", claim.Ref)
		requireEdge(report, edgeSet, reviewNode, "accepts", claimNode)

		step, ok := steps[binding.StepID]
		if !ok || step.Status != taskstate.StepCompleted {
			addIntegrityFinding(report, "BROKEN_STEP_LINK", "provenance binding points to a missing or non-completed step", "", binding.StepID)
			continue
		}
		stepNode := provenanceNodeID("step", task.ID+"|"+step.ID)
		requireNode(report, nodes, stepNode, "step", step.ID)
		requireEdge(report, edgeSet, claimNode, "bound_to", stepNode)

		for _, ref := range binding.RequirementRefs {
			if !stringIn(ref, task.RequirementRefs) || !stringIn(ref, step.RequirementRefs) {
				addIntegrityFinding(report, "BROKEN_REQUIREMENT_LINK", "requirement no longer belongs to both task and bound step", "", ref)
				continue
			}
			reqNode := provenanceNodeID("requirement", task.ID+"|"+ref)
			requireNode(report, nodes, reqNode, "requirement", ref)
			requireEdge(report, edgeSet, claimNode, "covers", reqNode)
			requireEdge(report, edgeSet, stepNode, "owns_requirement", reqNode)
		}
		ownedEvidence := taskstate.OutcomeEvidenceRefs(step)
		for _, ref := range binding.EvidenceRefs {
			if !stringIn(ref, ownedEvidence) {
				addIntegrityFinding(report, "BROKEN_EVIDENCE_LINK", "evidence no longer belongs to the bound step", "", ref)
				continue
			}
			evidenceNode := provenanceNodeID(provenanceEvidenceKind(ref), ref)
			requireNode(report, nodes, evidenceNode, provenanceEvidenceKind(ref), ref)
			requireEdge(report, edgeSet, claimNode, "cites", evidenceNode)
			requireEdge(report, edgeSet, stepNode, "owns_evidence", evidenceNode)
			if strings.HasPrefix(ref, "exec://") {
				expectedExec[ref] = step.ID
			}
		}
	}

	receipts := map[string]execevidence.Receipt{}
	for _, receipt := range bundle.ExecutionReceipts {
		normalized, err := execevidence.VerifyReceipt(receipt)
		ref := "exec://" + strings.TrimSpace(receipt.SessionID)
		if err != nil {
			addIntegrityFinding(report, "RECEIPT_DIGEST_MISMATCH", err.Error(), provenanceNodeID("execution_receipt", ref), ref)
			continue
		}
		if _, exists := receipts[ref]; exists {
			addIntegrityFinding(report, "DUPLICATE_RECEIPT", "portable bundle contains the same execution receipt more than once", "", ref)
		}
		receipts[ref] = normalized
		if _, expected := expectedExec[ref]; !expected {
			addIntegrityFinding(report, "BUNDLE_SCOPE_VIOLATION", "bundle contains an execution receipt not used by final_review provenance", "", ref)
		}
	}
	claims := map[string]execevidence.Claim{}
	for _, claim := range bundle.ExecutionClaims {
		ref := strings.TrimSpace(claim.Ref)
		if _, exists := claims[ref]; exists {
			addIntegrityFinding(report, "DUPLICATE_RECEIPT_CLAIM", "portable bundle contains the same receipt claim more than once", "", ref)
		}
		claims[ref] = claim
		if _, expected := expectedExec[ref]; !expected {
			addIntegrityFinding(report, "BUNDLE_SCOPE_VIOLATION", "bundle contains a receipt claim not used by final_review provenance", "", ref)
		}
	}
	for ref, stepID := range expectedExec {
		receipt, ok := receipts[ref]
		if !ok {
			addIntegrityFinding(report, "RECEIPT_MISSING", "final_review provenance execution receipt is absent from bundle", "", ref)
			continue
		}
		step := steps[stepID]
		persisted, ok := persistedReceiptDigest(step, ref)
		if !ok || persisted != receipt.ReceiptSHA256 {
			addIntegrityFinding(report, "RECEIPT_ATTESTATION_MISMATCH", "receipt digest does not match the persisted step attestation", "", ref)
		}
		claim, ok := claims[ref]
		if !ok {
			addIntegrityFinding(report, "RECEIPT_CLAIM_MISSING", "execution receipt owner claim is absent from bundle", "", ref)
			continue
		}
		ownerTask, ownerStep, err := parseTaskStepClaimKey(claim.ClaimKey)
		if err != nil || claim.Ref != ref || ownerTask != task.ID || ownerStep != stepID {
			addIntegrityFinding(report, "RECEIPT_CLAIM_MISMATCH", "execution receipt claim does not match the bound task step", "", ref)
		}
	}

	expectedEvolutionNodes := map[string]bool{}
	for _, record := range bundle.EvolutionRecords {
		if strings.TrimSpace(record.EvolutionID) == "" || record.Revision <= 0 {
			addIntegrityFinding(report, "EVOLUTION_REVISION_INVALID", "Evolution record has an invalid identity or revision", "", record.EvolutionID)
			continue
		}
		recordNode := provenanceNodeID("evolution_revision", fmt.Sprintf("%s|%d", record.EvolutionID, record.Revision))
		expectedEvolutionNodes[recordNode] = true
		requireNode(report, nodes, recordNode, "evolution_revision", record.EvolutionID)
		for i, evidence := range record.Evidence {
			if evidence.TaskID != task.ID && evidence.ReviewRevision != review.ReviewRevision {
				continue
			}
			if evidence.TaskID != task.ID || evidence.ReviewRevision != review.ReviewRevision {
				addIntegrityFinding(report, "EVOLUTION_SCOPE_MISMATCH", "Evolution evidence partially matches the task/review scope", "", evidence.Ref)
				continue
			}
			claim, ok := claimRefs[evidence.Ref]
			if !ok {
				addIntegrityFinding(report, "EVOLUTION_BROKEN_LINK", "Evolution evidence points to a claim outside this final_review provenance closure", "", evidence.Ref)
				continue
			}
			evidenceNode := provenanceNodeID("evolution_evidence", fmt.Sprintf("%s|%d|%d", record.EvolutionID, record.Revision, i))
			expectedEvolutionNodes[evidenceNode] = true
			requireNode(report, nodes, evidenceNode, "evolution_evidence", evidence.Ref)
			requireEdge(report, edgeSet, recordNode, "records", evidenceNode)
			requireEdge(report, edgeSet, evidenceNode, "uses_review_claim", provenanceNodeID("final_review_claim", claim.Ref))
		}
	}
	for _, node := range bundle.Graph.Nodes {
		if node.Kind == "evolution_revision" || node.Kind == "evolution_evidence" {
			if !expectedEvolutionNodes[node.ID] {
				addIntegrityFinding(report, "EVOLUTION_GRAPH_ORPHAN", "graph contains Evolution provenance not present in the bundled authoritative snapshot", node.ID, node.Ref)
			}
		}
	}
}

func failedIntegrityReport(taskID, revision string, err error) ProvenanceIntegrityReport {
	report := ProvenanceIntegrityReport{
		Contract:       provenanceIntegrityContract,
		Valid:          false,
		Verdict:        "fail",
		TaskID:         strings.TrimSpace(taskID),
		ReviewRevision: strings.TrimSpace(revision),
	}
	addIntegrityFinding(&report, classifyIntegrityError(err), err.Error(), "", "")
	return report
}

func classifyIntegrityError(err error) string {
	if err == nil {
		return "INTEGRITY_FAILURE"
	}
	if errors.Is(err, os.ErrNotExist) {
		return "BROKEN_LINK"
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "integrity check"), strings.Contains(message, "digest"):
		return "DIGEST_MISMATCH"
	case strings.Contains(message, "review_revision"), strings.Contains(message, "stale"), strings.Contains(message, "revision"):
		return "REVISION_MISMATCH"
	case strings.Contains(message, "cross-task"), strings.Contains(message, "cross-step"), strings.Contains(message, "scope mismatch"):
		return "SCOPE_MISMATCH"
	case strings.Contains(message, "not configured"), strings.Contains(message, "unavailable"):
		return "AUTHORITY_UNAVAILABLE"
	case strings.Contains(message, "dangling"), strings.Contains(message, "not found"), strings.Contains(message, "missing"), strings.Contains(message, "no persisted"), strings.Contains(message, "no such file"):
		return "BROKEN_LINK"
	case strings.Contains(message, "ambiguous"), strings.Contains(message, "duplicate"):
		return "AMBIGUOUS_PROVENANCE"
	default:
		return "INTEGRITY_FAILURE"
	}
}

func canonicalizeProvenanceGraph(graph ProvenanceBacktrace) (ProvenanceBacktrace, error) {
	out := graph
	nodes := map[string]ProvenanceNode{}
	for _, node := range graph.Nodes {
		if strings.TrimSpace(node.ID) == "" {
			return ProvenanceBacktrace{}, fmt.Errorf("provenance graph contains a node without id")
		}
		if prior, ok := nodes[node.ID]; ok {
			if !jsonEqual(prior, node) {
				return ProvenanceBacktrace{}, fmt.Errorf("conflicting duplicate provenance node %q", node.ID)
			}
			continue
		}
		nodes[node.ID] = node
	}
	out.Nodes = out.Nodes[:0]
	for _, node := range nodes {
		out.Nodes = append(out.Nodes, node)
	}
	sort.Slice(out.Nodes, func(i, j int) bool { return out.Nodes[i].ID < out.Nodes[j].ID })

	edges := map[string]ProvenanceEdge{}
	for _, edge := range graph.Edges {
		key := edgeKey(edge.From, edge.Relation, edge.To)
		edges[key] = edge
	}
	out.Edges = out.Edges[:0]
	for _, edge := range edges {
		out.Edges = append(out.Edges, edge)
	}
	sort.Slice(out.Edges, func(i, j int) bool {
		if out.Edges[i].From != out.Edges[j].From {
			return out.Edges[i].From < out.Edges[j].From
		}
		if out.Edges[i].Relation != out.Edges[j].Relation {
			return out.Edges[i].Relation < out.Edges[j].Relation
		}
		return out.Edges[i].To < out.Edges[j].To
	})

	paths := map[string]ProvenancePath{}
	for _, path := range graph.Paths {
		key := strings.Join(path.NodeIDs, "\x00")
		paths[key] = ProvenancePath{NodeIDs: append([]string(nil), path.NodeIDs...)}
	}
	out.Paths = out.Paths[:0]
	for _, path := range paths {
		out.Paths = append(out.Paths, path)
	}
	sort.Slice(out.Paths, func(i, j int) bool {
		return strings.Join(out.Paths[i].NodeIDs, "\x00") < strings.Join(out.Paths[j].NodeIDs, "\x00")
	})
	return out, nil
}

func portableBundleDigest(bundle PortableAuditBundle) (string, error) {
	graph, err := canonicalizeProvenanceGraph(bundle.Graph)
	if err != nil {
		return "", err
	}
	manifest := bundle.Manifest
	manifest.BundleSHA256 = ""
	payload := portableBundleDigestPayload{
		Contract: bundle.Contract, Manifest: manifest, Task: bundle.Task,
		ExecutionReceipts: bundle.ExecutionReceipts, ExecutionClaims: bundle.ExecutionClaims,
		EvolutionRecords: bundle.EvolutionRecords, Graph: graph,
	}
	return digestJSON(payload)
}

func digestJSON(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func digestCanonicalSlice[T any](values []T) (string, error) {
	if values == nil {
		values = []T{}
	}
	return digestJSON(values)
}

func verifyCanonicalSliceDigest[T any](report *ProvenanceIntegrityReport, code, expected string, values []T) {
	actual, err := digestCanonicalSlice(values)
	if err != nil {
		addIntegrityFinding(report, code, err.Error(), "", "")
		return
	}
	if !digestEqual(expected, actual) {
		addIntegrityFinding(report, code, "component digest does not match the manifest", "", "")
	}
}

func verifyComponentDigest(report *ProvenanceIntegrityReport, code, expected string, value any) {
	actual, err := digestJSON(value)
	if err != nil {
		addIntegrityFinding(report, code, err.Error(), "", "")
		return
	}
	if !digestEqual(expected, actual) {
		addIntegrityFinding(report, code, "component digest does not match the manifest", "", "")
	}
}

func requireNode(report *ProvenanceIntegrityReport, nodes map[string]ProvenanceNode, id, kind, ref string) {
	node, ok := nodes[id]
	if !ok {
		addIntegrityFinding(report, "GRAPH_NODE_MISSING", "required provenance node is missing", id, ref)
		return
	}
	if node.Kind != kind {
		addIntegrityFinding(report, "GRAPH_NODE_KIND_MISMATCH", "provenance node kind does not match its semantic role", id, ref)
	}
}

func requireEdge(report *ProvenanceIntegrityReport, edges map[string]bool, from, relation, to string) {
	if !edges[edgeKey(from, relation, to)] {
		addIntegrityFinding(report, "GRAPH_EDGE_MISSING", "required provenance edge is missing", from, relation+" -> "+to)
	}
}

func edgeKey(from, relation, to string) string {
	return from + "\x00" + relation + "\x00" + to
}

func addIntegrityFinding(report *ProvenanceIntegrityReport, code, message, nodeID, ref string) {
	report.Findings = append(report.Findings, ProvenanceIntegrityFinding{
		Code: code, Message: strings.TrimSpace(message), NodeID: strings.TrimSpace(nodeID), Ref: strings.TrimSpace(ref),
	})
}

func digestEqual(left, right string) bool {
	return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right)) && strings.TrimSpace(left) != ""
}

func jsonEqual(left, right any) bool {
	a, errA := json.Marshal(left)
	b, errB := json.Marshal(right)
	return errA == nil && errB == nil && string(a) == string(b)
}

func sortedBoolMapKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedMapKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
