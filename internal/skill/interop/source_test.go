package interop

import (
	"path/filepath"
	"strings"
	"testing"
)

const testCommit = "a80e4c9d12d607631249d4d7d059f835d4b2f13a"

func TestParseGitHubShorthandPinned(t *testing.T) {
	ref, err := ParseSourceRef("github:vercel-labs/skills@" + testCommit + "#skills/find-skills")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Kind != SourceGitHub || !ref.Immutable {
		t.Fatalf("unexpected source classification: %#v", ref)
	}
	if ref.Canonical != "github:vercel-labs/skills@"+testCommit+"#skills/find-skills" {
		t.Fatalf("canonical = %q", ref.Canonical)
	}
	if err := RequireImmutable(ref); err != nil {
		t.Fatalf("RequireImmutable() error = %v", err)
	}
}

func TestParseGitHubURLPinned(t *testing.T) {
	raw := "https://github.com/vercel-labs/skills/tree/" + testCommit + "/skills/find-skills"
	ref, err := ParseSourceRef(raw)
	if err != nil {
		t.Fatal(err)
	}
	if ref.Canonical != "github:vercel-labs/skills@"+testCommit+"#skills/find-skills" {
		t.Fatalf("canonical = %q", ref.Canonical)
	}
}

func TestRejectMutableGitHubSources(t *testing.T) {
	for _, raw := range []string{
		"github:vercel-labs/skills@main",
		"https://github.com/vercel-labs/skills",
		"https://github.com/vercel-labs/skills/tree/main/skills/find-skills",
		"https://github.com/vercel-labs/skills/tree/" + testCommit + "?tab=readme",
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := ParseSourceRef(raw); err == nil {
				t.Fatalf("ParseSourceRef(%q) unexpectedly succeeded", raw)
			}
		})
	}
}

func TestRejectGitHubSubpathTraversal(t *testing.T) {
	for _, raw := range []string{
		"github:vercel-labs/skills@" + testCommit + "#../other",
		"github:vercel-labs/skills@" + testCommit + "#skills/../../other",
	} {
		if _, err := ParseSourceRef(raw); err == nil {
			t.Fatalf("ParseSourceRef(%q) accepted traversal", raw)
		}
	}
}

func TestLocalSourceIsMutable(t *testing.T) {
	ref, err := ParseSourceRef("./skills/example")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Kind != SourceLocal || ref.Immutable {
		t.Fatalf("unexpected local ref: %#v", ref)
	}
	if !strings.HasPrefix(ref.Canonical, "local:") {
		t.Fatalf("canonical = %q", ref.Canonical)
	}
	if filepath.Clean(ref.LocalPath) != filepath.Clean("./skills/example") {
		t.Fatalf("local path = %q", ref.LocalPath)
	}
	if err := RequireImmutable(ref); err == nil {
		t.Fatal("RequireImmutable() accepted local mutable source")
	}
}

func TestRejectUnsupportedRemoteScheme(t *testing.T) {
	if _, err := ParseSourceRef("https://example.com/skills/demo"); err == nil {
		t.Fatal("non-GitHub https source unexpectedly accepted by interop contract")
	}
}
