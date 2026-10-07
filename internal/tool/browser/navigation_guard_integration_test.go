//go:build browser_integration

package browser

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNavigationGuardRealChromiumClickAndRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<title>Navigation policy QA</title><a id="bad" href="javascript:alert('blocked')">bad</a><a id="bad-file" href="file:///etc/passwd">bad file</a><a id="safe" href="/safe">safe</a><a id="redirect" href="/bad-redirect">redirect</a>`)
		case "/bad-redirect":
			w.Header().Set("Location", "file:///etc/passwd")
			w.WriteHeader(http.StatusFound)
		case "/safe":
			fmt.Fprint(w, "<title>Safe</title><div>safe route</div>")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	service := newIntegrationService(t)
	started, startErr := service.start(context.Background(), StartRequest{Browser: BrowserAuto, Headless: true, URL: server.URL, Timeout: 16 * time.Second})
	if startErr != nil {
		t.Fatalf("start with response guard: %v", startErr)
	}
	for _, selector := range []string{"#bad", "#bad-file"} {
		_, err := service.act(context.Background(), ActRequest{
			SessionID: started.SessionID, Timeout: 8 * time.Second,
			Actions: []Action{{Kind: "click", Click: &ClickAction{Selector: selector}}},
		})
		assertDeniedNavigation(t, err)
		snap, snapErr := service.snapshot(context.Background(), SnapshotRequest{SessionID: started.SessionID, Timeout: 8 * time.Second})
		if snapErr != nil || snap.URL != server.URL+"/" {
			t.Fatalf("rejected click navigated: url=%q err=%v", snap.URL, snapErr)
		}
	}

	_, err := service.act(context.Background(), ActRequest{
		SessionID: started.SessionID, Timeout: 8 * time.Second,
		Actions: []Action{
			{Kind: "click", Click: &ClickAction{Selector: "#redirect"}},
			{Kind: "wait", Wait: &WaitAction{Duration: 300 * time.Millisecond}},
		},
	})
	assertDeniedNavigation(t, err)

	_, err = service.act(context.Background(), ActRequest{
		SessionID: started.SessionID, Timeout: 8 * time.Second,
		Actions: []Action{{Kind: "goto", Goto: &GotoAction{URL: server.URL + "/bad-redirect", WaitUntil: WaitLoad, Timeout: 6 * time.Second}}},
	})
	assertDeniedNavigation(t, err)

	safe, err := service.act(context.Background(), ActRequest{
		SessionID: started.SessionID, Timeout: 10 * time.Second,
		Actions: []Action{{Kind: "goto", Goto: &GotoAction{URL: server.URL + "/safe", WaitUntil: WaitLoad, Timeout: 8 * time.Second}}},
	})
	if err != nil || safe.URL != server.URL+"/safe" {
		t.Fatalf("safe navigation after denial failed url=%q err=%v", safe.URL, err)
	}
}

func TestNavigationGuardRealChromiumInitialBadRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "data:text/html,blocked")
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	service := newIntegrationService(t)
	_, err := service.start(context.Background(), StartRequest{
		Browser: BrowserAuto, Headless: true, URL: server.URL, Timeout: 12 * time.Second,
	})
	assertDeniedNavigation(t, err)
}

func assertDeniedNavigation(t *testing.T, err error) {
	t.Helper()
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != ErrPolicyDenied {
		t.Fatalf("expected %s, got %v", ErrPolicyDenied, err)
	}
}
