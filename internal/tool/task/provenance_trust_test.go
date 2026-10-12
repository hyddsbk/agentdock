package task

import (
	"context"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/audittrust"
)

func TestSignedPortableAuditBundleVerifiesAcrossTrustBoundary(t *testing.T) {
	fixture := newP8Fixture(t)
	exported, err := fixture.service.manageTest(context.Background(), map[string]any{
		"action": "export_audit_bundle", "task_id": fixture.taskID, "review_revision": fixture.revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle := exported["audit_bundle"].(PortableAuditBundle)
	home := t.TempDir()
	now := time.Date(2026, 10, 12, 7, 0, 0, 0, time.UTC)
	registry, err := audittrust.Init(home, audittrust.Subject{NodeID: "node_p9", DeviceID: "device_p9"}, now)
	if err != nil {
		t.Fatal(err)
	}
	signature, err := audittrust.Sign(home, audittrust.SignInput{
		BundleContract: bundle.Contract,
		BundleSHA256:   bundle.Manifest.BundleSHA256,
		TaskID:         bundle.Manifest.TaskID,
		ReviewRevision: bundle.Manifest.ReviewRevision,
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	report := VerifySignedPortableAuditBundle(bundle, signature, registry, registry.RootKeyID, now.Add(2*time.Minute))
	if !report.Valid || report.Verdict != "pass" || !report.Integrity.Valid || !report.Trust.Valid {
		t.Fatalf("signed audit verification = %#v", report)
	}

	wrongIdentity := signature
	wrongIdentity.Statement.TaskID = "tsk_other"
	report = VerifySignedPortableAuditBundle(bundle, wrongIdentity, registry, registry.RootKeyID, now.Add(2*time.Minute))
	if report.Valid || !hasTrustFinding(report, "SIGNED_TASK_SCOPE_MISMATCH") || !hasTrustFinding(report, "SIGNATURE_INVALID") {
		t.Fatalf("tampered signed task scope was accepted: %#v", report)
	}
}

func TestSignedPortableAuditBundleRejectsRewrittenBundle(t *testing.T) {
	fixture := newP8Fixture(t)
	exported, err := fixture.service.manageTest(context.Background(), map[string]any{
		"action": "export_audit_bundle", "task_id": fixture.taskID,
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle := exported["audit_bundle"].(PortableAuditBundle)
	home := t.TempDir()
	now := time.Date(2026, 10, 12, 7, 0, 0, 0, time.UTC)
	registry, err := audittrust.Init(home, audittrust.Subject{NodeID: "node_p9", DeviceID: "device_p9"}, now)
	if err != nil {
		t.Fatal(err)
	}
	signature, err := audittrust.Sign(home, audittrust.SignInput{
		BundleContract: bundle.Contract,
		BundleSHA256:   bundle.Manifest.BundleSHA256,
		TaskID:         bundle.Manifest.TaskID,
		ReviewRevision: bundle.Manifest.ReviewRevision,
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	bundle.Task.Title += " tampered"
	report := VerifySignedPortableAuditBundle(bundle, signature, registry, registry.RootKeyID, now.Add(2*time.Minute))
	if report.Valid || report.Integrity.Valid || report.Trust.Valid {
		t.Fatalf("rewritten bundle was accepted: %#v", report)
	}
	if !hasTrustFinding(report, "SIGNED_BUNDLE_DIGEST_MISMATCH") || !hasTrustFinding(report, "BUNDLE_INTEGRITY_FAILED") {
		t.Fatalf("expected signed digest/integrity findings: %#v", report.Trust.Findings)
	}
}

func hasTrustFinding(report SignedAuditVerificationReport, code string) bool {
	for _, finding := range report.Trust.Findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}

func TestTaskManageSignedAuditExportAndOfflineVerify(t *testing.T) {
	fixture := newP8Fixture(t)
	cfg := fixture.service.config()
	registry, err := audittrust.Init(cfg.AgentDockHome, audittrust.Subject{NodeID: "node_task_action", DeviceID: "device_task_action"}, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	exported, err := fixture.service.manageTest(context.Background(), map[string]any{
		"action": "export_signed_audit_bundle", "task_id": fixture.taskID, "review_revision": fixture.revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle, ok := exported["audit_bundle"].(PortableAuditBundle)
	if !ok {
		t.Fatalf("audit bundle type = %T", exported["audit_bundle"])
	}
	signature, ok := exported["audit_signature"].(audittrust.DetachedSignature)
	if !ok {
		t.Fatalf("audit signature type = %T", exported["audit_signature"])
	}
	trustRegistry, ok := exported["audit_trust_registry"].(audittrust.Registry)
	if !ok {
		t.Fatalf("audit trust registry type = %T", exported["audit_trust_registry"])
	}
	if trustRegistry.RootKeyID != registry.RootKeyID || exported["root_key_id"] != registry.RootKeyID {
		t.Fatalf("unexpected root identity: %#v", exported)
	}

	verified, err := fixture.service.manageTest(context.Background(), map[string]any{
		"action":                  "verify_signed_audit_bundle",
		"audit_bundle":            bundle,
		"audit_signature":         signature,
		"audit_trust_registry":    trustRegistry,
		"expected_root_key_id":    registry.RootKeyID,
		"expected_trust_revision": registry.Revision,
		"expected_bundle_sha256":  bundle.Manifest.BundleSHA256,
	})
	if err != nil {
		t.Fatal(err)
	}
	report, ok := verified["signed_verification"].(SignedAuditVerificationReport)
	if !ok || !report.Valid || !report.Integrity.Valid || !report.Trust.Valid {
		t.Fatalf("signed verification = %#v", verified["signed_verification"])
	}

	stale, err := fixture.service.manageTest(context.Background(), map[string]any{
		"action":                  "verify_signed_audit_bundle",
		"audit_bundle":            bundle,
		"audit_signature":         signature,
		"audit_trust_registry":    trustRegistry,
		"expected_root_key_id":    registry.RootKeyID,
		"expected_trust_revision": registry.Revision + 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	staleReport := stale["signed_verification"].(SignedAuditVerificationReport)
	if staleReport.Valid || !hasTrustFinding(staleReport, "TRUST_REGISTRY_STALE") {
		t.Fatalf("stale registry floor was accepted: %#v", staleReport)
	}

	_, err = fixture.service.manageTest(context.Background(), map[string]any{
		"action":               "verify_signed_audit_bundle",
		"audit_bundle":         bundle,
		"audit_signature":      signature,
		"audit_trust_registry": trustRegistry,
	})
	if err == nil {
		t.Fatal("signed audit verification accepted a missing external root pin")
	}
}

func TestTaskManageSignedAuditExportAndVerify(t *testing.T) {
	fixture := newP8Fixture(t)
	cfg := fixture.service.config()
	now := time.Date(2026, 10, 12, 7, 0, 0, 0, time.UTC)
	registry, err := audittrust.Init(cfg.AgentDockHome, audittrust.Subject{NodeID: "node_task_manage", DeviceID: "device_task_manage"}, now)
	if err != nil {
		t.Fatal(err)
	}

	exported, err := fixture.service.manageTest(context.Background(), map[string]any{
		"action": "export_signed_audit_bundle", "task_id": fixture.taskID, "review_revision": fixture.revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle, ok := exported["audit_bundle"].(PortableAuditBundle)
	if !ok {
		t.Fatalf("audit bundle type = %T", exported["audit_bundle"])
	}
	signature, ok := exported["audit_signature"].(audittrust.DetachedSignature)
	if !ok {
		t.Fatalf("audit signature type = %T", exported["audit_signature"])
	}
	exportedRegistry, ok := exported["audit_trust_registry"].(audittrust.Registry)
	if !ok {
		t.Fatalf("audit registry type = %T", exported["audit_trust_registry"])
	}
	if signature.Statement.RegistryRevision != registry.Revision || exportedRegistry.Revision != registry.Revision {
		t.Fatalf("signature/registry snapshot mismatch: signature=%d registry=%d", signature.Statement.RegistryRevision, exportedRegistry.Revision)
	}

	verified, err := fixture.service.manageTest(context.Background(), map[string]any{
		"action":                  "verify_signed_audit_bundle",
		"audit_bundle":            bundle,
		"audit_signature":         signature,
		"audit_trust_registry":    exportedRegistry,
		"expected_root_key_id":    registry.RootKeyID,
		"expected_trust_revision": registry.Revision,
		"expected_bundle_sha256":  bundle.Manifest.BundleSHA256,
	})
	if err != nil {
		t.Fatal(err)
	}
	report, ok := verified["signed_verification"].(SignedAuditVerificationReport)
	if !ok {
		t.Fatalf("signed verification type = %T", verified["signed_verification"])
	}
	if !report.Valid || !report.Integrity.Valid || !report.Trust.Valid {
		t.Fatalf("signed verification = %#v", report)
	}

	stale, err := normalizeTaskManageRequest(ManageRequest{
		Action:                "verify_signed_audit_bundle",
		AuditBundle:           &bundle,
		AuditSignature:        &signature,
		AuditTrustRegistry:    &exportedRegistry,
		ExpectedRootKeyID:     registry.RootKeyID,
		ExpectedTrustRevision: registry.Revision + 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	staleReport := VerifySignedPortableAuditBundleExpected(*stale.AuditBundle, *stale.AuditSignature, *stale.AuditTrustRegistry, stale.ExpectedRootKeyID, "", stale.ExpectedTrustRevision, now.Add(time.Minute))
	if staleReport.Valid || !hasTrustFinding(staleReport, "TRUST_REGISTRY_STALE") {
		t.Fatalf("stale trust registry was accepted: %#v", staleReport)
	}
}

func TestStatefulSignedAuditVerificationAdvancesOnlyAfterFullBundleBindingPasses(t *testing.T) {
	fixture := newP8Fixture(t)
	exported, err := fixture.service.manageTest(context.Background(), map[string]any{
		"action": "export_audit_bundle", "task_id": fixture.taskID, "review_revision": fixture.revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle := exported["audit_bundle"].(PortableAuditBundle)

	signerHome := t.TempDir()
	verifierHome := t.TempDir()
	base := time.Date(2026, 10, 12, 13, 0, 0, 0, time.UTC)
	registryV1, err := audittrust.Init(signerHome, audittrust.Subject{NodeID: "node_p10_stateful", DeviceID: "device_p10_stateful"}, base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := audittrust.BootstrapTrustState(verifierHome, registryV1, registryV1.RootKeyID, registryV1.Revision, base); err != nil {
		t.Fatal(err)
	}

	registryV2, err := audittrust.Rotate(signerHome, base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	wrongScope, err := audittrust.Sign(signerHome, audittrust.SignInput{
		BundleContract: bundle.Contract,
		BundleSHA256:   bundle.Manifest.BundleSHA256,
		TaskID:         "tsk_wrong_scope",
		ReviewRevision: bundle.Manifest.ReviewRevision,
	}, base.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	report, state, err := VerifySignedPortableAuditBundleWithTrustState(
		verifierHome, bundle, wrongScope, registryV2, bundle.Manifest.BundleSHA256, base.Add(3*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.Valid || !hasTrustFinding(report, "SIGNED_TASK_SCOPE_MISMATCH") {
		t.Fatalf("wrong task scope was accepted: %#v", report)
	}
	if state.HighestAcceptedRegistryRevision != registryV1.Revision {
		t.Fatalf("failed verification advanced trust state to %d", state.HighestAcceptedRegistryRevision)
	}
	persisted, err := audittrust.LoadTrustState(verifierHome)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.HighestAcceptedRegistryRevision != registryV1.Revision {
		t.Fatalf("failed verification persisted revision %d", persisted.HighestAcceptedRegistryRevision)
	}

	correct, err := audittrust.Sign(signerHome, audittrust.SignInput{
		BundleContract: bundle.Contract,
		BundleSHA256:   bundle.Manifest.BundleSHA256,
		TaskID:         bundle.Manifest.TaskID,
		ReviewRevision: bundle.Manifest.ReviewRevision,
	}, base.Add(4*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	report, state, err = VerifySignedPortableAuditBundleWithTrustState(
		verifierHome, bundle, correct, registryV2, bundle.Manifest.BundleSHA256, base.Add(5*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Valid || state.HighestAcceptedRegistryRevision != registryV2.Revision {
		t.Fatalf("valid stateful verification did not advance state: report=%#v state=%#v", report, state)
	}

	staleSignature, err := audittrust.Sign(signerHome, audittrust.SignInput{
		BundleContract: bundle.Contract,
		BundleSHA256:   bundle.Manifest.BundleSHA256,
		TaskID:         bundle.Manifest.TaskID,
		ReviewRevision: bundle.Manifest.ReviewRevision,
	}, base.Add(6*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	report, state, err = VerifySignedPortableAuditBundleWithTrustState(
		verifierHome, bundle, staleSignature, registryV1, bundle.Manifest.BundleSHA256, base.Add(7*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.Valid || !hasTrustFinding(report, "TRUST_STATE_ROLLBACK") {
		t.Fatalf("stale registry replay was accepted: %#v", report)
	}
	if state.HighestAcceptedRegistryRevision != registryV2.Revision {
		t.Fatalf("stale replay changed trust state: %#v", state)
	}
}
