package interop

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDiscoverySearchIsBoundedReadOnlyCandidate(t *testing.T) {
	var gotQuery, gotOwner, gotLimit string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s", r.Method)
		}
		gotQuery = r.URL.Query().Get("q")
		gotOwner = r.URL.Query().Get("owner")
		gotLimit = r.URL.Query().Get("limit")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"skills": []map[string]any{
				{"id": "vercel-labs/agent-skills/web-design-guidelines", "name": "web-design-guidelines", "installs": 1234, "source": "vercel-labs/agent-skills"},
				{"id": "bad/control", "name": "bad\nname", "installs": -5, "source": "https://example.com/not-github"},
			},
		})
	}))
	defer server.Close()

	client := NewDiscoveryClient(server.Client())
	client.searchEndpoint = server.URL + "/api/search"

	items, err := client.Search(context.Background(), "design", "Vercel-Labs", 2)
	if err != nil {
		t.Fatal(err)
	}
	if gotQuery != "design" || gotOwner != "vercel-labs" || gotLimit != "2" {
		t.Fatalf("unexpected query params q=%q owner=%q limit=%q", gotQuery, gotOwner, gotLimit)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d", len(items))
	}
	if !items[0].Pinnable || items[0].InstallAuthorized {
		t.Fatalf("first candidate policy = %#v", items[0])
	}
	if items[0].CandidateSHA256 == "" || len(items[0].CandidateSHA256) != 64 {
		t.Fatalf("candidate digest = %q", items[0].CandidateSHA256)
	}
	if items[1].Pinnable || items[1].InstallAuthorized || items[1].Installs != 0 {
		t.Fatalf("second candidate policy = %#v", items[1])
	}
	if strings.ContainsAny(items[1].Name, "\r\n") {
		t.Fatalf("control characters were not removed: %q", items[1].Name)
	}
}

func TestDiscoverySearchRejectsInvalidBounds(t *testing.T) {
	client := NewDiscoveryClient(http.DefaultClient)
	for _, tc := range []struct {
		query string
		owner string
		limit int
	}{
		{"x", "", 10},
		{"valid", "bad/owner", 10},
		{"valid", "", 21},
	} {
		if _, err := client.Search(context.Background(), tc.query, tc.owner, tc.limit); err == nil {
			t.Fatalf("Search(%q,%q,%d) unexpectedly succeeded", tc.query, tc.owner, tc.limit)
		}
	}
}

func TestDiscoveryPinProducesImmutableProvenanceLock(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef01234567"
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{"sha": sha})
	}))
	defer server.Close()

	client := NewDiscoveryClient(server.Client())
	client.githubAPIBase = server.URL

	result, err := client.Pin(context.Background(), "https://github.com/vercel-labs/agent-skills.git", "web-design-guidelines")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/repos/vercel-labs/agent-skills/commits/HEAD" {
		t.Fatalf("GitHub path = %q", gotPath)
	}
	if result.Lock.Schema != DiscoveryLockSchema || result.Lock.Provider != "github" {
		t.Fatalf("lock metadata = %#v", result.Lock)
	}
	if !result.Lock.Source.Immutable || result.Lock.Source.Commit != sha {
		t.Fatalf("source ref = %#v", result.Lock.Source)
	}
	if result.Lock.InstallAuthorized || !result.Lock.ReviewRequired || result.Lock.NextOwner != "project-due-diligence" {
		t.Fatalf("lock policy = %#v", result.Lock)
	}
	if len(result.LockSHA256) != 64 {
		t.Fatalf("lock digest = %q", result.LockSHA256)
	}

	second, err := client.Pin(context.Background(), "vercel-labs/agent-skills", "web-design-guidelines")
	if err != nil {
		t.Fatal(err)
	}
	if second.LockSHA256 != result.LockSHA256 {
		t.Fatalf("lock digest is not deterministic: %q != %q", second.LockSHA256, result.LockSHA256)
	}
}

func TestDiscoveryPinRejectsMutableOrNonPublicGitHubSelector(t *testing.T) {
	client := NewDiscoveryClient(http.DefaultClient)
	for _, source := range []string{
		"",
		"github:vercel-labs/skills@main",
		"https://github.com/vercel-labs/skills/tree/main",
		"http://github.com/vercel-labs/skills",
		"https://gitlab.com/vercel-labs/skills",
		"owner/repo/extra",
	} {
		if _, err := client.Pin(context.Background(), source, "skill"); err == nil {
			t.Fatalf("Pin(%q) unexpectedly succeeded", source)
		}
	}
}

func TestDiscoveryPinRejectsNonCommitResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"sha": "main"})
	}))
	defer server.Close()
	client := NewDiscoveryClient(server.Client())
	client.githubAPIBase = server.URL
	if _, err := client.Pin(context.Background(), "vercel-labs/skills", "find-skills"); err == nil {
		t.Fatal("Pin accepted non-commit response")
	}
}

func TestDiscoveryResponseSizeLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"skills":[{"id":"x","name":"xx","source":"o/r","padding":"`))
		_, _ = w.Write([]byte(strings.Repeat("a", maxDiscoveryBody)))
		_, _ = w.Write([]byte(`"}]}`))
	}))
	defer server.Close()
	client := NewDiscoveryClient(server.Client())
	client.searchEndpoint = server.URL
	if _, err := client.Search(context.Background(), "xx", "", 1); err == nil || !strings.Contains(err.Error(), "1 MiB") {
		t.Fatalf("expected size limit error, got %v", err)
	}
}
