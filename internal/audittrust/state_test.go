package audittrust

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestTrustStateMonotonicallyAdvancesAndRejectsRegistryReplay(t *testing.T) {
	signerHome := t.TempDir()
	verifierHome := t.TempDir()
	base := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	subject := Subject{NodeID: "node_state", DeviceID: "device_state"}

	registryV1, err := Init(signerHome, subject, base)
	if err != nil {
		t.Fatal(err)
	}
	state, err := BootstrapTrustState(verifierHome, registryV1, registryV1.RootKeyID, registryV1.Revision, base)
	if err != nil {
		t.Fatal(err)
	}
	if state.Contract != TrustStateContract || state.HighestAcceptedRegistryRevision != 1 || state.CurrentAnchor.RootKeyID != registryV1.RootKeyID {
		t.Fatalf("unexpected bootstrap state: %#v", state)
	}

	hashV1 := strings.Repeat("1", 64)
	signatureV1, err := Sign(signerHome, SignInput{
		BundleContract: "provenance-audit-bundle/v1",
		BundleSHA256:   hashV1,
		TaskID:         "tsk_state_v1",
		ReviewRevision: "rev_state_v1",
	}, base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	report := VerifyExpectedFromTrustState(state, registryV1, signatureV1, hashV1, base.Add(2*time.Minute))
	if !report.Valid {
		t.Fatalf("v1 verification failed: %#v", report)
	}
	if _, err := AdvanceTrustState(verifierHome, registryV1, base.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}

	registryV2, err := Rotate(signerHome, base.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	hashV2 := strings.Repeat("2", 64)
	signatureV2, err := Sign(signerHome, SignInput{
		BundleContract: "provenance-audit-bundle/v1",
		BundleSHA256:   hashV2,
		TaskID:         "tsk_state_v2",
		ReviewRevision: "rev_state_v2",
	}, base.Add(4*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	state, err = LoadTrustState(verifierHome)
	if err != nil {
		t.Fatal(err)
	}
	report = VerifyExpectedFromTrustState(state, registryV2, signatureV2, hashV2, base.Add(5*time.Minute))
	if !report.Valid {
		t.Fatalf("v2 verification failed before advance: %#v", report)
	}
	state, err = AdvanceTrustState(verifierHome, registryV2, base.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if state.HighestAcceptedRegistryRevision != registryV2.Revision {
		t.Fatalf("state revision = %d, want %d", state.HighestAcceptedRegistryRevision, registryV2.Revision)
	}

	replayed := VerifyExpectedFromTrustState(state, registryV1, signatureV1, hashV1, base.Add(6*time.Minute))
	if replayed.Valid || !hasFinding(replayed, "TRUST_STATE_ROLLBACK") {
		t.Fatalf("stale root-signed registry replay was accepted: %#v", replayed)
	}
	if _, err := AdvanceTrustState(verifierHome, registryV1, base.Add(6*time.Minute)); err == nil || !strings.Contains(err.Error(), "rollback") {
		t.Fatalf("stale registry advanced trust state: %v", err)
	}
}

func TestRootRolloverRequiresTwoGenerationContinuity(t *testing.T) {
	signerHome := t.TempDir()
	verifierHome := t.TempDir()
	base := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	subject := Subject{NodeID: "node_rollover", DeviceID: "device_rollover"}

	oldRegistry, err := Init(signerHome, subject, base)
	if err != nil {
		t.Fatal(err)
	}
	oldHash := strings.Repeat("3", 64)
	oldSignature, err := Sign(signerHome, SignInput{
		BundleContract: "provenance-audit-bundle/v1",
		BundleSHA256:   oldHash,
		TaskID:         "tsk_before_rollover",
		ReviewRevision: "rev_before_rollover",
	}, base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BootstrapTrustState(verifierHome, oldRegistry, oldRegistry.RootKeyID, oldRegistry.Revision, base); err != nil {
		t.Fatal(err)
	}

	newRegistry, continuity, err := RolloverRoot(signerHome, base.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	recoveryData, err := os.ReadFile(RootContinuityRecoveryPath(signerHome, continuity))
	if err != nil {
		t.Fatalf("read persisted root continuity recovery proof: %v", err)
	}
	if !strings.Contains(string(recoveryData), `"contract": "audit-root-continuity/v1"`) {
		t.Fatalf("unexpected persisted root continuity proof: %s", recoveryData)
	}
	if newRegistry.RootKeyID == oldRegistry.RootKeyID || newRegistry.Revision != oldRegistry.Revision+1 {
		t.Fatalf("root rollover did not advance authority: old=%#v new=%#v", oldRegistry, newRegistry)
	}
	if continuity.Statement.PreviousAnchor.RootKeyID != oldRegistry.RootKeyID || continuity.Statement.NextAnchor.RootKeyID != newRegistry.RootKeyID {
		t.Fatalf("continuity anchors = %#v", continuity.Statement)
	}

	state, err := ApplyRootContinuity(verifierHome, continuity, newRegistry, base.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if state.CurrentAnchor.RootKeyID != newRegistry.RootKeyID || state.HighestAcceptedRegistryRevision != newRegistry.Revision {
		t.Fatalf("applied state = %#v", state)
	}

	newHash := strings.Repeat("4", 64)
	newSignature, err := Sign(signerHome, SignInput{
		BundleContract: "provenance-audit-bundle/v1",
		BundleSHA256:   newHash,
		TaskID:         "tsk_after_rollover",
		ReviewRevision: "rev_after_rollover",
	}, base.Add(4*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	report := VerifyExpectedFromTrustState(state, newRegistry, newSignature, newHash, base.Add(5*time.Minute))
	if !report.Valid {
		t.Fatalf("new root did not verify after continuity apply: %#v", report)
	}

	// Planned rollover does not rewrite history. A pre-rollover P9 signature
	// remains verifiable against the old root-signed registry snapshot.
	oldReport := VerifyExpected(oldRegistry, oldSignature, oldRegistry.RootKeyID, oldHash, oldRegistry.Revision, base.Add(5*time.Minute))
	if !oldReport.Valid {
		t.Fatalf("historical P9 signature semantics changed: %#v", oldReport)
	}
}

func TestRootRolloverRejectsNewRootSelfProofWithoutOldRootSignature(t *testing.T) {
	signerHome := t.TempDir()
	verifierHome := t.TempDir()
	base := time.Date(2026, 10, 12, 11, 0, 0, 0, time.UTC)
	subject := Subject{NodeID: "node_joint", DeviceID: "device_joint"}

	oldRegistry, err := Init(signerHome, subject, base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BootstrapTrustState(verifierHome, oldRegistry, oldRegistry.RootKeyID, oldRegistry.Revision, base); err != nil {
		t.Fatal(err)
	}
	newRegistry, continuity, err := RolloverRoot(signerHome, base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	selfProved := continuity
	selfProved.PreviousRootSignature = continuity.NextRootSignature
	if _, err := ApplyRootContinuity(verifierHome, selfProved, newRegistry, base.Add(2*time.Minute)); err == nil || !strings.Contains(err.Error(), "previous-root signature") {
		t.Fatalf("new root self-proof was accepted: %v", err)
	}

	state, err := LoadTrustState(verifierHome)
	if err != nil {
		t.Fatal(err)
	}
	if state.CurrentAnchor.RootKeyID != oldRegistry.RootKeyID || state.HighestAcceptedRegistryRevision != oldRegistry.Revision {
		t.Fatalf("failed rollover mutated verifier state: %#v", state)
	}
}

func TestRebootstrapIsExplicitCompromiseRecoveryPath(t *testing.T) {
	subject := Subject{NodeID: "node_recover", DeviceID: "device_recover"}
	base := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	oldSigner := t.TempDir()
	newSigner := t.TempDir()
	verifierHome := t.TempDir()

	oldRegistry, err := Init(oldSigner, subject, base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BootstrapTrustState(verifierHome, oldRegistry, oldRegistry.RootKeyID, oldRegistry.Revision, base); err != nil {
		t.Fatal(err)
	}
	newRegistry, err := Init(newSigner, subject, base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if newRegistry.RootKeyID == oldRegistry.RootKeyID {
		t.Fatal("test generated identical root ids")
	}

	if _, err := BootstrapTrustState(verifierHome, newRegistry, newRegistry.RootKeyID, newRegistry.Revision, base.Add(2*time.Minute)); err == nil {
		t.Fatal("ordinary bootstrap silently replaced existing trust state")
	}
	state, err := RebootstrapTrustState(verifierHome, newRegistry, newRegistry.RootKeyID, newRegistry.Revision, base.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if state.CurrentAnchor.RootKeyID != newRegistry.RootKeyID {
		t.Fatalf("explicit rebootstrap did not replace compromised authority: %#v", state)
	}
}
