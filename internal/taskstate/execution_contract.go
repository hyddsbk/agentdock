package taskstate

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	maxExecutionChecks           = 8
	maxExecutionCheckIDRunes     = 64
	maxExecutionCommandBytes     = 16 << 10
	maxAllowedExecutionExitCodes = 8
	executionExecutorIssuer      = "agentdock.exec_command/v1"
)

type ExecutionCheckSpec struct {
	ID               string `json:"id"`
	Command          string `json:"command"`
	AllowedExitCodes []int  `json:"allowed_exit_codes,omitempty"`
}

type ExecutionContractSpec struct {
	Ordered bool                 `json:"ordered,omitempty"`
	Checks  []ExecutionCheckSpec `json:"checks"`
}

type ExecutionCheck struct {
	ID               string `json:"id"`
	Command          string `json:"command"`
	CommandSHA256    string `json:"command_sha256"`
	AllowedExitCodes []int  `json:"allowed_exit_codes"`
}

type ExecutionContract struct {
	DeclaredAt time.Time        `json:"declared_at"`
	Ordered    bool             `json:"ordered,omitempty"`
	Checks     []ExecutionCheck `json:"checks"`
}

type ExecutionRun struct {
	CheckID       string    `json:"check_id,omitempty"`
	Command       string    `json:"command"`
	CommandSHA256 string    `json:"command_sha256"`
	ExitCode      int       `json:"exit_code"`
	Status        string    `json:"status"`
	EvidenceRef   string    `json:"evidence_ref"`
	Executor      string    `json:"executor"`
	SessionID     string    `json:"session_id"`
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at"`
	Runtime       string    `json:"runtime,omitempty"`
	Workdir       string    `json:"workdir,omitempty"`
	TimedOut      bool      `json:"timed_out,omitempty"`
	StdoutSHA256  string    `json:"stdout_sha256"`
	StderrSHA256  string    `json:"stderr_sha256"`
	ReceiptSHA256 string    `json:"receipt_sha256"`
}

func normalizeExecutionContract(stepID string, spec *ExecutionContractSpec, now time.Time) (*ExecutionContract, error) {
	if spec == nil {
		return nil, nil
	}
	if len(spec.Checks) == 0 {
		return nil, fmt.Errorf("step %s execution contract requires at least one check", stepID)
	}
	if len(spec.Checks) > maxExecutionChecks {
		return nil, fmt.Errorf("step %s execution contract checks cannot exceed %d", stepID, maxExecutionChecks)
	}
	seen := make(map[string]struct{}, len(spec.Checks))
	checks := make([]ExecutionCheck, 0, len(spec.Checks))
	for _, raw := range spec.Checks {
		id := strings.TrimSpace(raw.ID)
		if id == "" || id != raw.ID || len([]rune(id)) > maxExecutionCheckIDRunes || !validStepID(id) {
			return nil, fmt.Errorf("step %s has invalid execution check id %q", stepID, raw.ID)
		}
		if _, exists := seen[id]; exists {
			return nil, fmt.Errorf("step %s has duplicate execution check id %q", stepID, id)
		}
		seen[id] = struct{}{}
		command := strings.TrimSpace(raw.Command)
		if command == "" || command != raw.Command || len([]byte(command)) > maxExecutionCommandBytes {
			return nil, fmt.Errorf("step %s execution check %s has invalid command", stepID, id)
		}
		exitCodes, err := normalizeAllowedExitCodes(raw.AllowedExitCodes)
		if err != nil {
			return nil, fmt.Errorf("step %s execution check %s: %w", stepID, id, err)
		}
		checks = append(checks, ExecutionCheck{
			ID: id, Command: command, CommandSHA256: executionCommandSHA256(command), AllowedExitCodes: exitCodes,
		})
	}
	return &ExecutionContract{DeclaredAt: now, Ordered: spec.Ordered, Checks: checks}, nil
}

func normalizeAllowedExitCodes(values []int) ([]int, error) {
	if len(values) == 0 {
		return []int{0}, nil
	}
	if len(values) > maxAllowedExecutionExitCodes {
		return nil, fmt.Errorf("allowed exit codes cannot exceed %d", maxAllowedExecutionExitCodes)
	}
	seen := make(map[int]struct{}, len(values))
	out := make([]int, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			return nil, fmt.Errorf("duplicate allowed exit code %d", value)
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out, nil
}

func (contract *ExecutionContract) ValidateCompletion(evidence *ExecutionEvidence) error {
	if contract == nil {
		return nil
	}
	if contract.DeclaredAt.IsZero() {
		return errors.New("execution contract requires declared_at")
	}
	if len(contract.Checks) == 0 || len(contract.Checks) > maxExecutionChecks {
		return errors.New("execution contract checks are invalid")
	}
	if evidence == nil || len(evidence.Attestations) == 0 {
		return errors.New("execution contract requires executor attestations")
	}
	if len(evidence.Attestations) != len(contract.Checks) {
		return fmt.Errorf("execution contract requires %d attestations, got %d", len(contract.Checks), len(evidence.Attestations))
	}

	refSet := make(map[string]struct{}, len(evidence.EvidenceRefs))
	for _, ref := range evidence.EvidenceRefs {
		refSet[ref] = struct{}{}
	}
	runByCheck := make(map[string]*ExecutionRun, len(evidence.Attestations))
	receiptRefs := make(map[string]struct{}, len(evidence.Attestations))
	for _, run := range evidence.Attestations {
		if run == nil {
			return errors.New("execution contract contains nil attestation")
		}
		if _, exists := runByCheck[run.CheckID]; exists {
			return fmt.Errorf("execution contract has duplicate attestation for check %s", run.CheckID)
		}
		runByCheck[run.CheckID] = run
		if _, exists := receiptRefs[run.EvidenceRef]; exists {
			return fmt.Errorf("execution receipt %s is reused within step", run.EvidenceRef)
		}
		receiptRefs[run.EvidenceRef] = struct{}{}
		if _, exists := refSet[run.EvidenceRef]; !exists {
			return fmt.Errorf("execution attestation %s is missing from evidence_refs", run.CheckID)
		}
	}

	var previous *ExecutionRun
	for _, check := range contract.Checks {
		run := runByCheck[check.ID]
		if run == nil {
			return fmt.Errorf("execution contract missing attestation for check %s", check.ID)
		}
		if err := validateExecutionRun(check, contract.DeclaredAt, run); err != nil {
			return err
		}
		if contract.Ordered && previous != nil && !previous.FinishedAt.Before(run.StartedAt) {
			return fmt.Errorf("execution contract requires check %s to finish before %s starts", previous.CheckID, run.CheckID)
		}
		previous = run
	}
	return nil
}

func validateExecutionRun(check ExecutionCheck, declaredAt time.Time, run *ExecutionRun) error {
	if strings.TrimSpace(run.CheckID) != check.ID {
		return fmt.Errorf("execution attestation check id mismatch for %s", check.ID)
	}
	if strings.TrimSpace(run.Command) != check.Command {
		return fmt.Errorf("execution check %s command differs from declaration", check.ID)
	}
	if strings.ToLower(strings.TrimSpace(run.CommandSHA256)) != check.CommandSHA256 || run.CommandSHA256 != executionCommandSHA256(run.Command) {
		return fmt.Errorf("execution check %s command digest mismatch", check.ID)
	}
	if !strings.HasPrefix(strings.TrimSpace(run.EvidenceRef), "exec://") || len(run.EvidenceRef) > 2048 {
		return fmt.Errorf("execution check %s requires executor evidence_ref", check.ID)
	}
	if strings.TrimSpace(run.Executor) != executionExecutorIssuer {
		return fmt.Errorf("execution check %s requires trusted executor", check.ID)
	}
	if strings.TrimSpace(run.SessionID) == "" || run.EvidenceRef != "exec://"+run.SessionID {
		return fmt.Errorf("execution check %s session identity mismatch", check.ID)
	}
	if run.StartedAt.IsZero() || run.FinishedAt.IsZero() || run.FinishedAt.Before(run.StartedAt) {
		return fmt.Errorf("execution check %s timestamps are invalid", check.ID)
	}
	if run.StartedAt.Before(declaredAt) {
		return fmt.Errorf("execution check %s receipt predates declaration", check.ID)
	}
	if strings.ToLower(strings.TrimSpace(run.Status)) != "exited" {
		return fmt.Errorf("execution check %s did not exit normally", check.ID)
	}
	if run.TimedOut {
		return fmt.Errorf("execution check %s timed out", check.ID)
	}
	for label, digest := range map[string]string{
		"stdout": run.StdoutSHA256, "stderr": run.StderrSHA256, "receipt": run.ReceiptSHA256,
	} {
		if !validExecutionDigest(digest) {
			return fmt.Errorf("execution check %s %s digest is invalid", check.ID, label)
		}
	}
	if !containsExitCode(check.AllowedExitCodes, run.ExitCode) {
		return fmt.Errorf("execution check %s exit code %d is not allowed", check.ID, run.ExitCode)
	}
	return nil
}

func validateExecutionRunIntegrity(run *ExecutionRun) error {
	if run == nil {
		return errors.New("execution attestation is nil")
	}
	check := ExecutionCheck{
		ID: run.CheckID, Command: strings.TrimSpace(run.Command),
		CommandSHA256: executionCommandSHA256(run.Command), AllowedExitCodes: []int{run.ExitCode},
	}
	return validateExecutionRun(check, run.StartedAt, run)
}

func containsExitCode(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func executionCommandSHA256(command string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(command)))
	return hex.EncodeToString(sum[:])
}

func validExecutionDigest(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
