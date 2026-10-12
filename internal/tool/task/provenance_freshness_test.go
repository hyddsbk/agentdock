package task

import (
	"context"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/audittrust"
)

func TestSignedPortableAuditBundleCheckpointFreshness(t *testing.T) {
	fixture := newP8Fixture(t)
	exported, err := fixture.service.manageTest(context.Background(), map[string]any{
		"action": "export_audit_bundle", "task_id": fixture.taskID, "review_revision": fixture.revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle := exported["audit_bundle"].(PortableAuditBundle)

	home := t.TempDir()
	base := time.Date(2026, 10, 12, 9, 45, 0, 0, time.UTC)
	registry, err := audittrust.Init(home, audittrust.Subject{NodeID: "node_p10", DeviceID: "device_p10"}, base)
	if err != nil {
		t.Fatal(err)
	}
	signature, err := audittrust.Sign(home, audittrust.SignInput{
		BundleContract: bundle.Contract,
		BundleSHA256:   bundle.Manifest.BundleSHA256,
		TaskID:         bundle.Manifest.TaskID,
		ReviewRevision: bundle.Manifest.ReviewRevision,
	}, base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, registry, err := audittrust.CreateCheckpoint(home, 2*time.Hour, base.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	report := VerifySignedPortableAuditBundleCheckpointExpected(
		bundle, signature, registry, checkpoint, registry.RootKeyID, bundle.Manifest.BundleSHA256, base.Add(time.Hour),
	)
	if !report.Valid || report.Verdict != "pass" || !report.Freshness.Valid || !report.Signed.Valid {
		t.Fatalf("fresh signed verification = %#v", report)
	}

	expired := VerifySignedPortableAuditBundleCheckpointExpected(
		bundle, signature, registry, checkpoint, registry.RootKeyID, bundle.Manifest.BundleSHA256, base.Add(3*time.Hour),
	)
	if expired.Valid || expired.Freshness.Valid || !freshnessFinding(expired, "CHECKPOINT_EXPIRED") {
		t.Fatalf("expired checkpoint was accepted: %#v", expired)
	}
}

func freshnessFinding(report FreshSignedAuditVerificationReport, code string) bool {
	for _, finding := range report.Freshness.Findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}
