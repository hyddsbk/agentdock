package browser

import (
	"context"
	"testing"
)

func TestBrowserSessionAcceptsAdvertisedTimeoutForCloseAndCleanup(t *testing.T) {
	service := New(Config{AgentDockHome: t.TempDir()}, nil)
	t.Cleanup(func() { _ = service.Close() })

	closed, err := service.HandleSession(context.Background(), map[string]any{
		"action": "close", "session_id": "missing", "timeout_ms": 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if closed["code"] != ErrSessionNotFound {
		t.Fatalf("close result = %#v", closed)
	}

	cleaned, err := service.HandleSession(context.Background(), map[string]any{
		"action": "cleanup_stale", "max_age_ms": 1000, "timeout_ms": 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cleaned["browser_ok"] != true {
		t.Fatalf("cleanup result = %#v", cleaned)
	}
}

func TestBrowserActFailureIncludesNormalizedActionResult(t *testing.T) {
	service := New(Config{AgentDockHome: t.TempDir()}, nil)
	t.Cleanup(func() { _ = service.Close() })

	result, err := service.HandleAct(context.Background(), map[string]any{
		"session_id": "missing",
		"actions": []any{map[string]any{
			"action": "click", "selector": "#save",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["browser_ok"] != false || result["code"] != ErrSessionNotFound {
		t.Fatalf("failure result = %#v", result)
	}
	actionResult, ok := result["action_result"].(map[string]any)
	if !ok {
		t.Fatalf("action_result = %#v", result["action_result"])
	}
	if actionResult["ok"] != false || actionResult["status"] != "failed" || actionResult["action_count"] != float64(1) {
		t.Fatalf("normalized action_result = %#v", actionResult)
	}
}
