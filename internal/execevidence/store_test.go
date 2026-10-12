package execevidence

import (
	"strings"
	"testing"
	"time"
)

func sampleReceipt(command, session string, exitCode int, started, finished time.Time) Receipt {
	return Receipt{
		Issuer:       ExecutorIssuer,
		SessionID:    session,
		Command:      command,
		ExitCode:     exitCode,
		Status:       "exited",
		StartedAt:    started,
		FinishedAt:   finished,
		Runtime:      "windows",
		Workdir:      `C:\\repo`,
		StdoutSHA256: strings.Repeat("a", 64),
		StderrSHA256: strings.Repeat("b", 64),
	}
}

func TestStoreIssueResolveAndClaim(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().Add(-time.Second)
	receipt := sampleReceipt("go test ./internal/taskstate", "session-aaaaaaaaaaaaaaaaaaaaaaaa", 1, started, started.Add(250*time.Millisecond))
	ref, err := store.Issue(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if ref != "exec://session-aaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("ref = %q", ref)
	}
	loaded, err := store.Resolve(ref)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Command != receipt.Command || loaded.ExitCode != 1 || loaded.CommandSHA256 == "" {
		t.Fatalf("unexpected receipt: %#v", loaded)
	}
	if err := store.WithClaims([]string{ref}, "task:t1:step:s1", func(receipts []Receipt) error {
		if len(receipts) != 1 || receipts[0].SessionID != receipt.SessionID {
			t.Fatalf("unexpected claimed receipts: %#v", receipts)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.WithClaims([]string{ref}, "task:t2:step:s2", func([]Receipt) error { return nil }); err == nil || !strings.Contains(err.Error(), "already claimed") {
		t.Fatalf("reuse error = %v", err)
	}
}

func TestStoreClaimsRollbackWhenCallbackFails(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().Add(-time.Second)
	ref, err := store.Issue(sampleReceipt("go test ./...", "session-bbbbbbbbbbbbbbbbbbbbbbbb", 0, started, started.Add(100*time.Millisecond)))
	if err != nil {
		t.Fatal(err)
	}
	wantErr := "checkpoint rejected"
	if err := store.WithClaims([]string{ref}, "task:t1:step:s1", func([]Receipt) error { return errString(wantErr) }); err == nil || err.Error() != wantErr {
		t.Fatalf("callback error = %v", err)
	}
	if err := store.WithClaims([]string{ref}, "task:t2:step:s2", func([]Receipt) error { return nil }); err != nil {
		t.Fatalf("claim was not rolled back: %v", err)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
