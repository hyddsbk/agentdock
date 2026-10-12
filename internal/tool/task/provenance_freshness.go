package task

import (
	"time"

	"github.com/uvwt/agentdock/internal/audittrust"
)

const provenanceTrustFreshnessContract = "provenance-trust-freshness/v1"

type FreshSignedAuditVerificationReport struct {
	Contract  string                                  `json:"contract"`
	Valid     bool                                    `json:"valid"`
	Verdict   string                                  `json:"verdict"`
	Freshness audittrust.CheckpointVerificationReport `json:"freshness"`
	Signed    SignedAuditVerificationReport           `json:"signed"`
}

func VerifySignedPortableAuditBundleCheckpointExpected(bundle PortableAuditBundle, signature audittrust.DetachedSignature, registry audittrust.Registry, checkpoint audittrust.Checkpoint, expectedRootKeyID, expectedBundleSHA256 string, now time.Time) FreshSignedAuditVerificationReport {
	freshness := audittrust.VerifyCheckpoint(registry, checkpoint, expectedRootKeyID, now)
	signed := VerifySignedPortableAuditBundleExpected(bundle, signature, registry, expectedRootKeyID, expectedBundleSHA256, checkpoint.Statement.RegistryRevision, now)
	report := FreshSignedAuditVerificationReport{
		Contract:  provenanceTrustFreshnessContract,
		Freshness: freshness,
		Signed:    signed,
	}
	report.Valid = freshness.Valid && signed.Valid
	if report.Valid {
		report.Verdict = "pass"
	} else {
		report.Verdict = "fail"
	}
	return report
}

func VerifySignedPortableAuditBundleCheckpointWithTrustState(home string, bundle PortableAuditBundle, signature audittrust.DetachedSignature, registry audittrust.Registry, checkpoint audittrust.Checkpoint, expectedBundleSHA256 string, now time.Time) (FreshSignedAuditVerificationReport, audittrust.TrustState, error) {
	state, err := audittrust.LoadTrustState(home)
	if err != nil {
		return FreshSignedAuditVerificationReport{}, audittrust.TrustState{}, err
	}
	freshness := audittrust.VerifyCheckpointFromTrustState(state, registry, checkpoint, now)
	integrity := VerifyPortableAuditBundleExpected(bundle, expectedBundleSHA256)
	trust := audittrust.VerifyExpectedFromTrustState(state, registry, signature, integrity.BundleSHA256, now)
	signed := finalizeSignedAuditVerification(bundle, signature, integrity, trust)
	report := FreshSignedAuditVerificationReport{
		Contract:  provenanceTrustFreshnessContract,
		Freshness: freshness,
		Signed:    signed,
	}
	report.Valid = freshness.Valid && signed.Valid
	if report.Valid {
		report.Verdict = "pass"
	} else {
		report.Verdict = "fail"
		return report, state, nil
	}

	advanced, err := audittrust.AdvanceTrustState(home, registry, now)
	if err != nil {
		report.Signed.Trust.Findings = append(report.Signed.Trust.Findings, audittrust.Finding{Code: "TRUST_STATE_ADVANCE_FAILED", Message: err.Error()})
		report.Signed.Trust.Valid = false
		report.Signed.Trust.Verdict = "fail"
		report.Signed.Valid = false
		report.Signed.Verdict = "fail"
		report.Valid = false
		report.Verdict = "fail"
		return report, state, nil
	}
	return report, advanced, nil
}
