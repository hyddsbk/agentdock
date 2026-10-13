package task

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/config"
	evolutionpkg "github.com/uvwt/agentdock/internal/evolution"
	"github.com/uvwt/agentdock/internal/execevidence"
	"github.com/uvwt/agentdock/internal/taskstate"
)

func TestProvenanceBacktraceFromReviewRequirementAndReceipt(t *testing.T) {
	service, _ := newTaskTestService(t)
	receipts, err := execevidence.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service.WithExecutionEvidence(receipts)

	requirement := "spec://p7#R1"
	created, err := service.manageTest(context.Background(), map[string]any{
		"action": "create", "title": "P7 fixture", "goal": "prove reverse audit",
		"completion_conditions": []string{"release evidence is traceable"},
		"requirement_refs":      []string{requirement},
		"steps": []map[string]any{{
			"id": "verify", "title": "Verify", "requirement_refs": []string{requirement},
			"execution_contract": map[string]any{"checks": []map[string]any{{"id": "test", "command": "go test ./..."}}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID := created["task_id"].(string)
	loaded, err := service.manageTest(context.Background(), map[string]any{"action": "get", "task_id": taskID})
	if err != nil {
		t.Fatal(err)
	}
	declaredAt := loaded["task"].(taskstate.Task).Steps[0].ExecutionContract.DeclaredAt
	receiptRef := issueTDDReceipt(t, receipts, "session-777777777777777777777777", "go test ./...", 0, declaredAt.Add(time.Second))
	if _, err := service.manageTest(context.Background(), map[string]any{
		"action": "checkpoint", "task_id": taskID, "step_id": "verify", "status": "completed", "summary": "verified",
		"execution_receipts": []map[string]any{{"check_id": "test", "ref": receiptRef}},
	}); err != nil {
		t.Fatal(err)
	}

	review, err := service.manageTest(context.Background(), map[string]any{
		"action": "final_review", "task_id": taskID, "status": "pass", "summary": "accepted",
		"verified":           []string{"release is proven"},
		"verified_evidence":  []map[string]any{{"claim": "release is proven", "step_id": "verify", "requirement_refs": []string{requirement}, "evidence_refs": []string{receiptRef}}},
		"intent_spec":        map[string]any{"status": "pass", "evidence": []string{"requirement satisfied"}, "evidence_bindings": []map[string]any{{"claim": "requirement satisfied", "step_id": "verify", "requirement_refs": []string{requirement}, "evidence_refs": []string{receiptRef}}}},
		"engineering_policy": map[string]any{"status": "pass", "evidence": []string{"executor receipt verified"}, "evidence_bindings": []map[string]any{{"claim": "executor receipt verified", "step_id": "verify", "evidence_refs": []string{receiptRef}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	revision := review["review_revision"].(string)
	claimRef := fmt.Sprintf("task:%s:review:%s:verified:0", taskID, revision)
	intentClaimRef := fmt.Sprintf("task:%s:review:%s:intent_spec:0", taskID, revision)

	for _, tc := range []struct {
		name  string
		query map[string]any
	}{
		{"review claim", map[string]any{"kind": "final_review_claim", "ref": claimRef}},
		{"axis claim", map[string]any{"kind": "final_review_claim", "ref": intentClaimRef}},
		{"plain claim", map[string]any{"kind": "final_review_claim", "task_id": taskID, "ref": "requirement satisfied"}},
		{"requirement", map[string]any{"kind": "requirement", "task_id": taskID, "ref": requirement}},
		{"receipt", map[string]any{"kind": "exec_receipt", "ref": receiptRef}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := service.manageTest(context.Background(), map[string]any{"action": "backtrace", "provenance_query": tc.query})
			if err != nil {
				t.Fatal(err)
			}
			trace, ok := result["backtrace"].(ProvenanceBacktrace)
			if !ok {
				t.Fatalf("backtrace type = %T", result["backtrace"])
			}
			if trace.Contract != "provenance-backtrace/v1" || !trace.Accepted || trace.TaskID != taskID || trace.ReviewRevision != revision {
				t.Fatalf("unexpected backtrace: %#v", trace)
			}
			for _, kind := range []string{"task", "final_review", "final_review_claim", "step", "requirement", "execution_receipt"} {
				if !traceHasNodeKind(trace, kind) {
					t.Fatalf("%s missing %s: %#v", tc.name, kind, trace.Nodes)
				}
			}
		})
	}

	evolutionID := "evo_7777777777777777"
	service.evolution = &provenanceEvolutionStub{records: []evolutionpkg.Record{
		{
			EvolutionID: evolutionID, Statement: "P7 evidence", Status: evolutionpkg.StatusVerified, Revision: 7,
			Evidence: []evolutionpkg.Evidence{{Ref: claimRef, Relation: evolutionpkg.RelationSupport, TaskID: taskID, ReviewRevision: revision, Rationale: "accepted final review"}},
		},
		{
			EvolutionID: "evo_8888888888888888", Statement: "also consumed", Status: evolutionpkg.StatusActive, Revision: 2,
			Evidence: []evolutionpkg.Evidence{{Ref: claimRef, Relation: evolutionpkg.RelationSupport, TaskID: taskID, ReviewRevision: revision}},
		},
	}}
	if _, err := service.manageTest(context.Background(), map[string]any{
		"action": "backtrace", "provenance_query": map[string]any{"kind": "evolution_evidence", "ref": claimRef},
	}); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous evolution evidence error = %v", err)
	}
	evolutionTraceResult, err := service.manageTest(context.Background(), map[string]any{
		"action":           "backtrace",
		"provenance_query": map[string]any{"kind": "evolution_evidence", "ref": claimRef, "evolution_id": evolutionID},
	})
	if err != nil {
		t.Fatal(err)
	}
	evolutionTrace := evolutionTraceResult["backtrace"].(ProvenanceBacktrace)
	if evolutionTrace.Root.EvolutionID != evolutionID || !traceHasNodeKind(evolutionTrace, "evolution_revision") || !traceHasNodeKind(evolutionTrace, "evolution_evidence") {
		t.Fatalf("evolution evidence graph = %#v", evolutionTrace)
	}

	before, _ := service.manageTest(context.Background(), map[string]any{"action": "get", "task_id": taskID})
	_, err = service.manageTest(context.Background(), map[string]any{"action": "backtrace", "provenance_query": map[string]any{"kind": "exec_receipt", "ref": receiptRef, "task_id": "tsk_cross_task"}})
	if err == nil || !strings.Contains(err.Error(), "cross-task") {
		t.Fatalf("cross-task error = %v", err)
	}
	if _, err := service.manageTest(context.Background(), map[string]any{
		"action":           "backtrace",
		"provenance_query": map[string]any{"kind": "requirement", "task_id": taskID, "ref": "spec://p7#missing"},
	}); err == nil {
		t.Fatal("unknown requirement backtrace unexpectedly succeeded")
	}
	staleClaim := fmt.Sprintf("task:%s:review:rev_stale:verified:0", taskID)
	if _, err := service.manageTest(context.Background(), map[string]any{
		"action": "backtrace", "provenance_query": map[string]any{"kind": "final_review_claim", "ref": staleClaim},
	}); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale review claim error = %v", err)
	}
	unclaimed := issueTDDReceipt(t, receipts, "session-888888888888888888888888", "go test ./...", 0, declaredAt.Add(2*time.Second))
	if _, err := service.manageTest(context.Background(), map[string]any{
		"action": "backtrace", "provenance_query": map[string]any{"kind": "exec_receipt", "ref": unclaimed},
	}); err == nil {
		t.Fatal("unclaimed executor receipt backtrace unexpectedly succeeded")
	}

	after, _ := service.manageTest(context.Background(), map[string]any{"action": "get", "task_id": taskID})
	if before["task"].(taskstate.Task).UpdatedAt != after["task"].(taskstate.Task).UpdatedAt {
		t.Fatal("failed backtrace mutated task")
	}
}

func traceHasNodeKind(trace ProvenanceBacktrace, kind string) bool {
	for _, node := range trace.Nodes {
		if node.Kind == kind {
			return true
		}
	}
	return false
}

type provenanceEvolutionStub struct {
	records []evolutionpkg.Record
}

func (s *provenanceEvolutionStub) Guidance(context.Context, taskstate.Task) ([]taskstate.EvolutionContextItem, error) {
	return nil, nil
}

func (s *provenanceEvolutionStub) Candidates(context.Context, taskstate.Task) ([]taskstate.EvolutionContextItem, error) {
	return nil, nil
}

func (s *provenanceEvolutionStub) ValidateBindings(_ context.Context, _ taskstate.Task, bindings []taskstate.EvolutionBinding) ([]taskstate.EvolutionBinding, error) {
	return bindings, nil
}

func (s *provenanceEvolutionStub) ResolveBindings(context.Context, taskstate.Task) error {
	return nil
}

func (s *provenanceEvolutionStub) EvidenceRecords(_ context.Context, ref string) ([]evolutionpkg.Record, error) {
	var out []evolutionpkg.Record
	for _, record := range s.records {
		for _, evidence := range record.Evidence {
			if evidence.Ref == ref {
				out = append(out, record)
				break
			}
		}
	}
	return out, nil
}

func TestTaskManageSchemaPublishesBacktraceContract(t *testing.T) {
	schema := ManageInputSchema(config.Config{})
	props := schema["properties"].(map[string]any)
	if _, ok := props["provenance_query"]; !ok {
		t.Fatal("task_manage schema missing provenance_query")
	}
	action := props["action"].(map[string]any)
	allowed := action["enum"].([]string)
	found := false
	for _, value := range allowed {
		if value == "backtrace" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("task_manage schema action enum = %#v", allowed)
	}
}
