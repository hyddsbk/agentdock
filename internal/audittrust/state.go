package audittrust

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/fs/atomicfile"
	"github.com/uvwt/agentdock/internal/fs/filelock"
)

const (
	TrustStateContract        = "audit-trust-state/v1"
	RootContinuityContract    = "audit-root-continuity/v1"
	RootContinuityStatementV1 = "audit-root-continuity-statement/v1"
)

type TrustAnchor struct {
	RootKeyID     string `json:"root_key_id"`
	RootPublicKey string `json:"root_public_key"`
}

type TrustState struct {
	Contract                        string      `json:"contract"`
	Subject                         Subject     `json:"subject"`
	CurrentAnchor                   TrustAnchor `json:"current_anchor"`
	HighestAcceptedRegistryRevision int         `json:"highest_accepted_registry_revision"`
	UpdatedAt                       string      `json:"updated_at"`
}

type RootContinuityStatement struct {
	Contract                 string      `json:"contract"`
	Subject                  Subject     `json:"subject"`
	PreviousAnchor           TrustAnchor `json:"previous_anchor"`
	PreviousRegistryRevision int         `json:"previous_registry_revision"`
	NextAnchor               TrustAnchor `json:"next_anchor"`
	NextRegistryRevision     int         `json:"next_registry_revision"`
	IssuedAt                 string      `json:"issued_at"`
}

type RootContinuity struct {
	Contract              string                  `json:"contract"`
	Algorithm             string                  `json:"algorithm"`
	Statement             RootContinuityStatement `json:"statement"`
	PreviousRootSignature string                  `json:"previous_root_signature"`
	NextRootSignature     string                  `json:"next_root_signature"`
}

func BootstrapTrustState(home string, registry Registry, expectedRootKeyID string, expectedMinRegistryRevision int, now time.Time) (TrustState, error) {
	return bootstrapTrustState(home, registry, expectedRootKeyID, expectedMinRegistryRevision, now, false)
}

func RebootstrapTrustState(home string, registry Registry, expectedRootKeyID string, expectedMinRegistryRevision int, now time.Time) (TrustState, error) {
	return bootstrapTrustState(home, registry, expectedRootKeyID, expectedMinRegistryRevision, now, true)
}

func bootstrapTrustState(home string, registry Registry, expectedRootKeyID string, expectedMinRegistryRevision int, now time.Time, replace bool) (TrustState, error) {
	home = strings.TrimSpace(home)
	expectedRootKeyID = strings.TrimSpace(expectedRootKeyID)
	if home == "" {
		return TrustState{}, errors.New("audit trust home is required")
	}
	if expectedRootKeyID == "" {
		return TrustState{}, errors.New("expected audit trust root key id is required")
	}
	if expectedMinRegistryRevision <= 0 {
		return TrustState{}, errors.New("expected minimum audit trust registry revision must be positive")
	}
	if err := verifyRegistry(registry, expectedRootKeyID); err != nil {
		return TrustState{}, fmt.Errorf("bootstrap verifier trust state: %w", err)
	}
	if registry.Revision < expectedMinRegistryRevision {
		return TrustState{}, errors.New("bootstrap verifier trust state rejected a registry older than the external revision floor")
	}

	release, err := acquireVerifierStateLock(home)
	if err != nil {
		return TrustState{}, err
	}
	defer release()

	path := trustStatePath(home)
	if !replace {
		if _, err := os.Lstat(path); err == nil {
			return TrustState{}, errors.New("audit verifier trust state already exists")
		} else if !errors.Is(err, os.ErrNotExist) {
			return TrustState{}, fmt.Errorf("inspect audit verifier trust state: %w", err)
		}
	}

	state := TrustState{
		Contract: TrustStateContract,
		Subject:  registry.Subject,
		CurrentAnchor: TrustAnchor{
			RootKeyID:     registry.RootKeyID,
			RootPublicKey: registry.RootPublicKey,
		},
		HighestAcceptedRegistryRevision: registry.Revision,
		UpdatedAt:                       formatTime(normalizeTime(now)),
	}
	if err := validateTrustState(state); err != nil {
		return TrustState{}, err
	}
	if err := saveTrustState(path, state); err != nil {
		return TrustState{}, err
	}
	return state, nil
}

func LoadTrustState(home string) (TrustState, error) {
	home = strings.TrimSpace(home)
	if home == "" {
		return TrustState{}, errors.New("audit trust home is required")
	}
	release, err := acquireVerifierStateLock(home)
	if err != nil {
		return TrustState{}, err
	}
	defer release()
	return loadTrustState(trustStatePath(home))
}

func AdvanceTrustState(home string, registry Registry, now time.Time) (TrustState, error) {
	home = strings.TrimSpace(home)
	if home == "" {
		return TrustState{}, errors.New("audit trust home is required")
	}
	release, err := acquireVerifierStateLock(home)
	if err != nil {
		return TrustState{}, err
	}
	defer release()

	path := trustStatePath(home)
	state, err := loadTrustState(path)
	if err != nil {
		return TrustState{}, err
	}
	if err := registryMatchesTrustState(state, registry); err != nil {
		return TrustState{}, err
	}
	if registry.Revision < state.HighestAcceptedRegistryRevision {
		return TrustState{}, fmt.Errorf("audit trust registry rollback: revision %d is older than accepted revision %d", registry.Revision, state.HighestAcceptedRegistryRevision)
	}
	if registry.Revision == state.HighestAcceptedRegistryRevision {
		return state, nil
	}
	state.HighestAcceptedRegistryRevision = registry.Revision
	state.UpdatedAt = formatTime(normalizeTime(now))
	if err := saveTrustState(path, state); err != nil {
		return TrustState{}, err
	}
	return state, nil
}

func VerifyExpectedFromTrustState(state TrustState, registry Registry, signature DetachedSignature, expectedBundleSHA256 string, now time.Time) VerificationReport {
	report := VerificationReport{
		Contract:         SignatureContract,
		BundleSHA256:     strings.ToLower(strings.TrimSpace(signature.Statement.BundleSHA256)),
		RootKeyID:        strings.TrimSpace(signature.Statement.RootKeyID),
		RegistryRevision: registry.Revision,
		KeyID:            strings.TrimSpace(signature.Statement.KeyID),
		NodeID:           strings.TrimSpace(signature.Statement.NodeID),
		DeviceID:         strings.TrimSpace(signature.Statement.DeviceID),
	}
	if err := validateTrustState(state); err != nil {
		report.Findings = append(report.Findings, Finding{Code: "TRUST_STATE_INVALID", Message: err.Error()})
		report.Verdict = "fail"
		return report
	}
	if registry.Subject != state.Subject {
		report.Findings = append(report.Findings, Finding{Code: "TRUST_STATE_SUBJECT_MISMATCH", Message: "audit trust registry subject does not match verifier trust state"})
	}
	if registry.RootKeyID != state.CurrentAnchor.RootKeyID || registry.RootPublicKey != state.CurrentAnchor.RootPublicKey {
		report.Findings = append(report.Findings, Finding{Code: "TRUST_STATE_ANCHOR_MISMATCH", Message: "audit trust registry root does not match verifier trust state"})
	}
	if registry.Revision < state.HighestAcceptedRegistryRevision {
		report.Findings = append(report.Findings, Finding{Code: "TRUST_STATE_ROLLBACK", Message: "audit trust registry revision is older than the verifier's highest accepted revision"})
	}
	base := VerifyExpected(registry, signature, state.CurrentAnchor.RootKeyID, expectedBundleSHA256, state.HighestAcceptedRegistryRevision, now)
	report.Findings = append(report.Findings, base.Findings...)
	report.BundleSHA256 = base.BundleSHA256
	report.RootKeyID = base.RootKeyID
	report.RegistryRevision = base.RegistryRevision
	report.KeyID = base.KeyID
	report.NodeID = base.NodeID
	report.DeviceID = base.DeviceID
	report.Valid = len(report.Findings) == 0
	if report.Valid {
		report.Verdict = "pass"
	} else {
		report.Verdict = "fail"
	}
	return report
}

func RolloverRoot(home string, now time.Time) (Registry, RootContinuity, error) {
	home = strings.TrimSpace(home)
	release, err := acquireStoreLock(home)
	if err != nil {
		return Registry{}, RootContinuity{}, err
	}
	defer release()

	path := storePath(home)
	store, err := loadOrMigrateStoreUnlocked(home)
	if err != nil {
		return Registry{}, RootContinuity{}, err
	}
	if err := validatePrivateStore(store); err != nil {
		return Registry{}, RootContinuity{}, err
	}
	oldPrivate, err := decodePrivate(store.RootPrivateKey)
	if err != nil {
		return Registry{}, RootContinuity{}, fmt.Errorf("decode current audit root private key: %w", err)
	}
	newPublic, newPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Registry{}, RootContinuity{}, fmt.Errorf("generate next audit root key: %w", err)
	}
	now = normalizeTime(now)
	nextRevision := store.Registry.Revision + 1
	statement := RootContinuityStatement{
		Contract: RootContinuityStatementV1,
		Subject:  store.Registry.Subject,
		PreviousAnchor: TrustAnchor{
			RootKeyID:     store.Registry.RootKeyID,
			RootPublicKey: store.Registry.RootPublicKey,
		},
		PreviousRegistryRevision: store.Registry.Revision,
		NextAnchor: TrustAnchor{
			RootKeyID:     keyID("audit-root-sha256", newPublic),
			RootPublicKey: encodeBytes(newPublic),
		},
		NextRegistryRevision: nextRevision,
		IssuedAt:             formatTime(now),
	}
	payload, err := rootContinuitySigningBytes(statement)
	if err != nil {
		return Registry{}, RootContinuity{}, err
	}
	continuity := RootContinuity{
		Contract:              RootContinuityContract,
		Algorithm:             Algorithm,
		Statement:             statement,
		PreviousRootSignature: encodeBytes(ed25519.Sign(oldPrivate, payload)),
		NextRootSignature:     encodeBytes(ed25519.Sign(newPrivate, payload)),
	}

	store.RootPrivateKey = encodeBytes(newPrivate)
	store.Registry.RootKeyID = statement.NextAnchor.RootKeyID
	store.Registry.RootPublicKey = statement.NextAnchor.RootPublicKey
	store.Registry.Revision = nextRevision
	store.Registry.UpdatedAt = formatTime(now)
	if err := signRegistry(&store.Registry, newPrivate); err != nil {
		return Registry{}, RootContinuity{}, err
	}
	if err := validateRootContinuity(continuity, store.Registry); err != nil {
		return Registry{}, RootContinuity{}, err
	}
	if err := verifyContinuitySignatures(continuity, statement.PreviousAnchor); err != nil {
		return Registry{}, RootContinuity{}, fmt.Errorf("verify generated audit root continuity: %w", err)
	}
	continuityData, err := MarshalRootContinuity(continuity)
	if err != nil {
		return Registry{}, RootContinuity{}, fmt.Errorf("encode generated audit root continuity: %w", err)
	}
	recoveryPath := RootContinuityRecoveryPath(home, continuity)
	if err := atomicfile.Write(recoveryPath, continuityData, 0o600); err != nil {
		return Registry{}, RootContinuity{}, fmt.Errorf("persist audit root continuity recovery proof: %w", err)
	}
	if err := saveStore(path, store); err != nil {
		_ = os.Remove(recoveryPath)
		return Registry{}, RootContinuity{}, err
	}
	return store.Registry, continuity, nil
}

func ApplyRootContinuity(home string, continuity RootContinuity, nextRegistry Registry, now time.Time) (TrustState, error) {
	home = strings.TrimSpace(home)
	if home == "" {
		return TrustState{}, errors.New("audit trust home is required")
	}
	release, err := acquireVerifierStateLock(home)
	if err != nil {
		return TrustState{}, err
	}
	defer release()

	path := trustStatePath(home)
	state, err := loadTrustState(path)
	if err != nil {
		return TrustState{}, err
	}
	if err := validateRootContinuity(continuity, nextRegistry); err != nil {
		return TrustState{}, err
	}
	statement := continuity.Statement
	if statement.Subject != state.Subject || nextRegistry.Subject != state.Subject {
		return TrustState{}, errors.New("audit root continuity subject does not match verifier trust state")
	}
	if statement.PreviousAnchor != state.CurrentAnchor {
		return TrustState{}, errors.New("audit root continuity previous anchor does not match verifier trust state")
	}
	if statement.PreviousRegistryRevision != state.HighestAcceptedRegistryRevision {
		return TrustState{}, fmt.Errorf("audit root continuity starts at registry revision %d, but verifier state is at revision %d", statement.PreviousRegistryRevision, state.HighestAcceptedRegistryRevision)
	}
	if statement.NextRegistryRevision <= statement.PreviousRegistryRevision {
		return TrustState{}, errors.New("audit root continuity next registry revision must advance monotonically")
	}
	if nextRegistry.Revision != statement.NextRegistryRevision {
		return TrustState{}, errors.New("audit root continuity next registry revision does not match the supplied registry")
	}
	if err := verifyContinuitySignatures(continuity, state.CurrentAnchor); err != nil {
		return TrustState{}, err
	}
	if err := verifyRegistry(nextRegistry, statement.NextAnchor.RootKeyID); err != nil {
		return TrustState{}, fmt.Errorf("verify next audit trust registry: %w", err)
	}
	if nextRegistry.RootPublicKey != statement.NextAnchor.RootPublicKey {
		return TrustState{}, errors.New("next audit trust registry public root does not match continuity statement")
	}

	state.CurrentAnchor = statement.NextAnchor
	state.HighestAcceptedRegistryRevision = nextRegistry.Revision
	state.UpdatedAt = formatTime(normalizeTime(now))
	if err := saveTrustState(path, state); err != nil {
		return TrustState{}, err
	}
	return state, nil
}

func MarshalTrustState(state TrustState) ([]byte, error) {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func MarshalRootContinuity(continuity RootContinuity) ([]byte, error) {
	data, err := json.MarshalIndent(continuity, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func LoadRootContinuityFile(path string) (RootContinuity, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return RootContinuity{}, fmt.Errorf("read audit root continuity: %w", err)
	}
	var continuity RootContinuity
	if err := json.Unmarshal(data, &continuity); err != nil {
		return RootContinuity{}, fmt.Errorf("decode audit root continuity: %w", err)
	}
	return continuity, nil
}

func validateRootContinuity(continuity RootContinuity, nextRegistry Registry) error {
	if continuity.Contract != RootContinuityContract {
		return errors.New("unsupported audit root continuity contract")
	}
	if continuity.Algorithm != Algorithm {
		return errors.New("unsupported audit root continuity algorithm")
	}
	statement := continuity.Statement
	if statement.Contract != RootContinuityStatementV1 {
		return errors.New("unsupported audit root continuity statement contract")
	}
	if strings.TrimSpace(statement.Subject.NodeID) == "" || strings.TrimSpace(statement.Subject.DeviceID) == "" {
		return errors.New("audit root continuity subject is invalid")
	}
	if statement.PreviousRegistryRevision <= 0 || statement.NextRegistryRevision <= statement.PreviousRegistryRevision {
		return errors.New("audit root continuity registry revisions are invalid")
	}
	if _, err := parseTime(statement.IssuedAt); err != nil {
		return errors.New("audit root continuity issued_at is invalid")
	}
	if err := validateAnchor(statement.PreviousAnchor); err != nil {
		return fmt.Errorf("previous audit root anchor: %w", err)
	}
	if err := validateAnchor(statement.NextAnchor); err != nil {
		return fmt.Errorf("next audit root anchor: %w", err)
	}
	if statement.PreviousAnchor == statement.NextAnchor {
		return errors.New("audit root continuity must change the root anchor")
	}
	if nextRegistry.Contract != RegistryContract || nextRegistry.Subject != statement.Subject {
		return errors.New("next audit trust registry does not match root continuity subject or contract")
	}
	if nextRegistry.RootKeyID != statement.NextAnchor.RootKeyID || nextRegistry.RootPublicKey != statement.NextAnchor.RootPublicKey {
		return errors.New("next audit trust registry root does not match continuity statement")
	}
	if nextRegistry.Revision != statement.NextRegistryRevision {
		return errors.New("next audit trust registry revision does not match continuity statement")
	}
	return nil
}

func verifyContinuitySignatures(continuity RootContinuity, expectedPrevious TrustAnchor) error {
	if continuity.Statement.PreviousAnchor != expectedPrevious {
		return errors.New("audit root continuity previous anchor does not match the expected trust anchor")
	}
	payload, err := rootContinuitySigningBytes(continuity.Statement)
	if err != nil {
		return err
	}
	oldPublic, err := decodePublic(continuity.Statement.PreviousAnchor.RootPublicKey)
	if err != nil {
		return errors.New("audit root continuity previous root public key is invalid")
	}
	newPublic, err := decodePublic(continuity.Statement.NextAnchor.RootPublicKey)
	if err != nil {
		return errors.New("audit root continuity next root public key is invalid")
	}
	oldSig, err := decodeSignature(continuity.PreviousRootSignature)
	if err != nil || !ed25519.Verify(oldPublic, payload, oldSig) {
		return errors.New("audit root continuity previous-root signature verification failed")
	}
	newSig, err := decodeSignature(continuity.NextRootSignature)
	if err != nil || !ed25519.Verify(newPublic, payload, newSig) {
		return errors.New("audit root continuity next-root signature verification failed")
	}
	return nil
}

func rootContinuitySigningBytes(statement RootContinuityStatement) ([]byte, error) {
	return json.Marshal(statement)
}

func registryMatchesTrustState(state TrustState, registry Registry) error {
	if err := validateTrustState(state); err != nil {
		return err
	}
	if registry.Subject != state.Subject {
		return errors.New("audit trust registry subject does not match verifier trust state")
	}
	if registry.RootKeyID != state.CurrentAnchor.RootKeyID || registry.RootPublicKey != state.CurrentAnchor.RootPublicKey {
		return errors.New("audit trust registry root does not match verifier trust state")
	}
	if err := verifyRegistry(registry, state.CurrentAnchor.RootKeyID); err != nil {
		return err
	}
	return nil
}

func validateTrustState(state TrustState) error {
	if state.Contract != TrustStateContract {
		return errors.New("unsupported audit verifier trust state contract")
	}
	if strings.TrimSpace(state.Subject.NodeID) == "" || strings.TrimSpace(state.Subject.DeviceID) == "" {
		return errors.New("audit verifier trust state subject is invalid")
	}
	if err := validateAnchor(state.CurrentAnchor); err != nil {
		return err
	}
	if state.HighestAcceptedRegistryRevision <= 0 {
		return errors.New("audit verifier trust state registry revision is invalid")
	}
	if _, err := parseTime(state.UpdatedAt); err != nil {
		return errors.New("audit verifier trust state updated_at is invalid")
	}
	return nil
}

func validateAnchor(anchor TrustAnchor) error {
	publicKey, err := decodePublic(anchor.RootPublicKey)
	if err != nil {
		return errors.New("audit trust root public key is invalid")
	}
	if keyID("audit-root-sha256", publicKey) != strings.TrimSpace(anchor.RootKeyID) {
		return errors.New("audit trust root key id does not match its public key")
	}
	return nil
}

func saveTrustState(path string, state TrustState) error {
	if err := validateTrustState(state); err != nil {
		return err
	}
	data, err := MarshalTrustState(state)
	if err != nil {
		return fmt.Errorf("encode audit verifier trust state: %w", err)
	}
	if err := atomicfile.Write(path, data, 0o600); err != nil {
		return fmt.Errorf("write audit verifier trust state: %w", err)
	}
	return nil
}

func loadTrustState(path string) (TrustState, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return TrustState{}, fmt.Errorf("audit verifier trust state is not bootstrapped: %w", os.ErrNotExist)
		}
		return TrustState{}, fmt.Errorf("inspect audit verifier trust state: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return TrustState{}, errors.New("audit verifier trust state must be a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return TrustState{}, fmt.Errorf("read audit verifier trust state: %w", err)
	}
	var state TrustState
	if err := json.Unmarshal(data, &state); err != nil {
		return TrustState{}, fmt.Errorf("decode audit verifier trust state: %w", err)
	}
	if err := validateTrustState(state); err != nil {
		return TrustState{}, err
	}
	return state, nil
}

func acquireVerifierStateLock(home string) (func(), error) {
	home = strings.TrimSpace(home)
	if home == "" {
		return nil, errors.New("audit trust home is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	release, err := filelock.Acquire(ctx, filepath.Join(home, "audit-trust", ".verifier-state.lock"))
	cancel()
	if err != nil {
		return nil, fmt.Errorf("acquire audit verifier trust state lock: %w", err)
	}
	return release, nil
}

func trustStatePath(home string) string {
	return filepath.Join(home, "audit-trust", "trust-state-v1.json")
}

func RootContinuityRecoveryPath(home string, continuity RootContinuity) string {
	return filepath.Join(
		strings.TrimSpace(home),
		"audit-trust",
		fmt.Sprintf(
			"root-continuity-r%d-r%d.json",
			continuity.Statement.PreviousRegistryRevision,
			continuity.Statement.NextRegistryRevision,
		),
	)
}
