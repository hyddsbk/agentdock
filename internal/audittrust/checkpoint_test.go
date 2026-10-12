package audittrust

import (
	"strings"
	"testing"
	"time"
)

func TestTrustCheckpointCreateVerifyAndExpire(t *testing.T) {
	home := t.TempDir()
	base := time.Date(2026, 10, 12, 9, 30, 0, 0, time.UTC)
	registry, err := Init(home, Subject{NodeID: "node_checkpoint", DeviceID: "device_checkpoint"}, base)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, snapshot, err := CreateCheckpoint(home, 2*time.Hour, base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.RootKeyID != registry.RootKeyID || checkpoint.Statement.RegistryRevision != registry.Revision {
		t.Fatalf("checkpoint snapshot mismatch: %#v %#v", checkpoint, snapshot)
	}
	if report := VerifyCheckpoint(snapshot, checkpoint, registry.RootKeyID, base.Add(time.Hour)); !report.Valid {
		t.Fatalf("checkpoint did not verify: %#v", report)
	}
	expired := VerifyCheckpoint(snapshot, checkpoint, registry.RootKeyID, base.Add(3*time.Hour))
	if expired.Valid || !checkpointHasFinding(expired, "CHECKPOINT_EXPIRED") {
		t.Fatalf("expired checkpoint was accepted: %#v", expired)
	}
}

func TestTrustCheckpointRejectsTamperAndStaleRegistry(t *testing.T) {
	home := t.TempDir()
	base := time.Date(2026, 10, 12, 9, 30, 0, 0, time.UTC)
	registry, err := Init(home, Subject{NodeID: "node_checkpoint", DeviceID: "device_checkpoint"}, base)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, snapshot, err := CreateCheckpoint(home, time.Hour, base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	tampered := checkpoint
	tampered.Statement.RegistrySHA256 = strings.Repeat("0", 64)
	report := VerifyCheckpoint(snapshot, tampered, registry.RootKeyID, base.Add(2*time.Minute))
	if report.Valid || !checkpointHasFinding(report, "CHECKPOINT_REGISTRY_DIGEST_MISMATCH") || !checkpointHasFinding(report, "CHECKPOINT_SIGNATURE_INVALID") {
		t.Fatalf("tampered checkpoint was accepted: %#v", report)
	}

	rotated, err := Rotate(home, base.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	stale := VerifyCheckpoint(rotated, checkpoint, registry.RootKeyID, base.Add(4*time.Minute))
	if stale.Valid || !checkpointHasFinding(stale, "CHECKPOINT_REGISTRY_REVISION_MISMATCH") {
		t.Fatalf("stale checkpoint/registry pair was accepted: %#v", stale)
	}
}

func TestRegistrySHA256CanonicalizesKeyOrder(t *testing.T) {
	home := t.TempDir()
	base := time.Date(2026, 10, 12, 9, 30, 0, 0, time.UTC)
	registry, err := Init(home, Subject{NodeID: "node_order", DeviceID: "device_order"}, base)
	if err != nil {
		t.Fatal(err)
	}
	registry, err = Rotate(home, base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	first, err := RegistrySHA256(registry)
	if err != nil {
		t.Fatal(err)
	}
	reordered := registry
	reordered.Keys = append([]TrustedKey(nil), registry.Keys...)
	for i, j := 0, len(reordered.Keys)-1; i < j; i, j = i+1, j-1 {
		reordered.Keys[i], reordered.Keys[j] = reordered.Keys[j], reordered.Keys[i]
	}
	second, err := RegistrySHA256(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("registry digest changed after key reordering: %s != %s", first, second)
	}
}

func TestTrustCheckpointFreshnessBound(t *testing.T) {
	home := t.TempDir()
	base := time.Date(2026, 10, 12, 9, 30, 0, 0, time.UTC)
	if _, err := Init(home, Subject{NodeID: "node_bound", DeviceID: "device_bound"}, base); err != nil {
		t.Fatal(err)
	}
	if _, _, err := CreateCheckpoint(home, 0, base); err == nil {
		t.Fatal("zero checkpoint freshness was accepted")
	}
	if _, _, err := CreateCheckpoint(home, 25*time.Hour, base); err == nil {
		t.Fatal("overlong checkpoint freshness was accepted")
	}
}

func TestTrustCheckpointFromVerifierStatePreservesRootContinuity(t *testing.T) {
	signerHome := t.TempDir()
	verifierHome := t.TempDir()
	base := time.Date(2026, 10, 12, 9, 30, 0, 0, time.UTC)

	registryV1, err := Init(signerHome, Subject{NodeID: "node_state_checkpoint", DeviceID: "device_state_checkpoint"}, base)
	if err != nil {
		t.Fatal(err)
	}
	checkpointV1, registryV1, err := CreateCheckpoint(signerHome, time.Hour, base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	stateV1, err := BootstrapTrustState(verifierHome, registryV1, registryV1.RootKeyID, registryV1.Revision, base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	registryV2, err := Rotate(signerHome, base.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	checkpointV2, registryV2, err := CreateCheckpoint(signerHome, time.Hour, base.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if report := VerifyCheckpointFromTrustState(stateV1, registryV2, checkpointV2, base.Add(4*time.Minute)); !report.Valid {
		t.Fatalf("newer checkpoint under trusted root rejected: %#v", report)
	}
	stateV2, err := AdvanceTrustState(verifierHome, registryV2, base.Add(4*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if report := VerifyCheckpointFromTrustState(stateV2, registryV1, checkpointV1, base.Add(5*time.Minute)); report.Valid || !checkpointHasFinding(report, "TRUST_STATE_ROLLBACK") {
		t.Fatalf("stale checkpoint replay accepted: %#v", report)
	}

	registryV3, continuity, err := RolloverRoot(signerHome, base.Add(6*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	checkpointV3, registryV3, err := CreateCheckpoint(signerHome, time.Hour, base.Add(7*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	beforeContinuity := VerifyCheckpointFromTrustState(stateV2, registryV3, checkpointV3, base.Add(8*time.Minute))
	if beforeContinuity.Valid || !checkpointHasFinding(beforeContinuity, "TRUST_STATE_ANCHOR_MISMATCH") {
		t.Fatalf("checkpoint changed root without continuity proof: %#v", beforeContinuity)
	}
	stateV3, err := ApplyRootContinuity(verifierHome, continuity, registryV3, base.Add(8*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if report := VerifyCheckpointFromTrustState(stateV3, registryV3, checkpointV3, base.Add(9*time.Minute)); !report.Valid {
		t.Fatalf("checkpoint after valid root continuity rejected: %#v", report)
	}
}

func checkpointHasFinding(report CheckpointVerificationReport, code string) bool {
	for _, finding := range report.Findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}
