package audittrust

import (
	"strings"
	"testing"
	"time"
)

func TestAuditTrustInitSignVerify(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 10, 12, 7, 0, 0, 0, time.UTC)
	subject := Subject{NodeID: "node_test", DeviceID: "device_test"}

	registry, err := Init(home, subject, now)
	if err != nil {
		t.Fatal(err)
	}
	if registry.Contract != RegistryContract || registry.Revision != 1 {
		t.Fatalf("unexpected registry: %#v", registry)
	}
	if !strings.HasPrefix(registry.RootKeyID, "audit-root-sha256:") {
		t.Fatalf("root key id = %q", registry.RootKeyID)
	}
	if ActiveKeyID(registry) == "" {
		t.Fatal("missing active signing key")
	}

	hash := strings.Repeat("a", 64)
	signature, err := Sign(home, SignInput{
		BundleContract: "provenance-audit-bundle/v1",
		BundleSHA256:   hash,
		TaskID:         "tsk_test",
		ReviewRevision: "rev_test",
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	report := Verify(registry, signature, registry.RootKeyID, hash, now.Add(2*time.Minute))
	if !report.Valid || report.Verdict != "pass" {
		t.Fatalf("verification report = %#v", report)
	}

	unpinned := Verify(registry, signature, "", hash, now.Add(2*time.Minute))
	if unpinned.Valid || !hasFinding(unpinned, "TRUST_REGISTRY_INVALID") {
		t.Fatalf("unpinned registry was trusted: %#v", unpinned)
	}
	wrongRoot := Verify(registry, signature, "audit-root-sha256:"+strings.Repeat("0", 64), hash, now.Add(2*time.Minute))
	if wrongRoot.Valid || !hasFinding(wrongRoot, "TRUST_REGISTRY_INVALID") {
		t.Fatalf("wrong root pin was trusted: %#v", wrongRoot)
	}
}

func TestAuditTrustRotationPreservesHistoricalSignatures(t *testing.T) {
	home := t.TempDir()
	base := time.Date(2026, 10, 12, 7, 0, 0, 0, time.UTC)
	registry, err := Init(home, Subject{NodeID: "node_rotate", DeviceID: "device_rotate"}, base)
	if err != nil {
		t.Fatal(err)
	}
	hash := strings.Repeat("b", 64)
	oldSignature, err := Sign(home, SignInput{
		BundleContract: "provenance-audit-bundle/v1", BundleSHA256: hash,
		TaskID: "tsk_rotate", ReviewRevision: "rev_rotate",
	}, base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	oldKeyID := oldSignature.Statement.KeyID

	rotated, err := Rotate(home, base.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Revision != 2 || ActiveKeyID(rotated) == "" || ActiveKeyID(rotated) == oldKeyID {
		t.Fatalf("rotation did not create a fresh active key: %#v", rotated)
	}
	oldKey, ok := findKey(rotated.Keys, oldKeyID)
	if !ok || oldKey.Status != KeyRetired {
		t.Fatalf("old key was not retired: %#v", oldKey)
	}
	oldReport := Verify(rotated, oldSignature, registry.RootKeyID, hash, base.Add(3*time.Minute))
	if !oldReport.Valid {
		t.Fatalf("historical signature failed after rotation: %#v", oldReport)
	}

	newSignature, err := Sign(home, SignInput{
		BundleContract: "provenance-audit-bundle/v1", BundleSHA256: hash,
		TaskID: "tsk_rotate", ReviewRevision: "rev_rotate",
	}, base.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if newSignature.Statement.KeyID == oldKeyID {
		t.Fatal("rotation kept using the old signing key")
	}
	if report := Verify(rotated, newSignature, registry.RootKeyID, hash, base.Add(4*time.Minute)); !report.Valid {
		t.Fatalf("new signature did not verify: %#v", report)
	}

	tampered := oldSignature
	tampered.Statement.SignedAt = base.Add(5 * time.Minute).Format(time.RFC3339Nano)
	report := Verify(rotated, tampered, registry.RootKeyID, hash, base.Add(6*time.Minute))
	if report.Valid || !hasFinding(report, "SIGNATURE_AFTER_KEY_RETIREMENT") || !hasFinding(report, "SIGNATURE_INVALID") {
		t.Fatalf("post-retirement backdated statement was accepted: %#v", report)
	}
}

func TestAuditTrustRevocationFailsClosedAndReplacesActiveKey(t *testing.T) {
	home := t.TempDir()
	base := time.Date(2026, 10, 12, 7, 0, 0, 0, time.UTC)
	registry, err := Init(home, Subject{NodeID: "node_revoke", DeviceID: "device_revoke"}, base)
	if err != nil {
		t.Fatal(err)
	}
	hash := strings.Repeat("c", 64)
	signature, err := Sign(home, SignInput{
		BundleContract: "provenance-audit-bundle/v1", BundleSHA256: hash,
		TaskID: "tsk_revoke", ReviewRevision: "rev_revoke",
	}, base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	oldKeyID := signature.Statement.KeyID

	revoked, err := Revoke(home, oldKeyID, base.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if revoked.Revision != registry.Revision+1 {
		t.Fatalf("revision = %d", revoked.Revision)
	}
	key, ok := findKey(revoked.Keys, oldKeyID)
	if !ok || key.Status != KeyRevoked || key.RevokedAt == "" {
		t.Fatalf("key was not revoked: %#v", key)
	}
	if ActiveKeyID(revoked) == "" || ActiveKeyID(revoked) == oldKeyID {
		t.Fatal("revoking the active key did not atomically create a replacement")
	}
	report := Verify(revoked, signature, registry.RootKeyID, hash, base.Add(3*time.Minute))
	if report.Valid || !hasFinding(report, "SIGNING_KEY_REVOKED") {
		t.Fatalf("revoked signature was accepted: %#v", report)
	}

	// A root-signed registry can itself be replayed. The independently pinned
	// minimum revision closes that downgrade path after a revocation event.
	stale := VerifyExpected(registry, signature, registry.RootKeyID, hash, revoked.Revision, base.Add(3*time.Minute))
	if stale.Valid || !hasFinding(stale, "TRUST_REGISTRY_STALE") {
		t.Fatalf("pre-revocation registry replay was accepted: %#v", stale)
	}
}

func TestAuditTrustRegistryTamperIsRejected(t *testing.T) {
	home := t.TempDir()
	base := time.Date(2026, 10, 12, 7, 0, 0, 0, time.UTC)
	registry, err := Init(home, Subject{NodeID: "node_tamper", DeviceID: "device_tamper"}, base)
	if err != nil {
		t.Fatal(err)
	}
	hash := strings.Repeat("d", 64)
	signature, err := Sign(home, SignInput{
		BundleContract: "provenance-audit-bundle/v1", BundleSHA256: hash,
		TaskID: "tsk_tamper", ReviewRevision: "rev_tamper",
	}, base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	tampered := registry
	tampered.Subject.NodeID = "node_attacker"
	report := Verify(tampered, signature, registry.RootKeyID, hash, base.Add(2*time.Minute))
	if report.Valid || !hasFinding(report, "TRUST_REGISTRY_INVALID") || !hasFinding(report, "SIGNER_IDENTITY_MISMATCH") {
		t.Fatalf("tampered registry was accepted: %#v", report)
	}
}

func hasFinding(report VerificationReport, code string) bool {
	for _, finding := range report.Findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}
