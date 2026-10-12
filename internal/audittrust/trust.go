package audittrust

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/fs/atomicfile"
	"github.com/uvwt/agentdock/internal/fs/filelock"
)

const (
	RegistryContract  = "audit-trust-registry/v1"
	SignatureContract = "audit-detached-signature/v1"
	StatementContract = "audit-signature-statement/v1"
	Algorithm         = "ed25519"
	storeVersion      = 1
)

const (
	KeyActive  = "active"
	KeyRetired = "retired"
	KeyRevoked = "revoked"
)

type Subject struct {
	NodeID   string `json:"node_id"`
	DeviceID string `json:"device_id"`
}

type TrustedKey struct {
	KeyID     string `json:"key_id"`
	Algorithm string `json:"algorithm"`
	PublicKey string `json:"public_key"`
	Status    string `json:"status"`
	ValidFrom string `json:"valid_from"`
	RetiredAt string `json:"retired_at,omitempty"`
	RevokedAt string `json:"revoked_at,omitempty"`
}

type Registry struct {
	Contract      string       `json:"contract"`
	Revision      int          `json:"revision"`
	Subject       Subject      `json:"subject"`
	RootKeyID     string       `json:"root_key_id"`
	RootPublicKey string       `json:"root_public_key"`
	Keys          []TrustedKey `json:"keys"`
	UpdatedAt     string       `json:"updated_at"`
	Signature     string       `json:"signature"`
}

type Statement struct {
	Contract         string `json:"contract"`
	BundleContract   string `json:"bundle_contract"`
	BundleSHA256     string `json:"bundle_sha256"`
	TaskID           string `json:"task_id"`
	ReviewRevision   string `json:"review_revision"`
	NodeID           string `json:"node_id"`
	DeviceID         string `json:"device_id"`
	RootKeyID        string `json:"root_key_id"`
	RegistryRevision int    `json:"registry_revision"`
	KeyID            string `json:"key_id"`
	SignedAt         string `json:"signed_at"`
}

type DetachedSignature struct {
	Contract  string    `json:"contract"`
	Algorithm string    `json:"algorithm"`
	Statement Statement `json:"statement"`
	Signature string    `json:"signature"`
}

type Finding struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type VerificationReport struct {
	Contract         string    `json:"contract"`
	Valid            bool      `json:"valid"`
	Verdict          string    `json:"verdict"`
	BundleSHA256     string    `json:"bundle_sha256,omitempty"`
	RootKeyID        string    `json:"root_key_id,omitempty"`
	RegistryRevision int       `json:"registry_revision,omitempty"`
	KeyID            string    `json:"key_id,omitempty"`
	NodeID           string    `json:"node_id,omitempty"`
	DeviceID         string    `json:"device_id,omitempty"`
	Findings         []Finding `json:"findings,omitempty"`
}

type SignInput struct {
	BundleContract string
	BundleSHA256   string
	TaskID         string
	ReviewRevision string
}

type privateStore struct {
	Version            int               `json:"version"`
	Subject            Subject           `json:"subject"`
	RootPrivateKey     string            `json:"root_private_key"`
	SigningPrivateKeys map[string]string `json:"signing_private_keys"`
	Registry           Registry          `json:"registry"`
}

type registryPayload struct {
	Contract      string       `json:"contract"`
	Revision      int          `json:"revision"`
	Subject       Subject      `json:"subject"`
	RootKeyID     string       `json:"root_key_id"`
	RootPublicKey string       `json:"root_public_key"`
	Keys          []TrustedKey `json:"keys"`
	UpdatedAt     string       `json:"updated_at"`
}

func Init(home string, subject Subject, now time.Time) (Registry, error) {
	home = strings.TrimSpace(home)
	subject.NodeID = strings.TrimSpace(subject.NodeID)
	subject.DeviceID = strings.TrimSpace(subject.DeviceID)
	if home == "" {
		return Registry{}, errors.New("audit trust home is required")
	}
	if subject.NodeID == "" || subject.DeviceID == "" {
		return Registry{}, errors.New("audit trust requires node_id and device_id")
	}
	release, err := acquireStoreLock(home)
	if err != nil {
		return Registry{}, err
	}
	defer release()
	path := storePath(home)
	if _, err := os.Lstat(path); err == nil {
		return Registry{}, errors.New("audit trust store already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return Registry{}, fmt.Errorf("inspect audit trust store: %w", err)
	}

	rootPublic, rootPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Registry{}, fmt.Errorf("generate audit root key: %w", err)
	}
	signPublic, signPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Registry{}, fmt.Errorf("generate audit signing key: %w", err)
	}
	now = normalizeTime(now)
	rootID := keyID("audit-root-sha256", rootPublic)
	signID := keyID("audit-key-sha256", signPublic)
	registry := Registry{
		Contract:      RegistryContract,
		Revision:      1,
		Subject:       subject,
		RootKeyID:     rootID,
		RootPublicKey: encodeBytes(rootPublic),
		Keys: []TrustedKey{{
			KeyID: signID, Algorithm: Algorithm, PublicKey: encodeBytes(signPublic),
			Status: KeyActive, ValidFrom: formatTime(now),
		}},
		UpdatedAt: formatTime(now),
	}
	if err := signRegistry(&registry, rootPrivate); err != nil {
		return Registry{}, err
	}
	store := privateStore{
		Version: storeVersion, Subject: subject, RootPrivateKey: encodeBytes(rootPrivate),
		SigningPrivateKeys: map[string]string{signID: encodeBytes(signPrivate)}, Registry: registry,
	}
	if err := saveStore(path, store); err != nil {
		return Registry{}, err
	}
	return registry, nil
}

func RegistryFromHome(home string) (Registry, error) {
	store, err := loadStore(storePath(strings.TrimSpace(home)))
	if err != nil {
		return Registry{}, err
	}
	if err := validatePrivateStore(store); err != nil {
		return Registry{}, err
	}
	return store.Registry, nil
}

func Rotate(home string, now time.Time) (Registry, error) {
	home = strings.TrimSpace(home)
	release, err := acquireStoreLock(home)
	if err != nil {
		return Registry{}, err
	}
	defer release()
	path := storePath(home)
	store, err := loadStore(path)
	if err != nil {
		return Registry{}, err
	}
	if err := validatePrivateStore(store); err != nil {
		return Registry{}, err
	}
	now = normalizeTime(now)
	active := -1
	for i := range store.Registry.Keys {
		if store.Registry.Keys[i].Status == KeyActive {
			if active >= 0 {
				return Registry{}, errors.New("audit trust registry has multiple active signing keys")
			}
			active = i
		}
	}
	if active < 0 {
		return Registry{}, errors.New("audit trust registry has no active signing key")
	}
	oldKeyID := store.Registry.Keys[active].KeyID
	store.Registry.Keys[active].Status = KeyRetired
	store.Registry.Keys[active].RetiredAt = formatTime(now)
	delete(store.SigningPrivateKeys, oldKeyID)

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Registry{}, fmt.Errorf("generate rotated audit signing key: %w", err)
	}
	id := keyID("audit-key-sha256", pub)
	store.Registry.Keys = append(store.Registry.Keys, TrustedKey{
		KeyID: id, Algorithm: Algorithm, PublicKey: encodeBytes(pub), Status: KeyActive, ValidFrom: formatTime(now),
	})
	if store.SigningPrivateKeys == nil {
		store.SigningPrivateKeys = map[string]string{}
	}
	store.SigningPrivateKeys[id] = encodeBytes(priv)
	store.Registry.Revision++
	store.Registry.UpdatedAt = formatTime(now)
	rootPrivate, err := decodePrivate(store.RootPrivateKey)
	if err != nil {
		return Registry{}, fmt.Errorf("decode audit root private key: %w", err)
	}
	if err := signRegistry(&store.Registry, rootPrivate); err != nil {
		return Registry{}, err
	}
	if err := saveStore(path, store); err != nil {
		return Registry{}, err
	}
	return store.Registry, nil
}

func Revoke(home, keyIDValue string, now time.Time) (Registry, error) {
	home = strings.TrimSpace(home)
	release, err := acquireStoreLock(home)
	if err != nil {
		return Registry{}, err
	}
	defer release()
	path := storePath(home)
	store, err := loadStore(path)
	if err != nil {
		return Registry{}, err
	}
	if err := validatePrivateStore(store); err != nil {
		return Registry{}, err
	}
	keyIDValue = strings.TrimSpace(keyIDValue)
	if keyIDValue == "" {
		return Registry{}, errors.New("audit signing key id is required")
	}
	now = normalizeTime(now)
	index := -1
	for i := range store.Registry.Keys {
		if store.Registry.Keys[i].KeyID == keyIDValue {
			index = i
			break
		}
	}
	if index < 0 {
		return Registry{}, fmt.Errorf("audit signing key %s not found", keyIDValue)
	}
	if store.Registry.Keys[index].Status == KeyRevoked {
		return Registry{}, fmt.Errorf("audit signing key %s is already revoked", keyIDValue)
	}
	wasActive := store.Registry.Keys[index].Status == KeyActive
	store.Registry.Keys[index].Status = KeyRevoked
	store.Registry.Keys[index].RevokedAt = formatTime(now)
	store.Registry.Keys[index].RetiredAt = ""
	delete(store.SigningPrivateKeys, keyIDValue)

	if wasActive {
		pub, priv, genErr := ed25519.GenerateKey(rand.Reader)
		if genErr != nil {
			return Registry{}, fmt.Errorf("generate replacement audit signing key: %w", genErr)
		}
		id := keyID("audit-key-sha256", pub)
		store.Registry.Keys = append(store.Registry.Keys, TrustedKey{
			KeyID: id, Algorithm: Algorithm, PublicKey: encodeBytes(pub), Status: KeyActive, ValidFrom: formatTime(now),
		})
		if store.SigningPrivateKeys == nil {
			store.SigningPrivateKeys = map[string]string{}
		}
		store.SigningPrivateKeys[id] = encodeBytes(priv)
	}
	store.Registry.Revision++
	store.Registry.UpdatedAt = formatTime(now)
	rootPrivate, err := decodePrivate(store.RootPrivateKey)
	if err != nil {
		return Registry{}, fmt.Errorf("decode audit root private key: %w", err)
	}
	if err := signRegistry(&store.Registry, rootPrivate); err != nil {
		return Registry{}, err
	}
	if err := saveStore(path, store); err != nil {
		return Registry{}, err
	}
	return store.Registry, nil
}

func Sign(home string, input SignInput, now time.Time) (DetachedSignature, error) {
	signature, _, err := SignWithRegistry(home, input, now)
	return signature, err
}

func SignWithRegistry(home string, input SignInput, now time.Time) (DetachedSignature, Registry, error) {
	home = strings.TrimSpace(home)
	release, err := acquireStoreLock(home)
	if err != nil {
		return DetachedSignature{}, Registry{}, err
	}
	defer release()

	store, err := loadStore(storePath(home))
	if err != nil {
		return DetachedSignature{}, Registry{}, err
	}
	if err := validatePrivateStore(store); err != nil {
		return DetachedSignature{}, Registry{}, err
	}
	signature, err := signFromStore(store, input, now)
	if err != nil {
		return DetachedSignature{}, Registry{}, err
	}
	return signature, store.Registry, nil
}

func signFromStore(store privateStore, input SignInput, now time.Time) (DetachedSignature, error) {
	input.BundleContract = strings.TrimSpace(input.BundleContract)
	input.BundleSHA256 = strings.ToLower(strings.TrimSpace(input.BundleSHA256))
	input.TaskID = strings.TrimSpace(input.TaskID)
	input.ReviewRevision = strings.TrimSpace(input.ReviewRevision)
	if input.BundleContract == "" || input.TaskID == "" || input.ReviewRevision == "" {
		return DetachedSignature{}, errors.New("audit signature requires bundle contract, task_id, and review_revision")
	}
	if err := validateSHA256(input.BundleSHA256); err != nil {
		return DetachedSignature{}, fmt.Errorf("audit bundle digest: %w", err)
	}
	var key TrustedKey
	found := false
	for _, candidate := range store.Registry.Keys {
		if candidate.Status == KeyActive {
			if found {
				return DetachedSignature{}, errors.New("audit trust registry has multiple active signing keys")
			}
			key = candidate
			found = true
		}
	}
	if !found {
		return DetachedSignature{}, errors.New("audit trust registry has no active signing key")
	}
	privateEncoded, ok := store.SigningPrivateKeys[key.KeyID]
	if !ok {
		return DetachedSignature{}, fmt.Errorf("audit signing private key %s is unavailable", key.KeyID)
	}
	privateKey, err := decodePrivate(privateEncoded)
	if err != nil {
		return DetachedSignature{}, fmt.Errorf("decode audit signing private key: %w", err)
	}
	statement := Statement{
		Contract: StatementContract, BundleContract: input.BundleContract, BundleSHA256: input.BundleSHA256,
		TaskID: input.TaskID, ReviewRevision: input.ReviewRevision,
		NodeID: store.Registry.Subject.NodeID, DeviceID: store.Registry.Subject.DeviceID,
		RootKeyID: store.Registry.RootKeyID, RegistryRevision: store.Registry.Revision,
		KeyID: key.KeyID, SignedAt: formatTime(normalizeTime(now)),
	}
	message, err := json.Marshal(statement)
	if err != nil {
		return DetachedSignature{}, fmt.Errorf("encode audit signature statement: %w", err)
	}
	signature := ed25519.Sign(privateKey, message)
	return DetachedSignature{
		Contract: SignatureContract, Algorithm: Algorithm, Statement: statement, Signature: encodeBytes(signature),
	}, nil
}

func Verify(registry Registry, signature DetachedSignature, expectedRootKeyID, expectedBundleSHA256 string, now time.Time) VerificationReport {
	return VerifyExpected(registry, signature, expectedRootKeyID, expectedBundleSHA256, 0, now)
}

func VerifyExpected(registry Registry, signature DetachedSignature, expectedRootKeyID, expectedBundleSHA256 string, expectedMinRegistryRevision int, now time.Time) VerificationReport {
	report := VerificationReport{
		Contract:         SignatureContract,
		BundleSHA256:     strings.ToLower(strings.TrimSpace(signature.Statement.BundleSHA256)),
		RootKeyID:        strings.TrimSpace(signature.Statement.RootKeyID),
		RegistryRevision: registry.Revision,
		KeyID:            strings.TrimSpace(signature.Statement.KeyID),
		NodeID:           strings.TrimSpace(signature.Statement.NodeID),
		DeviceID:         strings.TrimSpace(signature.Statement.DeviceID),
	}
	add := func(code, message string) {
		report.Findings = append(report.Findings, Finding{Code: code, Message: message})
	}

	if signature.Contract != SignatureContract {
		add("SIGNATURE_CONTRACT_MISMATCH", "unsupported detached audit signature contract")
	}
	if signature.Algorithm != Algorithm {
		add("SIGNATURE_ALGORITHM_MISMATCH", "unsupported detached audit signature algorithm")
	}
	if signature.Statement.Contract != StatementContract {
		add("STATEMENT_CONTRACT_MISMATCH", "unsupported audit signature statement contract")
	}
	if err := verifyRegistry(registry, strings.TrimSpace(expectedRootKeyID)); err != nil {
		add("TRUST_REGISTRY_INVALID", err.Error())
	}
	if expectedMinRegistryRevision > 0 && registry.Revision < expectedMinRegistryRevision {
		add("TRUST_REGISTRY_STALE", "trusted registry revision is older than the externally pinned minimum")
	}
	if signature.Statement.RegistryRevision <= 0 {
		add("SIGNED_REGISTRY_REVISION_INVALID", "signature registry revision is invalid")
	} else if registry.Revision < signature.Statement.RegistryRevision {
		add("TRUST_REGISTRY_STALE", "trusted registry predates the registry revision used to create the signature")
	}
	if signature.Statement.NodeID != registry.Subject.NodeID || signature.Statement.DeviceID != registry.Subject.DeviceID {
		add("SIGNER_IDENTITY_MISMATCH", "signature subject does not match the trusted registry subject")
	}
	if signature.Statement.RootKeyID != registry.RootKeyID {
		add("ROOT_KEY_ID_MISMATCH", "signature root key id does not match the trusted registry")
	}
	expectedBundleSHA256 = strings.ToLower(strings.TrimSpace(expectedBundleSHA256))
	if expectedBundleSHA256 != "" {
		if err := validateSHA256(expectedBundleSHA256); err != nil {
			add("EXPECTED_BUNDLE_DIGEST_INVALID", "expected bundle SHA-256 is invalid")
		} else if !strings.EqualFold(expectedBundleSHA256, signature.Statement.BundleSHA256) {
			add("SIGNED_BUNDLE_DIGEST_MISMATCH", "signature does not cover the expected portable audit bundle")
		}
	}

	signedAt, err := parseTime(signature.Statement.SignedAt)
	if err != nil {
		add("SIGNED_AT_INVALID", "signature signed_at is invalid")
	} else if signedAt.After(normalizeTime(now).Add(5 * time.Minute)) {
		add("SIGNED_AT_FUTURE", "signature signed_at is unreasonably in the future")
	}
	key, ok := findKey(registry.Keys, signature.Statement.KeyID)
	if !ok {
		add("SIGNING_KEY_UNTRUSTED", "signature key is not present in the trusted registry")
	} else {
		if key.Algorithm != Algorithm {
			add("SIGNING_KEY_ALGORITHM_MISMATCH", "trusted signing key uses an unsupported algorithm")
		}
		validFrom, validErr := parseTime(key.ValidFrom)
		if validErr != nil {
			add("SIGNING_KEY_VALIDITY_INVALID", "trusted signing key valid_from is invalid")
		} else if err == nil && signedAt.Before(validFrom) {
			add("SIGNATURE_BEFORE_KEY_VALIDITY", "signature predates the trusted signing key")
		}
		switch key.Status {
		case KeyActive:
		case KeyRetired:
			retiredAt, retiredErr := parseTime(key.RetiredAt)
			if retiredErr != nil {
				add("SIGNING_KEY_RETIREMENT_INVALID", "retired signing key has no valid retired_at")
			} else if err == nil && signedAt.After(retiredAt) {
				add("SIGNATURE_AFTER_KEY_RETIREMENT", "signature was created after the signing key was retired")
			}
		case KeyRevoked:
			add("SIGNING_KEY_REVOKED", "signature key is revoked by the trusted registry")
		default:
			add("SIGNING_KEY_STATUS_INVALID", "trusted signing key has an unsupported status")
		}

		publicKey, pubErr := decodePublic(key.PublicKey)
		if pubErr != nil {
			add("SIGNING_KEY_INVALID", "trusted signing public key is invalid")
		} else if keyID("audit-key-sha256", publicKey) != key.KeyID {
			add("SIGNING_KEY_ID_MISMATCH", "trusted signing key id does not match its public key")
		} else {
			rawSig, sigErr := decodeSignature(signature.Signature)
			if sigErr != nil {
				add("SIGNATURE_ENCODING_INVALID", "detached audit signature is invalid")
			} else {
				message, marshalErr := json.Marshal(signature.Statement)
				if marshalErr != nil {
					add("STATEMENT_ENCODING_ERROR", "audit signature statement could not be encoded")
				} else if !ed25519.Verify(publicKey, message, rawSig) {
					add("SIGNATURE_INVALID", "detached audit signature verification failed")
				}
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

func LoadRegistryFile(path string) (Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Registry{}, fmt.Errorf("read audit trust registry: %w", err)
	}
	var registry Registry
	if err := json.Unmarshal(data, &registry); err != nil {
		return Registry{}, fmt.Errorf("decode audit trust registry: %w", err)
	}
	return registry, nil
}

func LoadSignatureFile(path string) (DetachedSignature, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return DetachedSignature{}, fmt.Errorf("read audit detached signature: %w", err)
	}
	var signature DetachedSignature
	if err := json.Unmarshal(data, &signature); err != nil {
		return DetachedSignature{}, fmt.Errorf("decode audit detached signature: %w", err)
	}
	return signature, nil
}

func MarshalRegistry(registry Registry) ([]byte, error) {
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func MarshalSignature(signature DetachedSignature) ([]byte, error) {
	data, err := json.MarshalIndent(signature, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func ActiveKeyID(registry Registry) string {
	for _, key := range registry.Keys {
		if key.Status == KeyActive {
			return key.KeyID
		}
	}
	return ""
}

func verifyRegistry(registry Registry, expectedRootKeyID string) error {
	if registry.Contract != RegistryContract {
		return errors.New("unsupported audit trust registry contract")
	}
	if registry.Revision <= 0 || strings.TrimSpace(registry.Subject.NodeID) == "" || strings.TrimSpace(registry.Subject.DeviceID) == "" {
		return errors.New("audit trust registry identity or revision is invalid")
	}
	if _, err := parseTime(registry.UpdatedAt); err != nil {
		return errors.New("audit trust registry updated_at is invalid")
	}
	rootPublic, err := decodePublic(registry.RootPublicKey)
	if err != nil {
		return errors.New("audit trust root public key is invalid")
	}
	rootID := keyID("audit-root-sha256", rootPublic)
	if rootID != registry.RootKeyID {
		return errors.New("audit trust root key id does not match its public key")
	}
	expectedRootKeyID = strings.TrimSpace(expectedRootKeyID)
	if expectedRootKeyID == "" {
		return errors.New("expected audit trust root key id is required")
	}
	if expectedRootKeyID != registry.RootKeyID {
		return errors.New("audit trust registry does not match the pinned root key id")
	}
	rawSignature, err := decodeSignature(registry.Signature)
	if err != nil {
		return errors.New("audit trust registry signature is invalid")
	}
	payload, err := registrySigningBytes(registry)
	if err != nil {
		return err
	}
	if !ed25519.Verify(rootPublic, payload, rawSignature) {
		return errors.New("audit trust registry root signature verification failed")
	}
	seen := map[string]bool{}
	active := 0
	for _, key := range registry.Keys {
		if strings.TrimSpace(key.KeyID) == "" || seen[key.KeyID] {
			return errors.New("audit trust registry contains duplicate or empty signing key ids")
		}
		seen[key.KeyID] = true
		publicKey, err := decodePublic(key.PublicKey)
		if err != nil || keyID("audit-key-sha256", publicKey) != key.KeyID {
			return errors.New("audit trust registry contains an invalid signing public key")
		}
		if key.Algorithm != Algorithm {
			return errors.New("audit trust registry contains an unsupported signing algorithm")
		}
		validFrom, err := parseTime(key.ValidFrom)
		if err != nil {
			return errors.New("audit trust registry contains invalid key validity")
		}
		switch key.Status {
		case KeyActive:
			active++
			if key.RetiredAt != "" || key.RevokedAt != "" {
				return errors.New("active audit signing key contains terminal lifecycle timestamps")
			}
		case KeyRetired:
			retiredAt, retiredErr := parseTime(key.RetiredAt)
			if retiredErr != nil || key.RevokedAt != "" || retiredAt.Before(validFrom) {
				return errors.New("retired audit signing key lifecycle is invalid")
			}
		case KeyRevoked:
			revokedAt, revokedErr := parseTime(key.RevokedAt)
			if revokedErr != nil || revokedAt.Before(validFrom) {
				return errors.New("revoked audit signing key lifecycle is invalid")
			}
		default:
			return errors.New("audit trust registry contains an unsupported key status")
		}
	}
	if active != 1 {
		return errors.New("audit trust registry must contain exactly one active signing key")
	}
	return nil
}

func validatePrivateStore(store privateStore) error {
	if store.Version != storeVersion {
		return errors.New("unsupported audit trust private store version")
	}
	if store.Subject != store.Registry.Subject {
		return errors.New("audit trust private store subject does not match registry")
	}
	rootPrivate, err := decodePrivate(store.RootPrivateKey)
	if err != nil {
		return errors.New("audit trust root private key is invalid")
	}
	rootPublic := rootPrivate.Public().(ed25519.PublicKey)
	if keyID("audit-root-sha256", rootPublic) != store.Registry.RootKeyID || encodeBytes(rootPublic) != store.Registry.RootPublicKey {
		return errors.New("audit trust root private key does not match registry")
	}
	if err := verifyRegistry(store.Registry, store.Registry.RootKeyID); err != nil {
		return err
	}
	for keyIDValue, encoded := range store.SigningPrivateKeys {
		privateKey, err := decodePrivate(encoded)
		if err != nil {
			return fmt.Errorf("audit signing private key %s is invalid", keyIDValue)
		}
		publicKey := privateKey.Public().(ed25519.PublicKey)
		if keyID("audit-key-sha256", publicKey) != keyIDValue {
			return fmt.Errorf("audit signing private key %s does not match its key id", keyIDValue)
		}
	}
	return nil
}

func verifyRegistryWithRoot(registry Registry, rootPublic ed25519.PublicKey) error {
	if registry.Contract != RegistryContract {
		return errors.New("unsupported audit trust registry contract")
	}
	payload, err := registrySigningBytes(registry)
	if err != nil {
		return err
	}
	signature, err := decodeSignature(registry.Signature)
	if err != nil || !ed25519.Verify(rootPublic, payload, signature) {
		return errors.New("audit trust registry root signature verification failed")
	}
	return nil
}

func signRegistry(registry *Registry, rootPrivate ed25519.PrivateKey) error {
	sort.Slice(registry.Keys, func(i, j int) bool { return registry.Keys[i].KeyID < registry.Keys[j].KeyID })
	registry.Signature = ""
	payload, err := registrySigningBytes(*registry)
	if err != nil {
		return err
	}
	registry.Signature = encodeBytes(ed25519.Sign(rootPrivate, payload))
	return nil
}

func registrySigningBytes(registry Registry) ([]byte, error) {
	keys := append([]TrustedKey(nil), registry.Keys...)
	sort.Slice(keys, func(i, j int) bool { return keys[i].KeyID < keys[j].KeyID })
	payload := registryPayload{
		Contract: registry.Contract, Revision: registry.Revision, Subject: registry.Subject,
		RootKeyID: registry.RootKeyID, RootPublicKey: registry.RootPublicKey,
		Keys: keys, UpdatedAt: registry.UpdatedAt,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode audit trust registry payload: %w", err)
	}
	return data, nil
}

func findKey(keys []TrustedKey, id string) (TrustedKey, bool) {
	for _, key := range keys {
		if key.KeyID == id {
			return key, true
		}
	}
	return TrustedKey{}, false
}

func saveStore(path string, store privateStore) error {
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return fmt.Errorf("encode audit trust private store: %w", err)
	}
	data = append(data, '\n')
	if err := atomicfile.Write(path, data, 0o600); err != nil {
		return fmt.Errorf("write audit trust private store: %w", err)
	}
	return nil
}

func loadStore(path string) (privateStore, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return privateStore{}, errors.New("audit trust store is not initialized")
		}
		return privateStore{}, fmt.Errorf("inspect audit trust store: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return privateStore{}, errors.New("audit trust store must be a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return privateStore{}, fmt.Errorf("read audit trust private store: %w", err)
	}
	var store privateStore
	if err := json.Unmarshal(data, &store); err != nil {
		return privateStore{}, fmt.Errorf("decode audit trust private store: %w", err)
	}
	return store, nil
}

func acquireStoreLock(home string) (func(), error) {
	home = strings.TrimSpace(home)
	if home == "" {
		return nil, errors.New("audit trust home is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	release, err := filelock.Acquire(ctx, filepath.Join(home, "audit-trust", ".store.lock"))
	cancel()
	if err != nil {
		return nil, fmt.Errorf("acquire audit trust store lock: %w", err)
	}
	return release, nil
}

func storePath(home string) string {
	return filepath.Join(home, "audit-trust", "private-v1.json")
}

func keyID(prefix string, publicKey ed25519.PublicKey) string {
	sum := sha256.Sum256(publicKey)
	return prefix + ":" + hex.EncodeToString(sum[:])
}

func encodeBytes(value []byte) string {
	return base64.RawStdEncoding.EncodeToString(value)
}

func decodePublic(value string) (ed25519.PublicKey, error) {
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("invalid ed25519 public key")
	}
	return ed25519.PublicKey(raw), nil
}

func decodePrivate(value string) (ed25519.PrivateKey, error) {
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil || len(raw) != ed25519.PrivateKeySize {
		return nil, errors.New("invalid ed25519 private key")
	}
	return ed25519.PrivateKey(raw), nil
}

func decodeSignature(value string) ([]byte, error) {
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil || len(raw) != ed25519.SignatureSize {
		return nil, errors.New("invalid ed25519 signature")
	}
	return raw, nil
}

func validateSHA256(value string) error {
	if len(value) != 64 {
		return errors.New("SHA-256 must be 64 hexadecimal characters")
	}
	_, err := hex.DecodeString(value)
	if err != nil {
		return errors.New("SHA-256 is not hexadecimal")
	}
	return nil
}

func normalizeTime(value time.Time) time.Time {
	if value.IsZero() {
		value = time.Now()
	}
	return value.UTC()
}

func formatTime(value time.Time) string {
	return normalizeTime(value).Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}
