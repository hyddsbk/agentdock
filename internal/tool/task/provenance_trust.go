package task

import (
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/audittrust"
)

const provenanceTrustContract = "provenance-trust/v1"

type SignedAuditVerificationReport struct {
	Contract  string                        `json:"contract"`
	Valid     bool                          `json:"valid"`
	Verdict   string                        `json:"verdict"`
	Integrity ProvenanceIntegrityReport     `json:"integrity"`
	Trust     audittrust.VerificationReport `json:"trust"`
}

func VerifySignedPortableAuditBundle(bundle PortableAuditBundle, signature audittrust.DetachedSignature, registry audittrust.Registry, expectedRootKeyID string, now time.Time) SignedAuditVerificationReport {
	return VerifySignedPortableAuditBundleExpected(bundle, signature, registry, expectedRootKeyID, "", 0, now)
}

func VerifySignedPortableAuditBundleExpected(bundle PortableAuditBundle, signature audittrust.DetachedSignature, registry audittrust.Registry, expectedRootKeyID, expectedBundleSHA256 string, expectedMinRegistryRevision int, now time.Time) SignedAuditVerificationReport {
	integrity := VerifyPortableAuditBundleExpected(bundle, expectedBundleSHA256)
	trust := audittrust.VerifyExpected(registry, signature, expectedRootKeyID, integrity.BundleSHA256, expectedMinRegistryRevision, now)
	return finalizeSignedAuditVerification(bundle, signature, integrity, trust)
}

func VerifySignedPortableAuditBundleWithTrustState(home string, bundle PortableAuditBundle, signature audittrust.DetachedSignature, registry audittrust.Registry, expectedBundleSHA256 string, now time.Time) (SignedAuditVerificationReport, audittrust.TrustState, error) {
	state, err := audittrust.LoadTrustState(home)
	if err != nil {
		return SignedAuditVerificationReport{}, audittrust.TrustState{}, err
	}
	integrity := VerifyPortableAuditBundleExpected(bundle, expectedBundleSHA256)
	trust := audittrust.VerifyExpectedFromTrustState(state, registry, signature, integrity.BundleSHA256, now)
	report := finalizeSignedAuditVerification(bundle, signature, integrity, trust)
	if !report.Valid {
		return report, state, nil
	}
	advanced, err := audittrust.AdvanceTrustState(home, registry, now)
	if err != nil {
		report.Trust.Findings = append(report.Trust.Findings, audittrust.Finding{Code: "TRUST_STATE_ADVANCE_FAILED", Message: err.Error()})
		report.Trust.Valid = false
		report.Trust.Verdict = "fail"
		report.Valid = false
		report.Verdict = "fail"
		return report, state, nil
	}
	return report, advanced, nil
}

func finalizeSignedAuditVerification(bundle PortableAuditBundle, signature audittrust.DetachedSignature, integrity ProvenanceIntegrityReport, trust audittrust.VerificationReport) SignedAuditVerificationReport {
	addTrustFinding := func(code, message string) {
		trust.Findings = append(trust.Findings, audittrust.Finding{Code: code, Message: message})
	}
	if strings.TrimSpace(signature.Statement.BundleContract) != strings.TrimSpace(bundle.Contract) {
		addTrustFinding("SIGNED_BUNDLE_CONTRACT_MISMATCH", "signature bundle contract does not match the portable audit bundle")
	}
	if strings.TrimSpace(signature.Statement.TaskID) != strings.TrimSpace(bundle.Manifest.TaskID) {
		addTrustFinding("SIGNED_TASK_SCOPE_MISMATCH", "signature task_id does not match the portable audit bundle")
	}
	if strings.TrimSpace(signature.Statement.ReviewRevision) != strings.TrimSpace(bundle.Manifest.ReviewRevision) {
		addTrustFinding("SIGNED_REVIEW_REVISION_MISMATCH", "signature review_revision does not match the portable audit bundle")
	}
	if !integrity.Valid {
		addTrustFinding("BUNDLE_INTEGRITY_FAILED", "portable audit bundle failed P8 integrity verification")
	}
	trust.Valid = len(trust.Findings) == 0
	if trust.Valid {
		trust.Verdict = "pass"
	} else {
		trust.Verdict = "fail"
	}

	report := SignedAuditVerificationReport{
		Contract:  provenanceTrustContract,
		Integrity: integrity,
		Trust:     trust,
	}
	report.Valid = integrity.Valid && trust.Valid
	if report.Valid {
		report.Verdict = "pass"
	} else {
		report.Verdict = "fail"
	}
	return report
}
