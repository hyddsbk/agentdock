package audittrust

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	CheckpointContract          = "audit-trust-checkpoint/v1"
	CheckpointStatementContract = "audit-trust-checkpoint-statement/v1"
	maxCheckpointFreshness      = 24 * time.Hour
)

type CheckpointStatement struct {
	Contract         string `json:"contract"`
	NodeID           string `json:"node_id"`
	DeviceID         string `json:"device_id"`
	RootKeyID        string `json:"root_key_id"`
	RegistryRevision int    `json:"registry_revision"`
	RegistrySHA256   string `json:"registry_sha256"`
	IssuedAt         string `json:"issued_at"`
	FreshUntil       string `json:"fresh_until"`
}

type Checkpoint struct {
	Contract  string              `json:"contract"`
	Algorithm string              `json:"algorithm"`
	Statement CheckpointStatement `json:"statement"`
	Signature string              `json:"signature"`
}

type CheckpointVerificationReport struct {
	Contract         string    `json:"contract"`
	Valid            bool      `json:"valid"`
	Verdict          string    `json:"verdict"`
	RootKeyID        string    `json:"root_key_id,omitempty"`
	RegistryRevision int       `json:"registry_revision,omitempty"`
	RegistrySHA256   string    `json:"registry_sha256,omitempty"`
	NodeID           string    `json:"node_id,omitempty"`
	DeviceID         string    `json:"device_id,omitempty"`
	FreshUntil       string    `json:"fresh_until,omitempty"`
	Findings         []Finding `json:"findings,omitempty"`
}

func CreateCheckpoint(home string, freshness time.Duration, now time.Time) (Checkpoint, Registry, error) {
	if freshness <= 0 || freshness > maxCheckpointFreshness {
		return Checkpoint{}, Registry{}, fmt.Errorf("audit trust checkpoint freshness must be > 0 and <= %s", maxCheckpointFreshness)
	}
	home = strings.TrimSpace(home)
	release, err := acquireStoreLock(home)
	if err != nil {
		return Checkpoint{}, Registry{}, err
	}
	defer release()

	store, err := loadOrMigrateStoreUnlocked(home)
	if err != nil {
		return Checkpoint{}, Registry{}, err
	}
	if err := validatePrivateStore(store); err != nil {
		return Checkpoint{}, Registry{}, err
	}
	rootPrivate, err := decodePrivate(store.RootPrivateKey)
	if err != nil {
		return Checkpoint{}, Registry{}, fmt.Errorf("decode audit trust root private key: %w", err)
	}
	registrySHA, err := RegistrySHA256(store.Registry)
	if err != nil {
		return Checkpoint{}, Registry{}, err
	}
	now = normalizeTime(now)
	statement := CheckpointStatement{
		Contract:         CheckpointStatementContract,
		NodeID:           store.Registry.Subject.NodeID,
		DeviceID:         store.Registry.Subject.DeviceID,
		RootKeyID:        store.Registry.RootKeyID,
		RegistryRevision: store.Registry.Revision,
		RegistrySHA256:   registrySHA,
		IssuedAt:         formatTime(now),
		FreshUntil:       formatTime(now.Add(freshness)),
	}
	payload, err := json.Marshal(statement)
	if err != nil {
		return Checkpoint{}, Registry{}, fmt.Errorf("encode audit trust checkpoint statement: %w", err)
	}
	return Checkpoint{
		Contract: CheckpointContract, Algorithm: Algorithm, Statement: statement,
		Signature: encodeBytes(ed25519.Sign(rootPrivate, payload)),
	}, store.Registry, nil
}

func VerifyCheckpoint(registry Registry, checkpoint Checkpoint, expectedRootKeyID string, now time.Time) CheckpointVerificationReport {
	report := CheckpointVerificationReport{
		Contract:         CheckpointContract,
		RootKeyID:        strings.TrimSpace(checkpoint.Statement.RootKeyID),
		RegistryRevision: checkpoint.Statement.RegistryRevision,
		RegistrySHA256:   strings.ToLower(strings.TrimSpace(checkpoint.Statement.RegistrySHA256)),
		NodeID:           strings.TrimSpace(checkpoint.Statement.NodeID),
		DeviceID:         strings.TrimSpace(checkpoint.Statement.DeviceID),
		FreshUntil:       strings.TrimSpace(checkpoint.Statement.FreshUntil),
	}
	add := func(code, message string) {
		report.Findings = append(report.Findings, Finding{Code: code, Message: message})
	}

	if checkpoint.Contract != CheckpointContract {
		add("CHECKPOINT_CONTRACT_MISMATCH", "unsupported audit trust checkpoint contract")
	}
	if checkpoint.Algorithm != Algorithm {
		add("CHECKPOINT_ALGORITHM_MISMATCH", "unsupported audit trust checkpoint algorithm")
	}
	if checkpoint.Statement.Contract != CheckpointStatementContract {
		add("CHECKPOINT_STATEMENT_CONTRACT_MISMATCH", "unsupported audit trust checkpoint statement contract")
	}
	if err := verifyRegistry(registry, strings.TrimSpace(expectedRootKeyID)); err != nil {
		add("TRUST_REGISTRY_INVALID", err.Error())
	}
	if checkpoint.Statement.NodeID != registry.Subject.NodeID || checkpoint.Statement.DeviceID != registry.Subject.DeviceID {
		add("CHECKPOINT_IDENTITY_MISMATCH", "checkpoint subject does not match the trusted registry subject")
	}
	if checkpoint.Statement.RootKeyID != registry.RootKeyID {
		add("CHECKPOINT_ROOT_KEY_MISMATCH", "checkpoint root key id does not match the trusted registry")
	}
	if checkpoint.Statement.RegistryRevision != registry.Revision {
		add("CHECKPOINT_REGISTRY_REVISION_MISMATCH", "checkpoint registry revision does not match the supplied trusted registry")
	}
	registrySHA, err := RegistrySHA256(registry)
	if err != nil {
		add("REGISTRY_DIGEST_ERROR", err.Error())
	} else if !strings.EqualFold(registrySHA, checkpoint.Statement.RegistrySHA256) {
		add("CHECKPOINT_REGISTRY_DIGEST_MISMATCH", "checkpoint registry digest does not match the supplied trusted registry")
	}

	issuedAt, issuedErr := parseTime(checkpoint.Statement.IssuedAt)
	if issuedErr != nil {
		add("CHECKPOINT_ISSUED_AT_INVALID", "checkpoint issued_at is invalid")
	}
	freshUntil, freshErr := parseTime(checkpoint.Statement.FreshUntil)
	if freshErr != nil {
		add("CHECKPOINT_FRESH_UNTIL_INVALID", "checkpoint fresh_until is invalid")
	}
	now = normalizeTime(now)
	if issuedErr == nil && issuedAt.After(now.Add(5*time.Minute)) {
		add("CHECKPOINT_ISSUED_IN_FUTURE", "checkpoint issued_at is unreasonably in the future")
	}
	if issuedErr == nil && freshErr == nil {
		if !freshUntil.After(issuedAt) {
			add("CHECKPOINT_FRESHNESS_INVALID", "checkpoint fresh_until must be after issued_at")
		} else if freshUntil.Sub(issuedAt) > maxCheckpointFreshness {
			add("CHECKPOINT_FRESHNESS_TOO_LONG", "checkpoint freshness window exceeds the maximum")
		}
		if now.After(freshUntil) {
			add("CHECKPOINT_EXPIRED", "audit trust checkpoint freshness window has expired")
		}
	}

	rootPublic, rootErr := decodePublic(registry.RootPublicKey)
	if rootErr != nil {
		add("CHECKPOINT_ROOT_KEY_INVALID", "trusted registry root public key is invalid")
	} else {
		if keyID("audit-root-sha256", rootPublic) != checkpoint.Statement.RootKeyID {
			add("CHECKPOINT_ROOT_KEY_ID_MISMATCH", "checkpoint root key id does not match the registry root public key")
		}
		rawSignature, sigErr := decodeSignature(checkpoint.Signature)
		if sigErr != nil {
			add("CHECKPOINT_SIGNATURE_ENCODING_INVALID", "audit trust checkpoint signature is invalid")
		} else {
			payload, marshalErr := json.Marshal(checkpoint.Statement)
			if marshalErr != nil {
				add("CHECKPOINT_STATEMENT_ENCODING_ERROR", "audit trust checkpoint statement could not be encoded")
			} else if !ed25519.Verify(rootPublic, payload, rawSignature) {
				add("CHECKPOINT_SIGNATURE_INVALID", "audit trust checkpoint root signature verification failed")
			}
		}
	}

	report.Valid = len(report.Findings) == 0
	if report.Valid {
		report.Verdict = "pass"
	} else {
		report.Verdict = "fail"
	}
	return report
}

func RegistrySHA256(registry Registry) (string, error) {
	canonical := registry
	canonical.Keys = append([]TrustedKey(nil), registry.Keys...)
	sort.Slice(canonical.Keys, func(i, j int) bool { return canonical.Keys[i].KeyID < canonical.Keys[j].KeyID })
	data, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("encode canonical audit trust registry: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func LoadCheckpointFile(path string) (Checkpoint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("read audit trust checkpoint: %w", err)
	}
	var checkpoint Checkpoint
	if err := json.Unmarshal(data, &checkpoint); err != nil {
		return Checkpoint{}, fmt.Errorf("decode audit trust checkpoint: %w", err)
	}
	return checkpoint, nil
}

func MarshalCheckpoint(checkpoint Checkpoint) ([]byte, error) {
	if strings.TrimSpace(checkpoint.Contract) == "" {
		return nil, errors.New("audit trust checkpoint contract is required")
	}
	data, err := json.MarshalIndent(checkpoint, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// VerifyCheckpointFromTrustState anchors checkpoint freshness to an already
// bootstrapped P10 verifier state. A checkpoint may advance registry freshness
// under the current root, but it can never bootstrap or replace that root.
func VerifyCheckpointFromTrustState(state TrustState, registry Registry, checkpoint Checkpoint, now time.Time) CheckpointVerificationReport {
	report := CheckpointVerificationReport{
		Contract:         CheckpointContract,
		RootKeyID:        strings.TrimSpace(checkpoint.Statement.RootKeyID),
		RegistryRevision: checkpoint.Statement.RegistryRevision,
		RegistrySHA256:   strings.ToLower(strings.TrimSpace(checkpoint.Statement.RegistrySHA256)),
		NodeID:           strings.TrimSpace(checkpoint.Statement.NodeID),
		DeviceID:         strings.TrimSpace(checkpoint.Statement.DeviceID),
		FreshUntil:       strings.TrimSpace(checkpoint.Statement.FreshUntil),
	}
	add := func(code, message string) {
		report.Findings = append(report.Findings, Finding{Code: code, Message: message})
	}
	if err := validateTrustState(state); err != nil {
		add("TRUST_STATE_INVALID", err.Error())
		report.Verdict = "fail"
		return report
	}
	if registry.Subject != state.Subject {
		add("TRUST_STATE_SUBJECT_MISMATCH", "audit trust registry subject does not match verifier trust state")
	}
	if registry.RootKeyID != state.CurrentAnchor.RootKeyID || registry.RootPublicKey != state.CurrentAnchor.RootPublicKey {
		add("TRUST_STATE_ANCHOR_MISMATCH", "audit trust registry root does not match verifier trust state")
	}
	if checkpoint.Statement.NodeID != state.Subject.NodeID || checkpoint.Statement.DeviceID != state.Subject.DeviceID {
		add("CHECKPOINT_TRUST_STATE_SUBJECT_MISMATCH", "audit trust checkpoint subject does not match verifier trust state")
	}
	if checkpoint.Statement.RootKeyID != state.CurrentAnchor.RootKeyID {
		add("CHECKPOINT_TRUST_STATE_ROOT_MISMATCH", "audit trust checkpoint root does not match verifier trust state")
	}
	if registry.Revision < state.HighestAcceptedRegistryRevision || checkpoint.Statement.RegistryRevision < state.HighestAcceptedRegistryRevision {
		add("TRUST_STATE_ROLLBACK", "audit trust checkpoint registry revision is older than the verifier's highest accepted revision")
	}

	base := VerifyCheckpoint(registry, checkpoint, state.CurrentAnchor.RootKeyID, now)
	report.Findings = append(report.Findings, base.Findings...)
	report.Valid = len(report.Findings) == 0
	if report.Valid {
		report.Verdict = "pass"
	} else {
		report.Verdict = "fail"
	}
	return report
}
