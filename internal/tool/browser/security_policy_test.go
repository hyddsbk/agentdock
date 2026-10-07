package browser

import (
	"context"
	"errors"
	"testing"
)

func TestSecurityPolicyURLAllowlist(t *testing.T) {
	allowed := []string{
		"about:blank", "https://example.com", "https://example.com/path?q=1#top",
		"http://127.0.0.1:18766/", "http://[::1]:8765/healthz",
	}
	for _, u := range allowed {
		if err := validateNavigationURL(u, true); err != nil {
			t.Errorf("allowed %q: %v", u, err)
		}
	}
	blocked := []string{
		"", "about:blank#x", "about:srcdoc", "data:text/html,Hello",
		"javascript:alert(1)", "file:///C:/secrets", "chrome://settings",
		"ftp://example.com/a", "//example.com/path", "/relative",
		"https://user:secret@example.com", "https://example.com\\@evil.com",
		"https://example.com/\nmalicious", "https://example.com:badport/",
		" https://example.com", "https://example.com ",
	}
	for _, u := range blocked {
		if err := validateNavigationURL(u, true); err == nil {
			t.Errorf("dangerous URL permitted: %q", u)
		}
	}
	if err := validateNavigationURL("about:blank", false); err == nil {
		t.Fatal("about:blank is not a cookie/storage origin")
	}
}

func TestSecurityPolicyPreflightRejectsBeforeActions(t *testing.T) {
	s := New(Config{AgentDockHome: t.TempDir()}, nil)
	defer s.Close()
	_, err := s.start(context.Background(), StartRequest{
		URL: "file:///private.txt",
	})
	expectSecurityError(t, err)
	_, err = s.start(context.Background(), StartRequest{
		URL:          "about:blank",
		LocalStorage: map[string]map[string]string{"file:///C:/": {"key": "value"}},
	})
	expectSecurityError(t, err)
	_, err = s.start(context.Background(), StartRequest{
		URL:          "about:blank",
		LocalStorage: map[string]map[string]string{"https://example.com/path": {"key": "value"}},
	})
	expectSecurityError(t, err)
	_, err = s.start(context.Background(), StartRequest{
		URL:     "about:blank",
		Cookies: []Cookie{{Name: "a", Value: "b", URL: "javascript:alert(1)"}},
	})
	expectSecurityError(t, err)
	_, err = s.act(context.Background(), ActRequest{Actions: []Action{
		{Kind: "click", Click: &ClickAction{Selector: "#noop"}},
		{Kind: "goto", Goto: &GotoAction{URL: "data:text/html,evil"}},
	}})
	expectSecurityError(t, err)
	if err := validateActSecurity([]Action{{Kind: "goto", Goto: &GotoAction{URL: "https://example.com"}}}); err != nil {
		t.Fatalf("safe navigation incorrectly blocked: %v", err)
	}
	if err := validateStorageOrigin("http://127.0.0.1:18766/"); err != nil {
		t.Fatalf("loopback storage origin rejected: %v", err)
	}
}

func expectSecurityError(t *testing.T, err error) {
	t.Helper()
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != ErrPolicyDenied {
		t.Fatalf("expected security policy error, got %v", err)
	}
}
