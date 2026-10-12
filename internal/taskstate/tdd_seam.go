package taskstate

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const tddExecutorIssuer = "agentdock.exec_command/v1"

// TDDSeam is an opt-in, auditable test-first boundary attached to a Task step.
// An absent seam preserves legacy Task behavior. A declared seam is fail-closed.
type TDDSeam struct {
	TestCommand string    `json:"test_command"`
	DeclaredAt  time.Time `json:"declared_at"`
	Red         *TDDRun   `json:"red,omitempty"`
	Green       *TDDRun   `json:"green,omitempty"`
	Regression  *TDDRun   `json:"regression,omitempty"`
}

type TDDRun struct {
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

func (seam *TDDSeam) ValidateCompletion() error {
	if seam == nil {
		return nil
	}
	command := strings.TrimSpace(seam.TestCommand)
	if command == "" {
		return errors.New("tdd seam requires test_command")
	}
	if seam.DeclaredAt.IsZero() {
		return errors.New("tdd seam requires declared_at")
	}
	commandDigest := tddCommandSHA256(command)
	for _, phase := range []struct {
		name            string
		run             *TDDRun
		expectedFailure bool
	}{
		{"red", seam.Red, true}, {"green", seam.Green, false}, {"regression", seam.Regression, false},
	} {
		if phase.run == nil {
			return fmt.Errorf("tdd seam missing %s evidence", phase.name)
		}
		if strings.TrimSpace(phase.run.Command) != command {
			return fmt.Errorf("tdd seam %s command differs from test_command", phase.name)
		}
		if strings.ToLower(strings.TrimSpace(phase.run.CommandSHA256)) != commandDigest {
			return fmt.Errorf("tdd seam %s command digest mismatch", phase.name)
		}
		if !strings.HasPrefix(strings.TrimSpace(phase.run.EvidenceRef), "exec://") || len(phase.run.EvidenceRef) > 2048 {
			return fmt.Errorf("tdd seam %s requires executor evidence_ref", phase.name)
		}
		if strings.TrimSpace(phase.run.Executor) != tddExecutorIssuer {
			return fmt.Errorf("tdd seam %s requires trusted executor", phase.name)
		}
		if strings.TrimSpace(phase.run.SessionID) == "" {
			return fmt.Errorf("tdd seam %s requires session_id", phase.name)
		}
		if phase.run.StartedAt.IsZero() || phase.run.FinishedAt.IsZero() || phase.run.FinishedAt.Before(phase.run.StartedAt) {
			return fmt.Errorf("tdd seam %s timestamps are invalid", phase.name)
		}
		if phase.run.StartedAt.Before(seam.DeclaredAt) {
			return fmt.Errorf("tdd seam %s receipt predates declaration", phase.name)
		}
		if strings.ToLower(strings.TrimSpace(phase.run.Status)) != "exited" {
			return fmt.Errorf("tdd seam %s requires normally exited test execution", phase.name)
		}
		if phase.run.TimedOut {
			return fmt.Errorf("tdd seam %s timed out", phase.name)
		}
		for label, digest := range map[string]string{
			"stdout":  phase.run.StdoutSHA256,
			"stderr":  phase.run.StderrSHA256,
			"receipt": phase.run.ReceiptSHA256,
		} {
			if !validTDDDigest(digest) {
				return fmt.Errorf("tdd seam %s %s digest is invalid", phase.name, label)
			}
		}
		if phase.expectedFailure && phase.run.ExitCode == 0 {
			return errors.New("tdd seam red must fail")
		}
		if !phase.expectedFailure && phase.run.ExitCode != 0 {
			return fmt.Errorf("tdd seam %s must pass", phase.name)
		}
	}
	if seam.Red.EvidenceRef == seam.Green.EvidenceRef || seam.Red.EvidenceRef == seam.Regression.EvidenceRef || seam.Green.EvidenceRef == seam.Regression.EvidenceRef {
		return errors.New("tdd seam phases require distinct evidence refs")
	}
	if !seam.Red.FinishedAt.Before(seam.Green.StartedAt) {
		return errors.New("tdd seam requires red before green")
	}
	if !seam.Green.FinishedAt.Before(seam.Regression.StartedAt) {
		return errors.New("tdd seam requires green before regression")
	}
	return nil
}

func tddCommandSHA256(command string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(command)))
	return hex.EncodeToString(sum[:])
}

func validTDDDigest(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
