package task

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/audittrust"
)

type trustCheckpointProviderStub struct {
	snapshot TrustCheckpointSnapshot
	err      error
	nodeID   string
}

func (s *trustCheckpointProviderStub) FetchTrustCheckpoint(_ context.Context, nodeID string) (TrustCheckpointSnapshot, error) {
	s.nodeID = nodeID
	if s.err != nil {
		return TrustCheckpointSnapshot{}, s.err
	}
	return s.snapshot, nil
}

func TestTaskManageCheckpointAndOnlineFreshnessVerification(t *testing.T) {
	fixture := newP8Fixture(t)
	exported, err := fixture.service.manageTest(context.Background(), map[string]any{
		"action": "export_audit_bundle", "task_id": fixture.taskID, "review_revision": fixture.revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle := exported["audit_bundle"].(PortableAuditBundle)

	home := t.TempDir()
	now := time.Now().UTC()
	registryV1, err := audittrust.Init(home, audittrust.Subject{NodeID: "node_p10_action", DeviceID: "device_p10_action"}, now.Add(-3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	verifierHome := fixture.service.config().AgentDockHome
	if _, err := audittrust.BootstrapTrustState(verifierHome, registryV1, registryV1.RootKeyID, registryV1.Revision, now.Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	registry, err := audittrust.Rotate(home, now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	signature, err := audittrust.Sign(home, audittrust.SignInput{
		BundleContract: bundle.Contract,
		BundleSHA256:   bundle.Manifest.BundleSHA256,
		TaskID:         bundle.Manifest.TaskID,
		ReviewRevision: bundle.Manifest.ReviewRevision,
	}, now.Add(-30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, registry, err := audittrust.CreateCheckpoint(home, 2*time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}

	offline, err := fixture.service.manageTest(context.Background(), map[string]any{
		"action":                 "verify_signed_audit_bundle",
		"audit_bundle":           bundle,
		"audit_signature":        signature,
		"audit_trust_registry":   registry,
		"audit_trust_checkpoint": checkpoint,
		"expected_root_key_id":   registry.RootKeyID,
		"expected_bundle_sha256": bundle.Manifest.BundleSHA256,
	})
	if err != nil {
		t.Fatal(err)
	}
	offlineReport, ok := offline["freshness_verification"].(FreshSignedAuditVerificationReport)
	if !ok || !offlineReport.Valid {
		t.Fatalf("offline checkpoint verification = %#v", offline["freshness_verification"])
	}

	provider := &trustCheckpointProviderStub{snapshot: TrustCheckpointSnapshot{Checkpoint: checkpoint, Registry: registry}}
	fixture.service.WithTrustCheckpointProvider(provider)
	online, err := fixture.service.manageTest(context.Background(), map[string]any{
		"action":                 "verify_signed_audit_bundle_online",
		"audit_bundle":           bundle,
		"audit_signature":        signature,
		"expected_bundle_sha256": bundle.Manifest.BundleSHA256,
	})
	if err != nil {
		t.Fatal(err)
	}
	onlineReport, ok := online["freshness_verification"].(FreshSignedAuditVerificationReport)
	if !ok || !onlineReport.Valid || provider.nodeID != signature.Statement.NodeID {
		t.Fatalf("online checkpoint verification = %#v node=%q", online["freshness_verification"], provider.nodeID)
	}
	state, ok := online["trust_state"].(audittrust.TrustState)
	if !ok || state.HighestAcceptedRegistryRevision != registry.Revision || state.CurrentAnchor.RootKeyID != registry.RootKeyID {
		t.Fatalf("online trust state = %#v", online["trust_state"])
	}

	provider.err = errors.New("checkpoint authority unavailable")
	if _, err := fixture.service.manageTest(context.Background(), map[string]any{
		"action":          "verify_signed_audit_bundle_online",
		"audit_bundle":    bundle,
		"audit_signature": signature,
	}); err == nil {
		t.Fatal("online checkpoint provider failure did not fail closed")
	}
}

func TestTaskManageOnlineFreshnessRequiresProvider(t *testing.T) {
	fixture := newP8Fixture(t)
	_, err := fixture.service.manageTest(context.Background(), map[string]any{
		"action":          "verify_signed_audit_bundle_online",
		"audit_bundle":    PortableAuditBundle{},
		"audit_signature": audittrust.DetachedSignature{},
	})
	if err == nil {
		t.Fatal("online verification without trust checkpoint provider was accepted")
	}
}
