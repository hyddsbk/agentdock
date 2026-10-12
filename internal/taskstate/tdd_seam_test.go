package taskstate

import (
	"strings"
	"testing"
	"time"
)

func validTDDSeam() *TDDSeam {
	return validTDDSeamForDeclaration(time.Now().Add(-4 * time.Second))
}

func validTDDSeamForDeclaration(declared time.Time) *TDDSeam {
	command := "go test ./internal/taskstate"
	digest := tddCommandSHA256(command)
	run := func(session, ref string, exitCode int, started time.Time) *TDDRun {
		return &TDDRun{
			Command: command, CommandSHA256: digest, ExitCode: exitCode, Status: "exited", EvidenceRef: ref,
			Executor: tddExecutorIssuer, SessionID: session, StartedAt: started, FinishedAt: started.Add(100 * time.Millisecond),
			StdoutSHA256: strings.Repeat("a", 64), StderrSHA256: strings.Repeat("b", 64), ReceiptSHA256: strings.Repeat("c", 64),
		}
	}
	return &TDDSeam{
		TestCommand: command, DeclaredAt: declared,
		Red:        run("session-111111111111111111111111", "exec://session-111111111111111111111111", 1, declared.Add(time.Second)),
		Green:      run("session-222222222222222222222222", "exec://session-222222222222222222222222", 0, declared.Add(2*time.Second)),
		Regression: run("session-333333333333333333333333", "exec://session-333333333333333333333333", 0, declared.Add(3*time.Second)),
	}
}

func TestTDDSeamFailClosed(t *testing.T) {
	if err := validTDDSeam().ValidateCompletion(); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		mutate  func(*TDDSeam)
		message string
	}{
		{"missing red", func(s *TDDSeam) { s.Red = nil }, "missing red"},
		{"red passed", func(s *TDDSeam) { s.Red.ExitCode = 0 }, "red must fail"},
		{"green failed", func(s *TDDSeam) { s.Green.ExitCode = 1 }, "green must pass"},
		{"regression missing", func(s *TDDSeam) { s.Regression = nil }, "missing regression"},
		{"command drift", func(s *TDDSeam) { s.Regression.Command = "go test ./..." }, "command differs"},
		{"missing receipt", func(s *TDDSeam) { s.Green.EvidenceRef = "" }, "evidence_ref"},
		{"reused receipt", func(s *TDDSeam) { s.Green.EvidenceRef = s.Red.EvidenceRef }, "distinct evidence"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := validTDDSeam()
			tc.mutate(s)
			err := s.ValidateCompletion()
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("error=%v, expected %q", err, tc.message)
			}
		})
	}
}
