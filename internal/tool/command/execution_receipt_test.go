package command

import (
	"context"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/execevidence"
)

func TestExecIssuesDurableExecutionReceipt(t *testing.T) {
	service, _ := newCommandTestService(t)
	receipts, err := execevidence.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service.receipts = receipts

	result, err := service.execArgs(context.Background(), map[string]any{
		"cmd":            "printf receipt-test",
		"execution_mode": "sync",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["command_ok"] != true {
		t.Fatalf("command result = %#v", result)
	}
	raw, ok := result["execution_receipt"].(map[string]any)
	if !ok {
		t.Fatalf("execution receipt missing: %#v", result)
	}
	ref, _ := raw["ref"].(string)
	if !strings.HasPrefix(ref, "exec://session-") {
		t.Fatalf("receipt ref = %q", ref)
	}
	receipt, err := receipts.Resolve(ref)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Command != "printf receipt-test" || receipt.ExitCode != 0 || receipt.Status != "exited" {
		t.Fatalf("unexpected receipt: %#v", receipt)
	}
	if receipt.StdoutSHA256 == "" || receipt.ReceiptSHA256 == "" || receipt.StartedAt.IsZero() || receipt.FinishedAt.IsZero() {
		t.Fatalf("incomplete receipt: %#v", receipt)
	}
}

func TestKilledSessionReceiptCannotLookLikeNormalExit(t *testing.T) {
	service, _ := newCommandTestService(t)
	receipts, err := execevidence.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service.receipts = receipts

	started, err := service.execArgs(context.Background(), map[string]any{
		"cmd":            "sleep 30",
		"execution_mode": "async",
	})
	if err != nil {
		t.Fatal(err)
	}
	sessionID, _ := started["session_id"].(string)
	result, err := service.killSessionArgs(map[string]any{"session_id": sessionID})
	if err != nil {
		t.Fatal(err)
	}
	raw, ok := result["execution_receipt"].(map[string]any)
	if !ok {
		t.Fatalf("killed execution receipt missing: %#v", result)
	}
	receipt, err := receipts.Resolve(raw["ref"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status == "exited" {
		t.Fatalf("killed session attested as normal exit: %#v", receipt)
	}
}
