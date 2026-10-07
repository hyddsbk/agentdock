package browser

import (
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"testing"
)

func TestNavigationGuardRedirectPolicy(t *testing.T) {
	cases := []struct {
		name     string
		from     string
		status   int64
		location string
		blocked  bool
	}{
		{"relative allowed", "https://example.com/path", 302, "/next", false},
		{"https allowed", "http://example.com/", 301, "https://safe.example/", false},
		{"scheme relative allowed", "https://example.com/", 308, "//other.example/p", false},
		{"javascript denied", "https://example.com/", 302, "javascript:alert(1)", true},
		{"file denied", "https://example.com/", 302, "file:///etc/passwd", true},
		{"data denied", "https://example.com/", 307, "data:text/html,hi", true},
		{"chrome denied", "https://example.com/", 301, "chrome://settings", true},
		{"credentials denied", "https://example.com/", 303, "https://user:pwd@evil.example/", true},
		{"malformed port denied", "https://example.com/", 302, "https://example.com:abc/", true},
		{"leading spaces denied", "https://example.com/", 302, " javascript:alert(1)", true},
		{"backslash denied", "https://example.com/", 302, "https:\\evil.example/", true},
		{"plain 200 ignores location", "https://example.com/", 200, "file:///etc/passwd", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := &fetch.EventRequestPaused{
				Request:            &network.Request{URL: tc.from},
				ResponseStatusCode: tc.status,
				ResponseHeaders:    []*fetch.HeaderEntry{{Name: "Location", Value: tc.location}},
			}
			if got := redirectViolatesPolicy(ev); got != tc.blocked {
				t.Fatalf("redirectViolatesPolicy()=%v, want %v", got, tc.blocked)
			}
		})
	}
	duplicate := &fetch.EventRequestPaused{
		Request:            &network.Request{URL: "https://example.com"},
		ResponseStatusCode: 302,
		ResponseHeaders: []*fetch.HeaderEntry{
			{Name: "Location", Value: "/safe"}, {Name: "location", Value: "/other"},
		},
	}
	if !redirectViolatesPolicy(duplicate) {
		t.Fatal("duplicate location accepted")
	}
	if !redirectViolatesPolicy(&fetch.EventRequestPaused{Request: &network.Request{URL: "file:///bad"}, ResponseStatusCode: 200}) {
		t.Fatal("unsafe document response URL accepted")
	}
	if redirectViolatesPolicy(&fetch.EventRequestPaused{Request: &network.Request{URL: "https://example.com"}, ResponseStatusCode: 200}) {
		t.Fatal("normal response denied")
	}
}

func TestNavigationGuardViolationState(t *testing.T) {
	guard := &navigationGuard{}
	if guard.takeDenied() {
		t.Fatal("initial state must be clean")
	}
	guard.markDenied()
	if !guard.takeDenied() {
		t.Fatal("violation missing")
	}
	if guard.takeDenied() {
		t.Fatal("violation must be consumed")
	}
}
