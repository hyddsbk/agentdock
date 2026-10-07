//go:build browser_integration

package browser

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBrowserSecurityPolicyRealChromiumDeniedThenRecovery(t *testing.T) {
	server := integrationServer(t)
	defer server.Close()
	service := newIntegrationService(t)
	started := startIntegrationSession(t, service, server.URL, true)

	_, err := service.act(context.Background(), ActRequest{
		SessionID: started.SessionID, Timeout: 5 * time.Second,
		Actions: []Action{
			{Kind: "click", Click: &ClickAction{Selector: "#noop"}},
			{Kind: "goto", Goto: &GotoAction{URL: "file:///C:/secrets"}},
		},
	})
	var e *Error
	if !errors.As(err, &e) || e.Code != ErrPolicyDenied {
		t.Fatalf("unsafe navigation was not denied: %v", err)
	}
	snapshot, err := service.snapshot(context.Background(), SnapshotRequest{SessionID: started.SessionID, Timeout: 5 * time.Second})
	if err != nil || snapshot.URL != server.URL+"/" {
		t.Fatalf("rejected batch must not navigate or close session: url=%q err=%v", snapshot.URL, err)
	}
	safe, err := service.act(context.Background(), ActRequest{
		SessionID: started.SessionID, Timeout: 5 * time.Second,
		Actions: []Action{{Kind: "goto", Goto: &GotoAction{URL: server.URL + "/loaded", WaitUntil: WaitLoad}}},
	})
	if err != nil || safe.ActionResult == nil || !safe.ActionResult.StateChanged {
		t.Fatalf("safe navigation after rejection failed: %v result=%#v", err, safe.ActionResult)
	}
}
