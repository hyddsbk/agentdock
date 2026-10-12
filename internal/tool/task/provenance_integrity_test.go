package task

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	evolutionpkg "github.com/uvwt/agentdock/internal/evolution"
	"github.com/uvwt/agentdock/internal/execevidence"
	"github.com/uvwt/agentdock/internal/taskstate"
)

type p8Fixture struct {
	service    *Service
	receipts   *execevidence.Store
	taskID     string
	revision   string
	claimRef   string
	receiptRef string
}

func newP8Fixture(t *testing.T) p8Fixture {
	t.Helper()
	service, _ := newTaskTestService(t)
	receipts, err := execevidence.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service.WithExecutionEvidence(receipts)

	requirement := "spec://p8#R1"
	created, err := service.manageTest(context.Background(), map[string]any{
		"action": "create", "title": "P8 fixture", "goal": "revalidate the entire provenance closure",
		"completion_conditions": []string{"portable audit bundle is independently verifiable"},
		"requirement_refs":      []string{requirement},
		"steps": []map[string]any{{
			"id": "verify", "title": "Verify P8", "requirement_refs": []string{requirement},
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
	receiptRef := issueTDDReceipt(t, receipts, "session-999999999999999999999999", "go test ./...", 0, declaredAt.Add(time.Second))
	if _, err := service.manageTest(context.Background(), map[string]any{
		"action": "checkpoint", "task_id": taskID, "step_id": "verify", "status": "completed", "summary": "verified",
		"execution_receipts": []map[string]any{{"check_id": "test", "ref": receiptRef}},
	}); err != nil {
		t.Fatal(err)
	}
	reviewed, err := service.manageTest(context.Background(), map[string]any{
		"action": "final_review", "task_id": taskID, "status": "pass", "summary": "accepted",
		"verified":          []string{"portable provenance closure is proven"},
		"verified_evidence": []map[string]any{{"claim": "portable provenance closure is proven", "step_id": "verify", "requirement_refs": []string{requirement}, "evidence_refs": []string{receiptRef}}},
		"intent_spec": map[string]any{
			"status": "pass", "evidence": []string{"requirement is covered"},
			"evidence_bindings": []map[string]any{{"claim": "requirement is covered", "step_id": "verify", "requirement_refs": []string{requirement}, "evidence_refs": []string{receiptRef}}},
		},
		"engineering_policy": map[string]any{
			"status": "pass", "evidence": []string{"executor receipt is intact"},
			"evidence_bindings": []map[string]any{{"claim": "executor receipt is intact", "step_id": "verify", "evidence_refs": []string{receiptRef}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	revision := reviewed["review_revision"].(string)
	claimRef := fmt.Sprintf("task:%s:review:%s:verified:0", taskID, revision)
	service.evolution = &provenanceEvolutionStub{records: []evolutionpkg.Record{{
		EvolutionID: "evo_9999999999999999", Statement: "P8 portable audit evidence",
		Status: evolutionpkg.StatusVerified, Revision: 9,
		Evidence: []evolutionpkg.Evidence{{
			Ref: claimRef, Relation: evolutionpkg.RelationSupport,
			TaskID: taskID, ReviewRevision: revision, Rationale: "accepted P8 review",
		}},
	}}}
	return p8Fixture{service: service, receipts: receipts, taskID: taskID, revision: revision, claimRef: claimRef, receiptRef: receiptRef}
}

func TestProvenanceIntegrityRevalidateExportAndOfflineVerify(t *testing.T) {
	fixture := newP8Fixture(t)

	revalidated, err := fixture.service.manageTest(context.Background(), map[string]any{
		"action": "revalidate", "task_id": fixture.taskID, "review_revision": fixture.revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	report := revalidated["integrity"].(ProvenanceIntegrityReport)
	if !report.Valid || report.Verdict != "pass" || report.GraphSHA256 == "" || report.NodeCount == 0 {
		t.Fatalf("revalidation report = %#v", report)
	}

	exported, err := fixture.service.manageTest(context.Background(), map[string]any{
		"action": "export_audit_bundle", "task_id": fixture.taskID, "review_revision": fixture.revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle, ok := exported["audit_bundle"].(PortableAuditBundle)
	if !ok {
		t.Fatalf("audit bundle type = %T", exported["audit_bundle"])
	}
	if bundle.Contract != portableAuditBundleContract || bundle.Manifest.BundleSHA256 == "" || len(bundle.ExecutionReceipts) != 1 || len(bundle.ExecutionClaims) != 1 || len(bundle.EvolutionRecords) != 1 {
		t.Fatalf("portable audit bundle = %#v", bundle)
	}
	if bundle.Manifest.GraphSHA256 == "" || bundle.Manifest.TaskSHA256 == "" || !bundle.Manifest.EvolutionChecked {
		t.Fatalf("portable audit manifest = %#v", bundle.Manifest)
	}

	offline := VerifyPortableAuditBundleExpected(bundle, bundle.Manifest.BundleSHA256)
	if !offline.Valid || offline.BundleSHA256 != bundle.Manifest.BundleSHA256 {
		t.Fatalf("offline verification = %#v", offline)
	}

	verified, err := fixture.service.manageTest(context.Background(), map[string]any{
		"action": "verify_audit_bundle", "audit_bundle": bundle,
		"expected_bundle_sha256": bundle.Manifest.BundleSHA256,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := verified["integrity"].(ProvenanceIntegrityReport); !got.Valid {
		t.Fatalf("task_manage offline verification = %#v", got)
	}
}

func TestPortableAuditBundleFailsClosedOnTampering(t *testing.T) {
	fixture := newP8Fixture(t)
	exported, err := fixture.service.manageTest(context.Background(), map[string]any{
		"action": "export_audit_bundle", "task_id": fixture.taskID,
	})
	if err != nil {
		t.Fatal(err)
	}
	original := exported["audit_bundle"].(PortableAuditBundle)

	t.Run("graph content", func(t *testing.T) {
		bundle := clonePortableBundle(t, original)
		bundle.Graph.Nodes[0].Label += " tampered"
		report := VerifyPortableAuditBundle(bundle)
		if report.Valid || !findingCode(report, "GRAPH_DIGEST_MISMATCH") || !findingCode(report, "BUNDLE_DIGEST_MISMATCH") {
			t.Fatalf("tampered graph report = %#v", report)
		}
	})

	t.Run("receipt content", func(t *testing.T) {
		bundle := clonePortableBundle(t, original)
		bundle.ExecutionReceipts[0].ExitCode = 77
		report := VerifyPortableAuditBundle(bundle)
		if report.Valid || !findingCode(report, "RECEIPT_DIGEST_MISMATCH") {
			t.Fatalf("tampered receipt report = %#v", report)
		}
	})

	t.Run("review revision", func(t *testing.T) {
		bundle := clonePortableBundle(t, original)
		bundle.Manifest.ReviewRevision = "rev_tampered"
		report := VerifyPortableAuditBundle(bundle)
		if report.Valid || !findingCode(report, "REVIEW_REVISION_MISMATCH") {
			t.Fatalf("tampered revision report = %#v", report)
		}
	})

	t.Run("rehashed broken graph edge", func(t *testing.T) {
		bundle := clonePortableBundle(t, original)
		filtered := bundle.Graph.Edges[:0]
		removed := false
		for _, edge := range bundle.Graph.Edges {
			if !removed && edge.Relation == "has_review" {
				removed = true
				continue
			}
			filtered = append(filtered, edge)
		}
		if !removed {
			t.Fatal("fixture graph has no has_review edge")
		}
		bundle.Graph.Edges = filtered
		bundle.Manifest.EdgeCount = len(bundle.Graph.Edges)
		canonical, err := canonicalizeProvenanceGraph(bundle.Graph)
		if err != nil {
			t.Fatal(err)
		}
		bundle.Manifest.GraphSHA256, err = digestJSON(canonical)
		if err != nil {
			t.Fatal(err)
		}
		bundle.Manifest.BundleSHA256, err = portableBundleDigest(bundle)
		if err != nil {
			t.Fatal(err)
		}
		report := VerifyPortableAuditBundle(bundle)
		if report.Valid || !findingCode(report, "GRAPH_EDGE_MISSING") {
			t.Fatalf("rehashed broken edge report = %#v", report)
		}
	})

	t.Run("external digest pin", func(t *testing.T) {
		report := VerifyPortableAuditBundleExpected(original, strings.Repeat("0", 64))
		if report.Valid || !findingCode(report, "EXPECTED_DIGEST_MISMATCH") {
			t.Fatalf("external digest pin report = %#v", report)
		}
	})
}

func TestLiveProvenanceRevalidationDetectsReceiptAndLinkTampering(t *testing.T) {
	t.Run("receipt digest", func(t *testing.T) {
		fixture := newP8Fixture(t)
		path := filepath.Join(fixture.receipts.Root(), "receipts", strings.TrimPrefix(fixture.receiptRef, "exec://")+".json")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var receipt map[string]any
		if err := json.Unmarshal(data, &receipt); err != nil {
			t.Fatal(err)
		}
		receipt["exit_code"] = float64(99)
		tampered, _ := json.Marshal(receipt)
		if err := os.WriteFile(path, tampered, 0o600); err != nil {
			t.Fatal(err)
		}
		result, err := fixture.service.manageTest(context.Background(), map[string]any{
			"action": "revalidate", "task_id": fixture.taskID,
		})
		if err != nil {
			t.Fatal(err)
		}
		report := result["integrity"].(ProvenanceIntegrityReport)
		if report.Valid || !findingCode(report, "DIGEST_MISMATCH") {
			t.Fatalf("live receipt tamper report = %#v", report)
		}
		if _, present := result["audit_bundle"]; present {
			t.Fatal("failed revalidation exposed an audit bundle")
		}
	})

	t.Run("missing receipt claim", func(t *testing.T) {
		fixture := newP8Fixture(t)
		path := filepath.Join(fixture.receipts.Root(), "claims", strings.TrimPrefix(fixture.receiptRef, "exec://")+".json")
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		result, err := fixture.service.manageTest(context.Background(), map[string]any{
			"action": "export_audit_bundle", "task_id": fixture.taskID,
		})
		if err != nil {
			t.Fatal(err)
		}
		report := result["integrity"].(ProvenanceIntegrityReport)
		if report.Valid || !findingCode(report, "BROKEN_LINK") {
			t.Fatalf("missing claim report = %#v", report)
		}
		if _, present := result["audit_bundle"]; present {
			t.Fatal("failed export exposed an audit bundle")
		}
	})

	t.Run("stale Evolution review revision", func(t *testing.T) {
		fixture := newP8Fixture(t)
		stub := fixture.service.evolution.(*provenanceEvolutionStub)
		stub.records[0].Evidence[0].ReviewRevision = "rev_stale"
		result, err := fixture.service.manageTest(context.Background(), map[string]any{
			"action": "revalidate", "task_id": fixture.taskID,
		})
		if err != nil {
			t.Fatal(err)
		}
		report := result["integrity"].(ProvenanceIntegrityReport)
		if report.Valid || !findingCode(report, "REVISION_MISMATCH") {
			t.Fatalf("stale evolution revision report = %#v", report)
		}
	})
}

func TestP8AuditActionsRemainReadOnlyAndDoNotBumpTaskSchema(t *testing.T) {
	if taskstate.SchemaVersion != 10 {
		t.Fatalf("P8 query-side contract must not bump task persistence schema: %d", taskstate.SchemaVersion)
	}
	fixture := newP8Fixture(t)
	before, err := fixture.service.manageTest(context.Background(), map[string]any{"action": "get", "task_id": fixture.taskID})
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"revalidate", "export_audit_bundle"} {
		if _, err := fixture.service.manageTest(context.Background(), map[string]any{"action": action, "task_id": fixture.taskID}); err != nil {
			t.Fatal(err)
		}
	}
	after, err := fixture.service.manageTest(context.Background(), map[string]any{"action": "get", "task_id": fixture.taskID})
	if err != nil {
		t.Fatal(err)
	}
	if before["task"].(taskstate.Task).UpdatedAt != after["task"].(taskstate.Task).UpdatedAt {
		t.Fatal("P8 audit actions mutated task state")
	}
}

func clonePortableBundle(t *testing.T, bundle PortableAuditBundle) PortableAuditBundle {
	t.Helper()
	data, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	var cloned PortableAuditBundle
	if err := json.Unmarshal(data, &cloned); err != nil {
		t.Fatal(err)
	}
	return cloned
}

func findingCode(report ProvenanceIntegrityReport, code string) bool {
	for _, finding := range report.Findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}
