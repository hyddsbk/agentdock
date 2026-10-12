package execevidence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const ExecutorIssuer = "agentdock.exec_command/v1"

type Receipt struct {
	Issuer        string    `json:"issuer"`
	SessionID     string    `json:"session_id"`
	Command       string    `json:"command"`
	CommandSHA256 string    `json:"command_sha256"`
	ExitCode      int       `json:"exit_code"`
	Status        string    `json:"status"`
	TimedOut      bool      `json:"timed_out,omitempty"`
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at"`
	Runtime       string    `json:"runtime,omitempty"`
	Workdir       string    `json:"workdir,omitempty"`
	StdoutSHA256  string    `json:"stdout_sha256"`
	StderrSHA256  string    `json:"stderr_sha256"`
	ReceiptSHA256 string    `json:"receipt_sha256"`
}

type Claim struct {
	Ref       string    `json:"ref"`
	ClaimKey  string    `json:"claim_key"`
	ClaimedAt time.Time `json:"claimed_at"`
}

type claimRecord = Claim

type Store struct {
	root         string
	receiptsRoot string
	claimsRoot   string
	mu           sync.Mutex
}

func New(root string) (*Store, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("execution evidence root is required")
	}
	store := &Store{
		root:         root,
		receiptsRoot: filepath.Join(root, "receipts"),
		claimsRoot:   filepath.Join(root, "claims"),
	}
	for _, dir := range []string{store.receiptsRoot, store.claimsRoot} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("create execution evidence directory: %w", err)
		}
	}
	return store, nil
}

func (s *Store) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

func (s *Store) Issue(receipt Receipt) (string, error) {
	if s == nil {
		return "", errors.New("execution evidence store is not configured")
	}
	normalized, err := normalizeReceipt(receipt)
	if err != nil {
		return "", err
	}
	ref := receiptRef(normalized.SessionID)
	path := s.receiptPath(normalized.SessionID)

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, err := s.resolvePath(path, normalized.SessionID); err == nil {
		if existing.ReceiptSHA256 != normalized.ReceiptSHA256 {
			return "", fmt.Errorf("execution receipt collision for %s", normalized.SessionID)
		}
		return ref, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := writeJSONExclusive(path, normalized); err != nil {
		if errors.Is(err, os.ErrExist) {
			existing, readErr := s.resolvePath(path, normalized.SessionID)
			if readErr == nil && existing.ReceiptSHA256 == normalized.ReceiptSHA256 {
				return ref, nil
			}
		}
		return "", fmt.Errorf("persist execution receipt: %w", err)
	}
	return ref, nil
}

func (s *Store) Resolve(ref string) (Receipt, error) {
	if s == nil {
		return Receipt{}, errors.New("execution evidence store is not configured")
	}
	sessionID, err := parseRef(ref)
	if err != nil {
		return Receipt{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resolvePath(s.receiptPath(sessionID), sessionID)
}

// VerifyReceipt validates one portable receipt without consulting a Store.
// It recomputes the command and receipt digests from the serialized fields and
// fails closed when the embedded receipt_sha256 no longer matches.
func VerifyReceipt(receipt Receipt) (Receipt, error) {
	storedDigest := strings.ToLower(strings.TrimSpace(receipt.ReceiptSHA256))
	receipt.ReceiptSHA256 = ""
	normalized, err := normalizeReceipt(receipt)
	if err != nil {
		return Receipt{}, err
	}
	if storedDigest == "" || storedDigest != normalized.ReceiptSHA256 {
		return Receipt{}, errors.New("execution receipt integrity check failed")
	}
	return normalized, nil
}

// ResolveClaim returns the durable owner reservation for one executor receipt.
// It never creates or changes a claim and fails closed on missing or malformed data.
func (s *Store) ResolveClaim(ref string) (Claim, error) {
	if s == nil {
		return Claim{}, errors.New("execution evidence store is not configured")
	}
	sessionID, err := parseRef(ref)
	if err != nil {
		return Claim{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.claimPath(sessionID))
	if err != nil {
		return Claim{}, fmt.Errorf("read execution receipt claim: %w", err)
	}
	var claim Claim
	if err := json.Unmarshal(data, &claim); err != nil {
		return Claim{}, fmt.Errorf("decode execution receipt claim: %w", err)
	}
	expectedRef := receiptRef(sessionID)
	if claim.Ref != expectedRef || strings.TrimSpace(claim.ClaimKey) == "" || len(claim.ClaimKey) > 512 || claim.ClaimedAt.IsZero() {
		return Claim{}, errors.New("execution receipt claim is invalid")
	}
	return claim, nil
}

// WithClaims resolves and durably reserves every receipt for claimKey before fn.
// If fn fails, only claims created by this call are rolled back. Existing
// same-key claims make retries idempotent; any other claim fails closed.
func (s *Store) WithClaims(refs []string, claimKey string, fn func([]Receipt) error) error {
	if s == nil {
		return errors.New("execution evidence store is not configured")
	}
	claimKey = strings.TrimSpace(claimKey)
	if claimKey == "" || len(claimKey) > 512 {
		return errors.New("execution receipt claim key is invalid")
	}
	if len(refs) == 0 {
		return errors.New("execution receipt refs are required")
	}
	if fn == nil {
		return errors.New("execution receipt claim callback is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	seen := map[string]struct{}{}
	receipts := make([]Receipt, 0, len(refs))
	sessionIDs := make([]string, 0, len(refs))
	for _, raw := range refs {
		ref := strings.TrimSpace(raw)
		if _, ok := seen[ref]; ok {
			return fmt.Errorf("execution receipt %s is duplicated", ref)
		}
		seen[ref] = struct{}{}
		sessionID, err := parseRef(ref)
		if err != nil {
			return err
		}
		receipt, err := s.resolvePath(s.receiptPath(sessionID), sessionID)
		if err != nil {
			return fmt.Errorf("resolve execution receipt %s: %w", ref, err)
		}
		receipts = append(receipts, receipt)
		sessionIDs = append(sessionIDs, sessionID)
	}

	created := make([]string, 0, len(refs))
	for i, sessionID := range sessionIDs {
		path := s.claimPath(sessionID)
		var existing claimRecord
		data, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := json.Unmarshal(data, &existing); err != nil {
				return fmt.Errorf("decode execution receipt claim: %w", err)
			}
			if existing.ClaimKey != claimKey {
				return fmt.Errorf("execution receipt %s already claimed by %s", refs[i], existing.ClaimKey)
			}
		case errors.Is(err, os.ErrNotExist):
			record := claimRecord{Ref: receiptRef(sessionID), ClaimKey: claimKey, ClaimedAt: time.Now().UTC()}
			if err := writeJSONExclusive(path, record); err != nil {
				return fmt.Errorf("claim execution receipt %s: %w", refs[i], err)
			}
			created = append(created, path)
		default:
			return fmt.Errorf("read execution receipt claim: %w", err)
		}
	}

	if err := fn(receipts); err != nil {
		for _, path := range created {
			_ = os.Remove(path)
		}
		return err
	}
	return nil
}

func normalizeReceipt(receipt Receipt) (Receipt, error) {
	receipt.Issuer = strings.TrimSpace(receipt.Issuer)
	if receipt.Issuer != ExecutorIssuer {
		return Receipt{}, fmt.Errorf("unsupported execution receipt issuer %q", receipt.Issuer)
	}
	if _, err := validateSessionID(receipt.SessionID); err != nil {
		return Receipt{}, err
	}
	receipt.Command = strings.TrimSpace(receipt.Command)
	if receipt.Command == "" || len(receipt.Command) > 16384 {
		return Receipt{}, errors.New("execution receipt command is invalid")
	}
	receipt.Status = strings.ToLower(strings.TrimSpace(receipt.Status))
	if receipt.Status == "" {
		return Receipt{}, errors.New("execution receipt status is required")
	}
	if receipt.StartedAt.IsZero() || receipt.FinishedAt.IsZero() || receipt.FinishedAt.Before(receipt.StartedAt) {
		return Receipt{}, errors.New("execution receipt timestamps are invalid")
	}
	if !validSHA256(receipt.StdoutSHA256) || !validSHA256(receipt.StderrSHA256) {
		return Receipt{}, errors.New("execution receipt output digest is invalid")
	}
	receipt.CommandSHA256 = sha256Hex(receipt.Command)
	receipt.ReceiptSHA256 = receiptDigest(receipt)
	return receipt, nil
}

func receiptDigest(receipt Receipt) string {
	payload := struct {
		Issuer        string    `json:"issuer"`
		SessionID     string    `json:"session_id"`
		Command       string    `json:"command"`
		CommandSHA256 string    `json:"command_sha256"`
		ExitCode      int       `json:"exit_code"`
		Status        string    `json:"status"`
		TimedOut      bool      `json:"timed_out,omitempty"`
		StartedAt     time.Time `json:"started_at"`
		FinishedAt    time.Time `json:"finished_at"`
		Runtime       string    `json:"runtime,omitempty"`
		Workdir       string    `json:"workdir,omitempty"`
		StdoutSHA256  string    `json:"stdout_sha256"`
		StderrSHA256  string    `json:"stderr_sha256"`
	}{
		receipt.Issuer, receipt.SessionID, receipt.Command, receipt.CommandSHA256,
		receipt.ExitCode, receipt.Status, receipt.TimedOut, receipt.StartedAt.UTC(), receipt.FinishedAt.UTC(),
		receipt.Runtime, receipt.Workdir, receipt.StdoutSHA256, receipt.StderrSHA256,
	}
	data, _ := json.Marshal(payload)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *Store) resolvePath(path, sessionID string) (Receipt, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Receipt{}, err
	}
	var receipt Receipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return Receipt{}, fmt.Errorf("decode execution receipt: %w", err)
	}
	if receipt.SessionID != sessionID {
		return Receipt{}, errors.New("execution receipt session id mismatch")
	}
	return VerifyReceipt(receipt)
}

func (s *Store) receiptPath(sessionID string) string {
	return filepath.Join(s.receiptsRoot, sessionID+".json")
}

func (s *Store) claimPath(sessionID string) string {
	return filepath.Join(s.claimsRoot, sessionID+".json")
}

func receiptRef(sessionID string) string {
	return "exec://" + sessionID
}

func parseRef(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, "exec://") {
		return "", errors.New("execution receipt ref must use exec://")
	}
	return validateSessionID(strings.TrimPrefix(ref, "exec://"))
}

func validateSessionID(sessionID string) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if !strings.HasPrefix(sessionID, "session-") {
		return "", errors.New("execution receipt session id is invalid")
	}
	raw := strings.TrimPrefix(sessionID, "session-")
	if len(raw) != 24 {
		return "", errors.New("execution receipt session id is invalid")
	}
	if _, err := hex.DecodeString(raw); err != nil {
		return "", errors.New("execution receipt session id is invalid")
	}
	return sessionID, nil
}

func validSHA256(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func CommandSHA256(command string) string {
	return sha256Hex(strings.TrimSpace(command))
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func writeJSONExclusive(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	ok = true
	return file.Close()
}
