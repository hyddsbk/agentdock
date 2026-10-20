package skill

import (
	"context"
	"testing"

	skillinterop "github.com/uvwt/agentdock/internal/skill/interop"
)

type fakeDiscoveryProvider struct {
	searchItems []skillinterop.SearchCandidate
	pinResult   skillinterop.PinResult
	searchErr   error
	pinErr      error
}

func (f *fakeDiscoveryProvider) Search(context.Context, string, string, int) ([]skillinterop.SearchCandidate, error) {
	return f.searchItems, f.searchErr
}

func (f *fakeDiscoveryProvider) Pin(context.Context, string, string) (skillinterop.PinResult, error) {
	return f.pinResult, f.pinErr
}

func TestSkillDiscoverySearchNeverAuthorizesInstall(t *testing.T) {
	service, _ := newSkillTestService(t)
	service.discovery = &fakeDiscoveryProvider{
		searchItems: []skillinterop.SearchCandidate{{
			Name:              "demo",
			Slug:              "owner/repo/demo",
			Source:            "owner/repo",
			CandidateSHA256:   "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			Pinnable:          true,
			InstallAuthorized: false,
		}},
	}

	result, err := service.Discovery(context.Background(), DiscoveryRequest{
		Action: "search",
		Query:  "demo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["action"] != "search" || result["count"] != 1 {
		t.Fatalf("unexpected search result: %#v", result)
	}
	if result["install_authorized"] != false || result["next_owner"] != "project-due-diligence" {
		t.Fatalf("search governance boundary missing: %#v", result)
	}
}

func TestSkillDiscoveryPinRoutesToDueDiligence(t *testing.T) {
	service, _ := newSkillTestService(t)
	lock := skillinterop.SourceLock{
		Schema:            skillinterop.DiscoveryLockSchema,
		Provider:          "github",
		Repository:        "owner/repo",
		Source:            skillinterop.SourceRef{Kind: skillinterop.SourceGitHub, Canonical: "github:owner/repo@0123456789abcdef0123456789abcdef01234567", Immutable: true, Commit: "0123456789abcdef0123456789abcdef01234567"},
		ReviewRequired:    true,
		InstallAuthorized: false,
		NextOwner:         "project-due-diligence",
	}
	service.discovery = &fakeDiscoveryProvider{
		pinResult: skillinterop.PinResult{
			Lock:       lock,
			LockSHA256: "abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd",
			ResolvedAt: "2026-10-20T12:00:00Z",
		},
	}

	result, err := service.Discovery(context.Background(), DiscoveryRequest{
		Action: "pin",
		Source: "owner/repo",
		Skill:  "demo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["action"] != "pin" || result["review_required"] != true || result["install_authorized"] != false {
		t.Fatalf("unexpected pin result: %#v", result)
	}
	if result["next_owner"] != "project-due-diligence" {
		t.Fatalf("pin bypassed governance route: %#v", result)
	}
}

func TestSkillDiscoveryRejectsMutationActions(t *testing.T) {
	service, _ := newSkillTestService(t)
	for _, action := range []string{"install", "update", "remove", "activate"} {
		if _, err := service.Discovery(context.Background(), DiscoveryRequest{Action: action}); err == nil {
			t.Fatalf("Discovery accepted mutation action %q", action)
		}
	}
}
