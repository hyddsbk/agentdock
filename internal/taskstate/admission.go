package taskstate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/fs/atomicfile"
)

const admissionSchemaVersion = 1

type AdmissionStatus string

const (
	AdmissionPass    AdmissionStatus = "pass"
	AdmissionResolve AdmissionStatus = "resolve"
	AdmissionBlocked AdmissionStatus = "blocked"
)

type AdmissionInput struct {
	Goal                string   `json:"goal"`
	Deliverable         string   `json:"deliverable"`
	Audience            string   `json:"audience"`
	SuccessConditions   []string `json:"success_conditions"`
	FailureConditions   []string `json:"failure_conditions"`
	Constraints         []string `json:"constraints,omitempty"`
	EvidenceRequirement string   `json:"evidence_requirement"`
	MutationAuthority   string   `json:"mutation_authority"`
	Complexity          string   `json:"complexity"`
	WorkflowType        string   `json:"workflow_type,omitempty"`
	DecisionFrontier    []string `json:"decision_frontier,omitempty"`
	BlockedReasons      []string `json:"blocked_reasons,omitempty"`
	Project             string   `json:"project,omitempty"`
	Device              string   `json:"device,omitempty"`
}

type AdmissionContract struct {
	Goal                string   `json:"goal"`
	Deliverable         string   `json:"deliverable"`
	Audience            string   `json:"audience"`
	SuccessConditions   []string `json:"success_conditions"`
	FailureConditions   []string `json:"failure_conditions"`
	Constraints         []string `json:"constraints,omitempty"`
	EvidenceRequirement string   `json:"evidence_requirement"`
	MutationAuthority   string   `json:"mutation_authority"`
	Complexity          string   `json:"complexity"`
	WorkflowType        string   `json:"workflow_type,omitempty"`
	DecisionFrontier    []string `json:"decision_frontier,omitempty"`
	BlockedReasons      []string `json:"blocked_reasons,omitempty"`
	Project             string   `json:"project,omitempty"`
	Device              string   `json:"device,omitempty"`
}

type Admission struct {
	SchemaVersion int               `json:"schema_version"`
	ID            string            `json:"id"`
	Digest        string            `json:"digest"`
	Status        AdmissionStatus   `json:"status"`
	Contract      AdmissionContract `json:"contract"`
	CreatedAt     time.Time         `json:"created_at"`
}

type WorkflowMatchReceiptInput struct {
	Type         string
	Recommended  string
	CandidateIDs []string
}

type WorkflowMatchReceipt struct {
	SchemaVersion   int       `json:"schema_version"`
	AdmissionRef    string    `json:"admission_ref"`
	AdmissionDigest string    `json:"admission_digest"`
	Type            string    `json:"type,omitempty"`
	Recommended     string    `json:"recommended,omitempty"`
	CandidateIDs    []string  `json:"candidate_ids,omitempty"`
	MatchedAt       time.Time `json:"matched_at"`
	Digest          string    `json:"digest"`
}

func (s *Store) CreateAdmission(input AdmissionInput) (Admission, error) {
	release, err := s.acquireStoreLock()
	if err != nil {
		return Admission{}, err
	}
	defer release()

	contract, status, err := normalizeAdmissionInput(input)
	if err != nil {
		return Admission{}, err
	}
	id, err := newOpaqueID("adm_")
	if err != nil {
		return Admission{}, err
	}
	digest, err := admissionContractDigest(contract)
	if err != nil {
		return Admission{}, err
	}
	admission := Admission{
		SchemaVersion: admissionSchemaVersion,
		ID:            id,
		Digest:        digest,
		Status:        status,
		Contract:      contract,
		CreatedAt:     time.Now().UTC(),
	}
	if err := s.saveAdmissionLocked(admission); err != nil {
		return Admission{}, err
	}
	return admission, nil
}

func (s *Store) GetAdmission(ref string) (Admission, error) {
	release, err := s.acquireStoreLock()
	if err != nil {
		return Admission{}, err
	}
	defer release()
	return s.loadAdmissionLocked(strings.TrimSpace(ref))
}

func (s *Store) RecordAdmissionWorkflowMatch(ref string, input WorkflowMatchReceiptInput) (WorkflowMatchReceipt, error) {
	release, err := s.acquireStoreLock()
	if err != nil {
		return WorkflowMatchReceipt{}, err
	}
	defer release()

	admission, err := s.loadAdmissionLocked(strings.TrimSpace(ref))
	if err != nil {
		return WorkflowMatchReceipt{}, err
	}
	if admission.Status != AdmissionPass {
		return WorkflowMatchReceipt{}, fmt.Errorf("admission %s status %s cannot enter workflow matching", admission.ID, admission.Status)
	}
	if existing, err := s.loadAdmissionWorkflowMatchLocked(admission.ID); err == nil {
		return existing, nil
	} else if !os.IsNotExist(err) {
		return WorkflowMatchReceipt{}, err
	}

	candidateIDs := normalizeTexts(input.CandidateIDs)
	receipt := WorkflowMatchReceipt{
		SchemaVersion:   admissionSchemaVersion,
		AdmissionRef:    admission.ID,
		AdmissionDigest: admission.Digest,
		Type:            strings.TrimSpace(input.Type),
		Recommended:     strings.TrimSpace(input.Recommended),
		CandidateIDs:    candidateIDs,
		MatchedAt:       time.Now().UTC(),
	}
	digest, err := workflowMatchReceiptDigest(receipt)
	if err != nil {
		return WorkflowMatchReceipt{}, err
	}
	receipt.Digest = digest

	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return WorkflowMatchReceipt{}, err
	}
	data = append(data, '\n')
	if err := atomicfile.Write(s.admissionMatchPath(admission.ID), data, 0o600); err != nil {
		return WorkflowMatchReceipt{}, err
	}
	return receipt, nil
}

func workflowMatchReceiptDigest(receipt WorkflowMatchReceipt) (string, error) {
	payload := struct {
		AdmissionRef    string    `json:"admission_ref"`
		AdmissionDigest string    `json:"admission_digest"`
		Type            string    `json:"type,omitempty"`
		Recommended     string    `json:"recommended,omitempty"`
		CandidateIDs    []string  `json:"candidate_ids,omitempty"`
		MatchedAt       time.Time `json:"matched_at"`
	}{
		AdmissionRef: receipt.AdmissionRef, AdmissionDigest: receipt.AdmissionDigest,
		Type: receipt.Type, Recommended: receipt.Recommended, CandidateIDs: receipt.CandidateIDs, MatchedAt: receipt.MatchedAt,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func (s *Store) GetAdmissionWorkflowMatch(ref string) (WorkflowMatchReceipt, error) {
	release, err := s.acquireStoreLock()
	if err != nil {
		return WorkflowMatchReceipt{}, err
	}
	defer release()
	return s.loadAdmissionWorkflowMatchLocked(strings.TrimSpace(ref))
}

func normalizeAdmissionInput(input AdmissionInput) (AdmissionContract, AdmissionStatus, error) {
	contract := AdmissionContract{
		Goal:                strings.TrimSpace(input.Goal),
		Deliverable:         strings.TrimSpace(input.Deliverable),
		Audience:            strings.TrimSpace(input.Audience),
		SuccessConditions:   normalizeTexts(input.SuccessConditions),
		FailureConditions:   normalizeTexts(input.FailureConditions),
		Constraints:         normalizeTexts(input.Constraints),
		EvidenceRequirement: strings.ToLower(strings.TrimSpace(input.EvidenceRequirement)),
		MutationAuthority:   strings.ToLower(strings.TrimSpace(input.MutationAuthority)),
		Complexity:          strings.ToLower(strings.TrimSpace(input.Complexity)),
		WorkflowType:        strings.TrimSpace(input.WorkflowType),
		DecisionFrontier:    normalizeTexts(input.DecisionFrontier),
		BlockedReasons:      normalizeTexts(input.BlockedReasons),
		Project:             strings.ToLower(strings.TrimSpace(input.Project)),
		Device:              strings.TrimSpace(input.Device),
	}
	if contract.Goal == "" || contract.Deliverable == "" || contract.Audience == "" {
		return AdmissionContract{}, "", errors.New("admission goal, deliverable, and audience are required")
	}
	if len(contract.SuccessConditions) == 0 || len(contract.FailureConditions) == 0 {
		return AdmissionContract{}, "", errors.New("admission requires success_conditions and failure_conditions")
	}
	if !oneOf(contract.EvidenceRequirement, "none", "lightweight", "research", "first_party", "execution_receipt") {
		return AdmissionContract{}, "", errors.New("invalid admission evidence_requirement")
	}
	if !oneOf(contract.MutationAuthority, "read_only", "local_write", "staging_write", "production_write") {
		return AdmissionContract{}, "", errors.New("invalid admission mutation_authority")
	}
	if !oneOf(contract.Complexity, "trivial", "standard", "substantial", "high_risk") {
		return AdmissionContract{}, "", errors.New("invalid admission complexity")
	}
	if len(contract.Project) > 256 || len(contract.Device) > 256 {
		return AdmissionContract{}, "", errors.New("admission project/device context is too long")
	}
	for label, values := range map[string][]string{
		"success condition": contract.SuccessConditions,
		"failure condition": contract.FailureConditions,
		"constraint":        contract.Constraints,
		"decision frontier": contract.DecisionFrontier,
		"blocked reason":    contract.BlockedReasons,
	} {
		if len(values) > maxTaskConditions {
			return AdmissionContract{}, "", fmt.Errorf("admission %s entries cannot exceed %d", label, maxTaskConditions)
		}
		for _, value := range values {
			if err := validateTextLimit("admission "+label, value, maxTaskConditionBytes); err != nil {
				return AdmissionContract{}, "", err
			}
		}
	}
	for label, value := range map[string]string{
		"goal": contract.Goal, "deliverable": contract.Deliverable, "audience": contract.Audience,
	} {
		if err := validateTextLimit("admission "+label, value, maxTaskGoalBytes); err != nil {
			return AdmissionContract{}, "", err
		}
	}
	status := AdmissionPass
	if len(contract.BlockedReasons) > 0 {
		status = AdmissionBlocked
	} else if len(contract.DecisionFrontier) > 0 {
		status = AdmissionResolve
	}
	return contract, status, nil
}

func admissionContractDigest(contract AdmissionContract) (string, error) {
	data, err := json.Marshal(contract)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func (s *Store) saveAdmissionLocked(admission Admission) error {
	if err := validateAdmissionID(admission.ID); err != nil {
		return err
	}
	if admission.SchemaVersion != admissionSchemaVersion {
		return fmt.Errorf("unsupported admission schema version %d", admission.SchemaVersion)
	}
	expected, err := admissionContractDigest(admission.Contract)
	if err != nil {
		return err
	}
	if admission.Digest != expected {
		return errors.New("admission digest does not match frozen contract")
	}
	path := s.admissionPath(admission.ID)
	if _, err := os.Stat(path); err == nil {
		return errors.New("admission contract is immutable and already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	data, err := json.MarshalIndent(admission, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicfile.Write(path, data, 0o600)
}

func (s *Store) loadAdmissionLocked(ref string) (Admission, error) {
	if err := validateAdmissionID(ref); err != nil {
		return Admission{}, err
	}
	data, err := os.ReadFile(s.admissionPath(ref))
	if err != nil {
		return Admission{}, err
	}
	var admission Admission
	if err := json.Unmarshal(data, &admission); err != nil {
		return Admission{}, fmt.Errorf("decode admission %s: %w", ref, err)
	}
	if admission.SchemaVersion != admissionSchemaVersion {
		return Admission{}, fmt.Errorf("unsupported admission schema version %d", admission.SchemaVersion)
	}
	if admission.ID != ref {
		return Admission{}, errors.New("admission identity mismatch")
	}
	expected, err := admissionContractDigest(admission.Contract)
	if err != nil {
		return Admission{}, err
	}
	if admission.Digest != expected {
		return Admission{}, errors.New("admission digest mismatch")
	}
	return admission, nil
}

func (s *Store) loadAdmissionWorkflowMatchLocked(ref string) (WorkflowMatchReceipt, error) {
	if err := validateAdmissionID(ref); err != nil {
		return WorkflowMatchReceipt{}, err
	}
	data, err := os.ReadFile(s.admissionMatchPath(ref))
	if err != nil {
		return WorkflowMatchReceipt{}, err
	}
	var receipt WorkflowMatchReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return WorkflowMatchReceipt{}, err
	}
	if receipt.AdmissionRef != ref {
		return WorkflowMatchReceipt{}, errors.New("workflow match admission identity mismatch")
	}
	admission, err := s.loadAdmissionLocked(ref)
	if err != nil {
		return WorkflowMatchReceipt{}, err
	}
	if receipt.AdmissionDigest != admission.Digest {
		return WorkflowMatchReceipt{}, errors.New("workflow match admission digest mismatch")
	}
	expected, err := workflowMatchReceiptDigest(receipt)
	if err != nil {
		return WorkflowMatchReceipt{}, err
	}
	if receipt.Digest != expected {
		return WorkflowMatchReceipt{}, errors.New("workflow match receipt digest mismatch")
	}
	return receipt, nil
}

func validateAdmissionID(id string) error {
	if !strings.HasPrefix(id, "adm_") || len(id) != 20 {
		return errors.New("invalid admission ref")
	}
	for _, ch := range id[4:] {
		if !strings.ContainsRune("0123456789abcdef", ch) {
			return errors.New("invalid admission ref")
		}
	}
	return nil
}

func (s *Store) admissionPath(id string) string {
	return filepath.Join(s.root, id+".json")
}

func (s *Store) admissionMatchPath(id string) string {
	return filepath.Join(s.root, id+".match.json")
}

func oneOf(value string, options ...string) bool {
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}
